// Package tui implements the bubbletea front-end for the orchestrator.
//
// Layout (resized to fit the terminal):
//
//	┌─ avm2azapi ─────────────────────────────────────────────────┐
//	│  Stage: tasks                          12/120 done   2 fail │
//	├──────────── tasks ─────────┬──────── output ────────────────┤
//	│ ✔ planning                 │ [info] creating session ...    │
//	│ ✔ task-1-executor          │ [tool] read track.md           │
//	│ ✔ task-1-checker           │ [assistant] Implementation ... │
//	│ ▶ task-2-executor          │ [done] session idle            │
//	│ … task-2-checker           │                                │
//	│ … task-3-executor          │                                │
//	└────────────────────────────┴────────────────────────────────┘
//	  ↑/↓ select   q quit
//
// The user can move the highlight in the left pane; the right pane mirrors
// the streamed output of the highlighted job.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lonegunmanb/avm2azapi/internal/orchestrator"
)

// updateMsg wraps an orchestrator.Update so bubbletea can dispatch it through
// its message bus.
type updateMsg orchestrator.Update

// orchestratorClosedMsg is sent when the orchestrator's update channel closes.
type orchestratorClosedMsg struct{}

// Subscribe returns a tea.Cmd that pulls the next Update from the channel.
func subscribeCmd(ch <-chan orchestrator.Update) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return orchestratorClosedMsg{}
		}
		return updateMsg(u)
	}
}

// jobItem implements list.Item for the left-pane task list.
type jobItem struct {
	view orchestrator.JobView
}

func (i jobItem) FilterValue() string { return i.view.Job.Title }
func (i jobItem) Title() string       { return statusGlyph(i.view.State) + " " + i.view.Job.Title }
func (i jobItem) Description() string {
	return fmt.Sprintf("stage=%s  state=%s", i.view.Job.Stage, i.view.State)
}

func statusGlyph(s orchestrator.JobState) string {
	switch s {
	case orchestrator.JobSucceeded:
		return "✔"
	case orchestrator.JobFailed:
		return "✘"
	case orchestrator.JobRunning:
		return "▶"
	case orchestrator.JobSkipped:
		return "⊝"
	default:
		return "…"
	}
}

// Model is the bubbletea model.
type Model struct {
	updates <-chan orchestrator.Update

	width, height int
	stage         string
	done          bool
	err           error

	list     list.Model
	output   viewport.Model
	jobs     []orchestrator.JobView
	follow   bool // when true, output pane sticks to the active job
	activeID string
}

// New constructs a fresh TUI model. `ch` is the orchestrator's update channel.
func New(ch <-chan orchestrator.Update) Model {
	const startW, startH = 100, 30

	leftDelegate := list.NewDefaultDelegate()
	leftDelegate.SetSpacing(0)
	leftDelegate.ShowDescription = false
	l := list.New(nil, leftDelegate, startW/2, startH-4)
	l.Title = "Tasks"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)

	vp := viewport.New(startW/2, startH-4)
	vp.SetContent("(no output yet)")

	return Model{
		updates: ch,
		list:    l,
		output:  vp,
		follow:  true,
		width:   startW,
		height:  startH,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return subscribeCmd(m.updates)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "f":
			m.follow = !m.follow
		case "g":
			m.output.GotoTop()
		case "G":
			m.output.GotoBottom()
		}
		// Pass arrow keys / pgup/pgdn to whichever pane is active.
		var cmds []tea.Cmd
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		cmds = append(cmds, cmd)
		// When the user manually navigates, stop auto-following.
		switch msg.String() {
		case "up", "down", "pgup", "pgdown", "home", "end", "k", "j":
			m.follow = false
		}
		m.output, cmd = m.output.Update(msg)
		cmds = append(cmds, cmd)
		m.refreshOutputPane()
		return m, tea.Batch(cmds...)

	case updateMsg:
		o := orchestrator.Update(msg)
		m.applyUpdate(o)
		return m, subscribeCmd(m.updates)

	case orchestratorClosedMsg:
		// Pipeline finished; stay on screen so the user can review.
		return m, nil
	}
	return m, nil
}

func (m *Model) applyUpdate(u orchestrator.Update) {
	m.jobs = u.Snapshot
	m.stage = u.Stage
	m.done = u.Done
	m.err = u.Err
	m.activeID = u.Active

	items := make([]list.Item, len(m.jobs))
	for i, j := range m.jobs {
		items[i] = jobItem{view: j}
	}
	m.list.SetItems(items)

	if m.follow && m.activeID != "" {
		for i, j := range m.jobs {
			if j.Job.ID == m.activeID {
				m.list.Select(i)
				break
			}
		}
	}
	m.refreshOutputPane()
}

func (m *Model) refreshOutputPane() {
	if len(m.jobs) == 0 {
		m.output.SetContent("(no output yet)")
		return
	}
	idx := m.list.Index()
	if idx < 0 || idx >= len(m.jobs) {
		idx = 0
	}
	view := m.jobs[idx]
	m.output.SetContent(renderOutput(view))
	if m.follow {
		m.output.GotoBottom()
	}
}

func (m *Model) relayout() {
	header, footer := 2, 2
	body := m.height - header - footer
	if body < 4 {
		body = 4
	}
	leftW := m.width / 3
	if leftW < 24 {
		leftW = 24
	}
	rightW := m.width - leftW - 2
	if rightW < 20 {
		rightW = 20
	}
	m.list.SetSize(leftW, body)
	m.output.Width = rightW
	m.output.Height = body
}

// View implements tea.Model.
func (m Model) View() string {
	header := headerStyle.Render(m.headerText())
	left := paneStyle.Render(m.list.View())
	right := paneStyle.Render(m.output.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	footer := helpStyle.Render(m.footerText())
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) headerText() string {
	done, fail, running, total := 0, 0, 0, len(m.jobs)
	for _, j := range m.jobs {
		switch j.State {
		case orchestrator.JobSucceeded:
			done++
		case orchestrator.JobFailed:
			fail++
		case orchestrator.JobRunning:
			running++
		}
	}
	stage := m.stage
	if stage == "" {
		stage = "starting"
	}
	suffix := ""
	if m.done {
		if m.err != nil {
			suffix = "  ✘ aborted: " + m.err.Error()
		} else {
			suffix = "  ✔ pipeline complete"
		}
	}
	return fmt.Sprintf("avm2azapi   stage=%s   %d/%d done   %d running   %d failed%s",
		stage, done, total, running, fail, suffix)
}

func (m Model) footerText() string {
	follow := "off"
	if m.follow {
		follow = "on"
	}
	return fmt.Sprintf("↑/↓ select  pgup/pgdn scroll  f follow=%s  q quit", follow)
}

func renderOutput(view orchestrator.JobView) string {
	if len(view.Output) == 0 {
		return fmt.Sprintf("# %s\nstate: %s\n(no output yet)\n", view.Job.Title, view.State)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\nstate: %s   stage=%s\n\n", view.Job.Title, view.State, view.Job.Stage)
	for _, line := range view.Output {
		fmt.Fprintf(&sb, "[%s] %s\n", line.Kind, line.Text)
	}
	if view.Err != nil {
		fmt.Fprintf(&sb, "\nERROR: %s\n", view.Err)
	}
	return sb.String()
}

// styles ---------------------------------------------------------------------

var (
	headerStyle = lipgloss.NewStyle().
		Bold(true).
		Padding(0, 1).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("63"))

	paneStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
		Faint(true).
		Padding(0, 1)
)

// Compile-time interface check for clarity.
var _ tea.Model = Model{}

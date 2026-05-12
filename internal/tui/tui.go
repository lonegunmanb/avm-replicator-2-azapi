// Package tui implements the bubbletea front-end for the orchestrator.
//
// Layout (resized to fit the terminal):
//
//	┌─ avm2azapi ─────────────────────────────────────────────────┐
//	│  Stage: tasks                          12/120 done   2 fail │
//	├──────────── tasks ─────────┬──────── output ────────────────┤
//	│ ✔ planning                 │ [15:04:01] [info] creating ... │
//	│ ✔ task-1-executor          │ [15:04:02] [tool] read track   │
//	│ ✔ task-1-checker           │ [15:04:18] [assistant] Impl... │
//	│ ▶ task-2-executor (0:42)   │ [15:05:00] [done] session idle │
//	│ … task-2-checker           │                                │
//	│ … task-3-executor          │                                │
//	└────────────────────────────┴────────────────────────────────┘
//	  ↑/↓ select  pgup/pgdn scroll  g/G top/bottom  f follow  q quit
//
// The user can move the highlight in the left pane; the right pane mirrors
// the streamed output of the highlighted job.
package tui

import (
	"fmt"
	"strings"
	"time"

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

// tickMsg fires once per second so the TUI re-renders even when no
// orchestrator events arrive — this keeps the elapsed-time counter on the
// active job advancing so the user can see the agent is alive.
type tickMsg time.Time

// tickInterval controls how often the heartbeat tick fires.
const tickInterval = time.Second

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

// tickCmd schedules the next heartbeat tick.
func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
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

	list   list.Model
	output viewport.Model
	jobs   []orchestrator.JobView
	// followActive: when true, the left list selection automatically tracks
	// the orchestrator's currently-active job. Disabled when the user
	// manually moves the selection.
	followActive bool
	// stickBottom: when true, the right output pane sticks to the bottom as
	// new lines stream in. Disabled when the user scrolls up to read history,
	// re-enabled when they scroll back to the bottom.
	stickBottom bool
	activeID    string
	now         time.Time
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
		updates:      ch,
		list:         l,
		output:       vp,
		followActive: true,
		stickBottom:  true,
		width:        startW,
		height:       startH,
		now:          time.Now(),
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(subscribeCmd(m.updates), tickCmd())
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
			// "f" toggles both follow modes together — convenient single
			// shortcut to "stop everything from moving" or "track again".
			on := !(m.followActive && m.stickBottom)
			m.followActive = on
			m.stickBottom = on
			if on {
				m.snapToActive()
				m.refreshOutputPane()
				m.output.GotoBottom()
			}
			return m, nil
		case "g", "home":
			m.output.GotoTop()
			m.stickBottom = false
			return m, nil
		case "G", "end":
			m.output.GotoBottom()
			m.stickBottom = true
			return m, nil
		}

		var cmds []tea.Cmd
		var cmd tea.Cmd

		// Route keys: list-nav keys go to the list (and disable
		// followActive); output-scroll keys go to the viewport (and may
		// toggle stickBottom). This avoids the previous behavior where
		// arrow keys were sent to both panes simultaneously.
		switch msg.String() {
		case "up", "down", "k", "j":
			prev := m.list.Index()
			m.list, cmd = m.list.Update(msg)
			cmds = append(cmds, cmd)
			if m.list.Index() != prev {
				m.followActive = false
			}
			m.refreshOutputPane()
		case "pgup", "pgdown", "ctrl+u", "ctrl+d", "u", "d":
			m.output, cmd = m.output.Update(msg)
			cmds = append(cmds, cmd)
			m.stickBottom = m.output.AtBottom()
		default:
			// Unknown key — let the list handle it (e.g. typeahead).
			m.list, cmd = m.list.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case updateMsg:
		o := orchestrator.Update(msg)
		m.applyUpdate(o)
		return m, subscribeCmd(m.updates)

	case tickMsg:
		m.now = time.Time(msg)
		// Re-render the output pane so the running-job header line picks up
		// the new elapsed-time value even if no new events arrived.
		m.refreshOutputPane()
		return m, tickCmd()

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

	if m.followActive {
		m.snapToActive()
	}
	m.refreshOutputPane()
}

// snapToActive moves the left list selection onto the orchestrator's
// currently-active job, if any.
func (m *Model) snapToActive() {
	if m.activeID == "" {
		return
	}
	for i, j := range m.jobs {
		if j.Job.ID == m.activeID {
			m.list.Select(i)
			return
		}
	}
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
	m.output.SetContent(renderOutput(view, m.now))
	if m.stickBottom {
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
	} else if active := m.activeJobView(); active != nil {
		// Surface the active job's elapsed time so the user can tell at a
		// glance that the agent is doing work, even when no fresh output
		// lines are arriving.
		suffix = fmt.Sprintf("  ▶ %s (%s)", active.Job.ID, formatElapsed(m.now.Sub(active.Started)))
	}
	return fmt.Sprintf("avm2azapi   stage=%s   %d/%d done   %d running   %d failed%s",
		stage, done, total, running, fail, suffix)
}

// activeJobView returns a pointer to the currently-active running job, if any.
func (m Model) activeJobView() *orchestrator.JobView {
	if m.activeID == "" {
		return nil
	}
	for i := range m.jobs {
		if m.jobs[i].Job.ID == m.activeID && m.jobs[i].State == orchestrator.JobRunning {
			return &m.jobs[i]
		}
	}
	return nil
}

func (m Model) footerText() string {
	follow := "off"
	if m.followActive {
		follow = "on"
	}
	stick := "off"
	if m.stickBottom {
		stick = "on"
	}
	return fmt.Sprintf("↑/↓ select  pgup/pgdn scroll  g/G top/bottom  f follow=%s/stick=%s  q quit", follow, stick)
}

func renderOutput(view orchestrator.JobView, now time.Time) string {
	var sb strings.Builder
	// Header line: title, state, stage, elapsed time. The elapsed value
	// updates every tick for running jobs so the user sees activity even
	// when no new event lines arrive.
	elapsed := jobElapsed(view, now)
	fmt.Fprintf(&sb, "# %s\nstate: %s   stage=%s   elapsed=%s\n\n",
		view.Job.Title, view.State, view.Job.Stage, formatElapsed(elapsed))
	if len(view.Output) == 0 {
		sb.WriteString("(no output yet)\n")
	} else {
		for _, line := range view.Output {
			fmt.Fprintf(&sb, "[%s] [%s] %s\n",
				line.When.Format("15:04:05"), line.Kind, line.Text)
		}
	}
	if view.Err != nil {
		fmt.Fprintf(&sb, "\nERROR: %s\n", view.Err)
	}
	return sb.String()
}

// jobElapsed returns the wall-clock duration the job has been (or was) running.
func jobElapsed(view orchestrator.JobView, now time.Time) time.Duration {
	if view.Started.IsZero() {
		return 0
	}
	end := view.Finished
	if end.IsZero() {
		end = now
	}
	d := end.Sub(view.Started)
	if d < 0 {
		return 0
	}
	return d
}

// formatElapsed renders a duration as H:MM:SS or M:SS, truncated to seconds.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
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

// Package orchestrator drives the migration pipeline. It is the Go replacement
// for `migrate.ps1` + `run-coordinator.ps1`.
//
// The pipeline is a linear sequence of "stages":
//
//  1. planning           — one Copilot session that produces track.md
//  2. tasks              — for every Pending row in track.md, run an executor
//                           session followed by a checker session
//  3. prepare_tests       — one Copilot session that drafts acceptance tests
//
// External shell steps from the original script (`newres`, `test_extractor.ps1`,
// `run-all-acctests.ps1`) are intentionally NOT covered here; they are
// non-Copilot work and the user can keep invoking them manually. The Go
// orchestrator focuses on what used to be the `copilot CLI` calls.
//
// The orchestrator does not block on the TUI; it pushes Job descriptors into
// a request channel and reads back the bubbletea-friendly Update events the
// TUI subscribes to. See cmd/avm2azapi for the wiring.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lonegunmanb/avm2azapi/internal/prompts"
	"github.com/lonegunmanb/avm2azapi/internal/runner"
	"github.com/lonegunmanb/avm2azapi/internal/track"
)

// JobState is the lifecycle state of a single Job inside the orchestrator.
type JobState int

const (
	JobPending JobState = iota
	JobRunning
	JobSucceeded
	JobFailed
	JobSkipped
)

func (s JobState) String() string {
	switch s {
	case JobRunning:
		return "running"
	case JobSucceeded:
		return "ok"
	case JobFailed:
		return "failed"
	case JobSkipped:
		return "skipped"
	default:
		return "pending"
	}
}

// JobView is a snapshot of one job, suitable for rendering in a TUI.
type JobView struct {
	Job      runner.Job
	State    JobState
	Started  time.Time
	Finished time.Time
	Err      error
	// Output is the in-memory log of streamed event lines. The orchestrator
	// trims this to MaxOutputLines per job.
	Output []runner.EventLine
}

// MaxOutputLines is the hard cap on lines kept per job.
const MaxOutputLines = 2000

// Update is one TUI-visible state change. The orchestrator publishes Updates
// to its subscriber as work proceeds.
type Update struct {
	Snapshot []JobView // full ordered view of every job known so far
	Active   string    // ID of the currently running job, "" when idle
	Stage    string    // human-readable current pipeline stage
	Done     bool      // true on the *final* update (pipeline finished)
	Err      error     // non-nil if the pipeline aborted
}

// Config controls one orchestrator run.
type Config struct {
	ResourceType string  // e.g. "azurerm_orchestrated_virtual_machine_scale_set"
	WorkDir      string  // directory containing track.md / coordinator.md / etc.
	Backend      runner.Backend
	// MaxTasks limits how many `track.md` rows are processed (0 = no limit).
	// Useful for the TUI demo to keep things short.
	MaxTasks int
	// SkipPlanning skips the planner stage (used when track.md already exists).
	SkipPlanning bool
	// SkipTests skips the prepare_tests stage.
	SkipTests bool
}

// Orchestrator coordinates the pipeline.
type Orchestrator struct {
	cfg    Config
	mu     sync.Mutex
	jobs   []*JobView
	byID   map[string]*JobView
	active string
	stage  string
	subs   []chan Update
}

// New builds an Orchestrator. The config's Backend MUST be non-nil.
func New(cfg Config) (*Orchestrator, error) {
	if cfg.Backend == nil {
		return nil, errors.New("orchestrator: Backend is required")
	}
	if cfg.WorkDir == "" {
		return nil, errors.New("orchestrator: WorkDir is required")
	}
	return &Orchestrator{
		cfg:  cfg,
		byID: make(map[string]*JobView),
	}, nil
}

// Subscribe returns a channel that receives Update snapshots. The channel is
// buffered; the orchestrator drops messages if a subscriber is too slow.
// Subscribers should drain the channel until it is closed (which happens when
// Run returns).
func (o *Orchestrator) Subscribe() <-chan Update {
	ch := make(chan Update, 64)
	o.mu.Lock()
	o.subs = append(o.subs, ch)
	o.mu.Unlock()
	// Push an initial snapshot so the TUI renders something immediately.
	o.publish()
	return ch
}

// Run executes the full pipeline. It returns nil on success or the first
// fatal error encountered.
func (o *Orchestrator) Run(ctx context.Context) error {
	defer o.shutdown()

	// Stage 1 — planning
	if !o.cfg.SkipPlanning {
		if err := o.runStagePlanning(ctx); err != nil {
			return o.fail("planning", err)
		}
	}

	// Stage 2 — tasks
	if err := o.runStageTasks(ctx); err != nil {
		return o.fail("tasks", err)
	}

	// Stage 3 — acceptance test prep
	if !o.cfg.SkipTests {
		if err := o.runStagePrepareTests(ctx); err != nil {
			return o.fail("prepare_tests", err)
		}
	}

	o.setStage("done")
	o.publishDone(nil)
	return nil
}

// ---------------------------------------------------------------------------
// Stage runners
// ---------------------------------------------------------------------------

func (o *Orchestrator) runStagePlanning(ctx context.Context) error {
	o.setStage("planning")
	job := runner.Job{
		ID:     "planning",
		Title:  "Planner — produce track.md",
		Stage:  "planning",
		Prompt: prompts.Planner(o.cfg.ResourceType),
	}
	_, err := o.runJob(ctx, job)
	return err
}

func (o *Orchestrator) runStageTasks(ctx context.Context) error {
	o.setStage("tasks")
	trackPath := filepath.Join(o.cfg.WorkDir, "track.md")
	tasks, err := track.Parse(trackPath)
	if err != nil {
		return fmt.Errorf("parse %s: %w", trackPath, err)
	}
	processed := 0
	firstExecutor := true
	for _, t := range tasks {
		if o.cfg.MaxTasks > 0 && processed >= o.cfg.MaxTasks {
			break
		}
		if t.Status == track.StatusCompleted {
			// Already done; emit a "skipped" job entry for visibility.
			o.recordSkipped(taskExecutorJob(t, o.cfg.ResourceType, false))
			o.recordSkipped(taskCheckerJob(t))
			continue
		}
		exec := taskExecutorJob(t, o.cfg.ResourceType, firstExecutor)
		firstExecutor = false
		if r, err := o.runJob(ctx, exec); err != nil {
			return err
		} else if !r.OK {
			return fmt.Errorf("executor failed for task #%d (%s)", t.No, t.Path)
		}
		check := taskCheckerJob(t)
		if r, err := o.runJob(ctx, check); err != nil {
			return err
		} else if !r.OK {
			return fmt.Errorf("checker failed for task #%d (%s)", t.No, t.Path)
		}
		processed++
	}
	return nil
}

func (o *Orchestrator) runStagePrepareTests(ctx context.Context) error {
	o.setStage("prepare_tests")
	job := runner.Job{
		ID:     "prepare-tests",
		Title:  "Test planner — draft acceptance tests",
		Stage:  "prepare_tests",
		Prompt: prompts.PrepareTests(o.cfg.ResourceType),
	}
	_, err := o.runJob(ctx, job)
	return err
}

// ---------------------------------------------------------------------------
// Job factories
// ---------------------------------------------------------------------------

func taskExecutorJob(t track.Task, resourceType string, isFirst bool) runner.Job {
	id := fmt.Sprintf("task-%d-executor", t.No)
	title := fmt.Sprintf("Executor — Task #%d (%s)", t.No, t.Path)
	var prompt string
	switch {
	case strings.HasPrefix(strings.ToLower(t.Path), "__check_"):
		prompt = prompts.ExecutorHiddenFields(t.No, resourceType)
	case t.IsBlock() && t.IsRoot():
		prompt = prompts.ExecutorBlockSkeleton(t.No, t.Path, resourceType)
	case !t.IsRoot():
		prompt = prompts.ExecutorBlockArgument(t.No, t.Path, resourceType)
	default:
		prompt = prompts.ExecutorArgument(t.No, t.Path, resourceType, isFirst)
	}
	return runner.Job{ID: id, Title: title, Stage: "executor", Prompt: prompt}
}

func taskCheckerJob(t track.Task) runner.Job {
	return runner.Job{
		ID:     fmt.Sprintf("task-%d-checker", t.No),
		Title:  fmt.Sprintf("Checker — Task #%d (%s)", t.No, t.Path),
		Stage:  "checker",
		Prompt: prompts.Checker(t.No, sanitizeFieldName(t.Path)),
	}
}

func sanitizeFieldName(p string) string {
	// Field names appear in proof-doc filenames; collapse "block.field" to "field"
	// for the human-readable token used in the prompt.
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ---------------------------------------------------------------------------
// Job execution & state-tracking
// ---------------------------------------------------------------------------

func (o *Orchestrator) runJob(ctx context.Context, job runner.Job) (runner.Result, error) {
	view := &JobView{Job: job, State: JobRunning, Started: time.Now()}
	o.mu.Lock()
	o.jobs = append(o.jobs, view)
	o.byID[job.ID] = view
	o.active = job.ID
	o.mu.Unlock()
	o.publish()

	out := make(chan runner.EventLine, 64)
	resultCh := make(chan runner.Result, 1)
	go func() { resultCh <- o.cfg.Backend.Run(ctx, job, out) }()

	for line := range out {
		o.appendOutput(view, line)
		o.publish()
	}
	res := <-resultCh

	o.mu.Lock()
	view.Finished = time.Now()
	if res.OK {
		view.State = JobSucceeded
	} else {
		view.State = JobFailed
		view.Err = res.Err
	}
	o.active = ""
	o.mu.Unlock()
	o.publish()
	return res, nil
}

func (o *Orchestrator) recordSkipped(job runner.Job) {
	view := &JobView{Job: job, State: JobSkipped, Started: time.Now(), Finished: time.Now()}
	o.mu.Lock()
	o.jobs = append(o.jobs, view)
	o.byID[job.ID] = view
	o.mu.Unlock()
	o.publish()
}

func (o *Orchestrator) appendOutput(view *JobView, line runner.EventLine) {
	o.mu.Lock()
	defer o.mu.Unlock()
	view.Output = append(view.Output, line)
	if len(view.Output) > MaxOutputLines {
		view.Output = view.Output[len(view.Output)-MaxOutputLines:]
	}
}

func (o *Orchestrator) setStage(s string) {
	o.mu.Lock()
	o.stage = s
	o.mu.Unlock()
	o.publish()
}

func (o *Orchestrator) snapshot() Update {
	o.mu.Lock()
	defer o.mu.Unlock()
	jobs := make([]JobView, len(o.jobs))
	for i, j := range o.jobs {
		jobs[i] = *j
		// Defensive copy of the slice header so subscribers can't mutate ours.
		jobs[i].Output = append([]runner.EventLine(nil), j.Output...)
	}
	return Update{Snapshot: jobs, Active: o.active, Stage: o.stage}
}

func (o *Orchestrator) publish() {
	u := o.snapshot()
	o.mu.Lock()
	subs := append([]chan Update(nil), o.subs...)
	o.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- u:
		default:
			// Subscriber too slow; drop. The next publish will catch it up.
		}
	}
}

func (o *Orchestrator) publishDone(err error) {
	u := o.snapshot()
	u.Done = true
	u.Err = err
	o.mu.Lock()
	subs := append([]chan Update(nil), o.subs...)
	o.mu.Unlock()
	for _, ch := range subs {
		// On the terminal update we *want* to block briefly so the TUI sees it.
		select {
		case ch <- u:
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (o *Orchestrator) fail(stage string, err error) error {
	o.setStage(stage + " (failed)")
	o.publishDone(err)
	return err
}

func (o *Orchestrator) shutdown() {
	o.mu.Lock()
	subs := o.subs
	o.subs = nil
	o.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

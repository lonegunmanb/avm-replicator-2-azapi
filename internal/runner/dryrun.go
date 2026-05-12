package runner

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DryRunBackend is a Backend that simulates a Copilot session without spawning
// any subprocess. It produces a small scripted stream of lines based on the
// job's prompt so the TUI can be exercised end-to-end (and the orchestrator's
// state machine can be smoke-tested) without an authenticated Copilot CLI.
//
// The simulated session always succeeds.
type DryRunBackend struct {
	StepDelay time.Duration // between simulated lines (default 200ms)
}

// NewDryRunBackend returns a DryRunBackend with sensible defaults.
func NewDryRunBackend() *DryRunBackend {
	return &DryRunBackend{StepDelay: 200 * time.Millisecond}
}

// Close is a no-op for the simulator.
func (b *DryRunBackend) Close() error { return nil }

// Run streams a canned series of lines and returns success.
func (b *DryRunBackend) Run(ctx context.Context, job Job, out chan<- EventLine) Result {
	defer close(out)
	start := time.Now()

	emit(out, EventInfo, fmt.Sprintf("[dry-run] would create session for stage=%s", job.Stage))
	emit(out, EventInfo, "[dry-run] prompt preview: "+previewPrompt(job.Prompt))

	steps := []EventLine{
		{Kind: EventTool, Text: "[dry-run] read track.md"},
		{Kind: EventTool, Text: "[dry-run] query_terraform_block_implementation_source_code"},
		{Kind: EventAssistant, Text: "Investigating provider source for: " + job.Title},
		{Kind: EventTool, Text: "[dry-run] edit migrate_main.tf"},
		{Kind: EventAssistant, Text: "Implementation complete; updated migrate files."},
		{Kind: EventTool, Text: "[dry-run] update track.md status"},
		{Kind: EventDone, Text: "session idle"},
	}
	for _, s := range steps {
		select {
		case <-ctx.Done():
			emit(out, EventError, "cancelled: "+ctx.Err().Error())
			return Result{JobID: job.ID, OK: false, Err: ctx.Err(), Duration: time.Since(start)}
		case <-time.After(b.StepDelay):
		}
		s.When = time.Now()
		emit(out, s.Kind, s.Text)
	}

	return Result{JobID: job.ID, OK: true, Duration: time.Since(start)}
}

func previewPrompt(p string) string {
	p = strings.ReplaceAll(p, "\n", " ")
	if len(p) > 120 {
		return p[:117] + "..."
	}
	return p
}

// Package runner runs a single Copilot session for one prompt and streams its
// output as a series of EventLine values. A Backend abstraction lets us swap
// the real `github.com/github/copilot-sdk/go` implementation for an in-process
// simulator that is useful for local development and for the TUI demo when no
// authenticated Copilot CLI is available.
package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

// EventKind classifies a streamed line for color/icon rendering in the TUI.
type EventKind int

const (
	EventInfo EventKind = iota
	EventAssistant
	EventTool
	EventError
	EventDone
)

func (k EventKind) String() string {
	switch k {
	case EventAssistant:
		return "assistant"
	case EventTool:
		return "tool"
	case EventError:
		return "error"
	case EventDone:
		return "done"
	default:
		return "info"
	}
}

// EventLine is one line of human-readable output emitted by a session.
type EventLine struct {
	When time.Time
	Kind EventKind
	Text string
}

// Job describes a single Copilot session that should be run.
type Job struct {
	// ID is a stable identifier the TUI uses to display the job.
	ID string
	// Title is shown in the TUI's task list, e.g. "Executor — Task #23 (zones)".
	Title string
	// Stage is the high-level pipeline phase ("planning", "executor", ...).
	Stage string
	// Prompt is the text sent to Copilot.
	Prompt string
	// Model is optional; defaults to the backend's default.
	Model string
}

// Result summarizes a finished job.
type Result struct {
	JobID    string
	OK       bool
	Err      error
	Duration time.Duration
}

// Backend is the pluggable session-execution strategy.
//
// Run launches one session for `job` and streams its output to `out`. It MUST
// close `out` exactly once before returning. A nil error and OK=true means the
// session reached an idle state successfully.
type Backend interface {
	Run(ctx context.Context, job Job, out chan<- EventLine) Result
	Close() error
}

// ----------------------------------------------------------------------------
// SDKBackend — the real implementation backed by github.com/github/copilot-sdk/go
// ----------------------------------------------------------------------------

// SDKBackend wraps a single shared *copilot.Client and creates one session per
// Job. The Copilot CLI binary must be installed and authenticated on the host.
type SDKBackend struct {
	mu     sync.Mutex
	client *copilot.Client
	cwd    string
	model  string
}

// NewSDKBackend constructs (but does not Start) a Copilot SDK client.
// `cwd` is the working directory used for spawned CLI processes.
// `defaultModel` is used when a Job does not set its own.
func NewSDKBackend(cwd, defaultModel string) *SDKBackend {
	if defaultModel == "" {
		defaultModel = "claude-sonnet-4.5"
	}
	return &SDKBackend{cwd: cwd, model: defaultModel}
}

// ensureClient lazily starts the SDK client.
func (b *SDKBackend) ensureClient(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		return nil
	}
	c := copilot.NewClient(&copilot.ClientOptions{
		Cwd:      b.cwd,
		LogLevel: "error",
	})
	if err := c.Start(ctx); err != nil {
		return fmt.Errorf("start copilot client: %w", err)
	}
	b.client = c
	return nil
}

// Close stops the underlying Copilot client.
func (b *SDKBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil {
		return nil
	}
	err := b.client.Stop()
	b.client = nil
	return err
}

// Run executes the job and streams its events.
func (b *SDKBackend) Run(ctx context.Context, job Job, out chan<- EventLine) Result {
	defer close(out)
	start := time.Now()

	if err := b.ensureClient(ctx); err != nil {
		emit(out, EventError, "failed to start copilot client: "+err.Error())
		return Result{JobID: job.ID, OK: false, Err: err, Duration: time.Since(start)}
	}

	model := job.Model
	if model == "" {
		model = b.model
	}

	emit(out, EventInfo, fmt.Sprintf("creating session (model=%s, stage=%s)", model, job.Stage))
	session, err := b.client.CreateSession(ctx, &copilot.SessionConfig{
		Model:               model,
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
	})
	if err != nil {
		emit(out, EventError, "create session: "+err.Error())
		return Result{JobID: job.ID, OK: false, Err: err, Duration: time.Since(start)}
	}
	defer session.Disconnect() //nolint:errcheck // best-effort cleanup

	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		switch d := event.Data.(type) {
		case *copilot.AssistantMessageData:
			if strings.TrimSpace(d.Content) != "" {
				emit(out, EventAssistant, d.Content)
			}
		case *copilot.SessionIdleData:
			_ = d
			emit(out, EventDone, "session idle")
			closeDone()
		default:
			// Surface unknown event types as low-priority info lines so the
			// user can see *something* happening in the TUI.
			emit(out, EventInfo, fmt.Sprintf("event: %s", event.Type))
		}
	})
	defer unsubscribe()

	if _, err := session.Send(ctx, copilot.MessageOptions{Prompt: job.Prompt}); err != nil {
		emit(out, EventError, "send: "+err.Error())
		return Result{JobID: job.ID, OK: false, Err: err, Duration: time.Since(start)}
	}

	select {
	case <-done:
		return Result{JobID: job.ID, OK: true, Duration: time.Since(start)}
	case <-ctx.Done():
		emit(out, EventError, "cancelled: "+ctx.Err().Error())
		return Result{JobID: job.ID, OK: false, Err: ctx.Err(), Duration: time.Since(start)}
	}
}

// emit safely sends an EventLine, dropping it if the receiver has gone away.
func emit(out chan<- EventLine, kind EventKind, text string) {
	defer func() { _ = recover() }() // out may be closed during shutdown
	out <- EventLine{When: time.Now(), Kind: kind, Text: text}
}

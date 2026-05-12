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
	// Usage, when non-nil, carries structured token-accounting data for this
	// line. Only `assistant.usage` events populate it; the orchestrator sums
	// these into a per-pipeline running total surfaced in the TUI header.
	Usage *TokenUsage
}

// TokenUsage is a per-API-call token snapshot extracted from a Copilot
// `assistant.usage` event. Values are non-negative; missing SDK fields decode
// as zero.
type TokenUsage struct {
	Input      int64
	Output     int64
	Reasoning  int64 // subset of Output for chain-of-thought models
	CacheRead  int64
	CacheWrite int64
	Model      string
}

// Per-line truncation caps for the chat/intent events we surface in the TUI.
// Right pane is line-oriented; very long blobs make scrolling unpleasant and
// blow past `MaxOutputLines` quickly. The user explicitly OK'd head-truncation.
const (
	maxAssistantMsgChars = 2000
	maxUserMsgChars      = 800
	maxSystemMsgChars    = 400
	maxIntentChars       = 240
)

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
	// "Yolo" wiring: both permission gates must be open for the embedded
	// Copilot CLI runtime to proceed without interactive prompts.
	//
	//  1. OnPermissionRequest is the user-intent layer. In SDK v1.0.0-beta.3
	//     PermissionHandler.ApproveAll returns PermissionRequestResultKindApproved
	//     which is the string "approve-once" — the value the embedded
	//     `@github/copilot` CLI accepts. (Older SDKs returned "approved" and
	//     would hang with `unexpected user permission response`.)
	//  2. Hooks.OnPreToolUse is a *separate* gate. Even after user-intent is
	//     approved, an unset hook leaves SDK defaults in place and tool calls
	//     can still be blocked. Always returning {PermissionDecision: "allow"}
	//     wires the second gate fully open.
	//
	// Both layers are required; pinned by a regression test in runner_test.go.
	session, err := b.client.CreateSession(ctx, &copilot.SessionConfig{
		Model:               model,
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		Hooks: &copilot.SessionHooks{
			OnPreToolUse: func(_ copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
				return &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}, nil
			},
		},
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
		case *copilot.UserMessageData:
			if c := strings.TrimSpace(d.Content); c != "" {
				emit(out, EventInfo, "user: "+truncateForLog(c, maxUserMsgChars))
			}
		case *copilot.SystemMessageData:
			if c := strings.TrimSpace(d.Content); c != "" {
				emit(out, EventInfo, fmt.Sprintf("system[%s]: %s", d.Role, truncateForLog(c, maxSystemMsgChars)))
			}
		case *copilot.AssistantIntentData:
			if c := strings.TrimSpace(d.Intent); c != "" {
				emit(out, EventInfo, "intent: "+truncateForLog(c, maxIntentChars))
			}
		case *copilot.AssistantMessageData:
			if c := strings.TrimSpace(d.Content); c != "" {
				emit(out, EventAssistant, truncateForLog(c, maxAssistantMsgChars))
			}
		case *copilot.AssistantUsageData:
			u := tokenUsageFromSDK(d)
			emitUsage(out, formatUsageLine(u), u)
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

// emitUsage is like emit but attaches a structured TokenUsage payload so the
// orchestrator can sum tokens across the whole pipeline.
func emitUsage(out chan<- EventLine, text string, usage TokenUsage) {
	defer func() { _ = recover() }()
	u := usage // copy so caller's variable can't be mutated through the pointer
	out <- EventLine{When: time.Now(), Kind: EventInfo, Text: text, Usage: &u}
}

// truncateForLog returns the first `max` runes of s with a "…(truncated, N
// chars)" suffix when s exceeds the cap. Operates on runes so multi-byte
// content (CJK, emoji) isn't sliced mid-codepoint.
func truncateForLog(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + fmt.Sprintf("…(truncated, %d chars)", len(r)-max)
}

// tokenUsageFromSDK projects an `assistant.usage` event into our flat
// TokenUsage struct. SDK fields are *float64 (JSON number, optional);
// missing values become zero.
func tokenUsageFromSDK(d *copilot.AssistantUsageData) TokenUsage {
	return TokenUsage{
		Input:      derefFloat(d.InputTokens),
		Output:     derefFloat(d.OutputTokens),
		Reasoning:  derefFloat(d.ReasoningTokens),
		CacheRead:  derefFloat(d.CacheReadTokens),
		CacheWrite: derefFloat(d.CacheWriteTokens),
		Model:      d.Model,
	}
}

func derefFloat(p *float64) int64 {
	if p == nil {
		return 0
	}
	return int64(*p)
}

// formatUsageLine produces the human-readable summary written to the right
// pane for one assistant.usage event.
func formatUsageLine(u TokenUsage) string {
	return fmt.Sprintf("usage[%s]: in=%d out=%d (reasoning=%d cache_r=%d cache_w=%d)",
		u.Model, u.Input, u.Output, u.Reasoning, u.CacheRead, u.CacheWrite)
}

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lonegunmanb/avm2azapi/internal/runner"
	"github.com/lonegunmanb/avm2azapi/replicator"
)

// prepareWorkDir extracts embedded prompt files into dir and removes any
// stale manually-copied replicator/ subdirectory.
func prepareWorkDir(dir string) {
	if err := replicator.PrepareWorkDir(dir); err != nil {
		fatal(fmt.Errorf("prepare work dir: %w", err))
	}
}

// signalContext returns a context that is cancelled on SIGINT / SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// runJobPrint runs a single Copilot job and streams its output to stdout.
// Returns true when the job reports success.
func runJobPrint(ctx context.Context, backend runner.Backend, job runner.Job) bool {
	out := make(chan runner.EventLine, 64)
	rc := make(chan runner.Result, 1)
	go func() { rc <- backend.Run(ctx, job, out) }()
	for line := range out {
		fmt.Printf("[%s] %s\n", line.Kind, line.Text)
	}
	return (<-rc).OK
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fatal:", err)
	os.Exit(1)
}

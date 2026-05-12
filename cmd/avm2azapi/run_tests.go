package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lonegunmanb/avm2azapi/internal/prompts"
	"github.com/lonegunmanb/avm2azapi/internal/runner"
	"github.com/lonegunmanb/avm2azapi/internal/testcases"
)

// cmdRunTests is the Go replacement for run-all-acctests.ps1.
// It reads test_cases.md, filters the cases to run, and invokes a Copilot
// session for each one using the terraform-test.md tester role.
func cmdRunTests(args []string) {
	fs := flag.NewFlagSet("run-tests", flag.ExitOnError)
	dir := fs.String("dir", ".", "working directory")
	model := fs.String("model", "claude-sonnet-4.5", "Copilot model")
	rerun := fs.Bool("rerun", false, "also run cases already marked as test success")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	prepareWorkDir(*dir)

	tcFile := filepath.Join(*dir, "test_cases.md")
	all, err := testcases.Parse(tcFile)
	if err != nil {
		fatal(err)
	}

	toRun := filterForTestRun(all, *rerun)
	fmt.Printf("total: %d  to run: %d  skipped: %d\n", len(all), len(toRun), len(all)-len(toRun))

	if len(toRun) == 0 {
		fmt.Println("nothing to run")
		return
	}

	ctx, cancel := signalContext()
	defer cancel()

	backend := runner.NewSDKBackend(*dir, *model)
	defer backend.Close() //nolint:errcheck

	breakFile := filepath.Join(*dir, "break")

	for _, tc := range toRun {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(1)
		default:
		}

		fmt.Printf("\n=== running test: %s ===\n", tc.Name)
		ok := runJobPrint(ctx, backend, runner.Job{
			ID:     "test-" + tc.Name,
			Title:  "Test " + tc.Name,
			Stage:  "run-tests",
			Prompt: prompts.RunAccTest(tc.Name),
		})
		if !ok {
			fmt.Fprintf(os.Stderr, "test failed: %s  (continuing; use Ctrl-C to abort)\n", tc.Name)
		}

		// Honor a "break" sentinel file so operators can stop the run gracefully.
		if _, err := os.Stat(breakFile); err == nil {
			fmt.Println("\nbreak file detected — stopping")
			break
		}
	}
}

// filterForTestRun returns the subset of cases that should be run.
// Cases are excluded when:
//   - extraction status or test status contains "invalid"
//   - extraction status is "Skipped"
//   - test status contains "test success" (unless -rerun is set)
func filterForTestRun(cases []testcases.TestCase, rerun bool) []testcases.TestCase {
	var out []testcases.TestCase
	for _, tc := range cases {
		es := strings.ToLower(tc.ExtractionStatus)
		ts := strings.ToLower(tc.TestStatus)
		if strings.Contains(es, "invalid") || strings.Contains(ts, "invalid") {
			continue
		}
		if strings.Contains(es, "skipped") {
			continue
		}
		if !rerun && strings.Contains(ts, "test success") {
			continue
		}
		out = append(out, tc)
	}
	return out
}

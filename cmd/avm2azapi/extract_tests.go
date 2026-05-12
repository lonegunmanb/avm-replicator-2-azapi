package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lonegunmanb/avm2azapi/internal/prompts"
	"github.com/lonegunmanb/avm2azapi/internal/runner"
	"github.com/lonegunmanb/avm2azapi/internal/testcases"
)

// cmdExtractTests is the Go replacement for test_extractor.ps1.
// It reads test_cases.md, finds Pending rows, and runs a Copilot session for
// each one to extract + convert the test case.
func cmdExtractTests(args []string) {
	fs := flag.NewFlagSet("extract-tests", flag.ExitOnError)
	dir := fs.String("dir", ".", "working directory (must contain test_cases.md)")
	model := fs.String("model", "claude-sonnet-4.5", "Copilot model")
	maxCases := fs.Int("max-cases", 0, "cap how many cases to process (0 = all)")
	dryRun := fs.Bool("dry-run", false, "print what would run without calling Copilot")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	prepareWorkDir(*dir)

	tcFile := filepath.Join(*dir, "test_cases.md")
	all, err := testcases.Parse(tcFile)
	if err != nil {
		fatal(err)
	}

	pending := filterByExtractionStatus(all, testcases.StatusPending)
	if *maxCases > 0 && len(pending) > *maxCases {
		pending = pending[:*maxCases]
	}

	fmt.Printf("total: %d  pending: %d\n", len(all), len(pending))

	if *dryRun {
		for _, tc := range pending {
			fmt.Printf("  would process: %s\n", tc.Name)
		}
		return
	}

	if len(pending) == 0 {
		fmt.Println("nothing to do")
		return
	}

	ctx, cancel := signalContext()
	defer cancel()

	backend := runner.NewSDKBackend(*dir, *model)
	defer backend.Close() //nolint:errcheck

	success, fail := 0, 0
	for _, tc := range pending {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(1)
		default:
		}

		if uErr := testcases.UpdateExtractionStatus(tcFile, tc.Name, testcases.StatusInProgress); uErr != nil {
			fmt.Fprintf(os.Stderr, "warn: set in-progress for %q: %v\n", tc.Name, uErr)
		}

		fmt.Printf("\n=== extracting: %s ===\n", tc.Name)
		ok := runJobPrint(ctx, backend, runner.Job{
			ID:     "extract-" + tc.Name,
			Title:  "Extract " + tc.Name,
			Stage:  "extract-tests",
			Prompt: prompts.ExtractTestCase(tc.Name),
		})

		newStatus := testcases.StatusCompleted
		if !ok {
			newStatus = testcases.StatusPending
			fail++
		} else {
			success++
		}
		if uErr := testcases.UpdateExtractionStatus(tcFile, tc.Name, newStatus); uErr != nil {
			fmt.Fprintf(os.Stderr, "warn: update status for %q: %v\n", tc.Name, uErr)
		}
	}

	fmt.Printf("\nextracted: %d  failed: %d\n", success, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

func filterByExtractionStatus(cases []testcases.TestCase, status string) []testcases.TestCase {
	var out []testcases.TestCase
	for _, tc := range cases {
		if tc.ExtractionStatus == status {
			out = append(out, tc)
		}
	}
	return out
}

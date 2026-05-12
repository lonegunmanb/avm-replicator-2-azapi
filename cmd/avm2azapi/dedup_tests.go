package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lonegunmanb/avm2azapi/internal/testcases"
)

// cmdDedupTests is the Go replacement for deduplicate_tests.ps1.
// It scans <acc-dir>/<caseName>/ directories, computes MD5(azurerm.tf+main.tf)
// for each, and marks duplicates as "Skipped" in test_cases.md.
func cmdDedupTests(args []string) {
	fs := flag.NewFlagSet("dedup-tests", flag.ExitOnError)
	dir := fs.String("dir", ".", "working directory (contains test_cases.md)")
	accDir := fs.String("acc-dir", "azurermacctest", "acceptance test directory (relative to -dir)")
	dryRun := fs.Bool("dry-run", false, "report duplicates without updating test_cases.md")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	prepareWorkDir(*dir)

	tcFile := filepath.Join(*dir, "test_cases.md")
	testRoot := filepath.Join(*dir, *accDir)

	res, err := testcases.Deduplicate(tcFile, testRoot, *dryRun, func(s string) {
		fmt.Println(s)
	})
	if err != nil {
		fatal(err)
	}

	fmt.Printf("\nunique: %d  duplicates: %d  errors: %d\n", res.Unique, res.Duplicates, res.Errors)
	if *dryRun && res.Duplicates > 0 {
		fmt.Println("dry-run: no changes written; re-run without -dry-run to apply")
	}
}

// Package testcases parses and updates the test_cases.md file that drives
// the acceptance-test pipeline.
//
// Expected table format (columns separated by "|"):
//
//	| case name | provider test URL | extraction status | test status |
package testcases

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Extraction status values used in the third column of test_cases.md.
const (
	StatusPending    = "Pending"
	StatusInProgress = "In Progress"
	StatusCompleted  = "Completed"
	StatusSkipped    = "Skipped"
)

// TestCase is one data row of test_cases.md.
type TestCase struct {
	Name             string // column 1
	URL              string // column 2
	ExtractionStatus string // column 3 (Pending / In Progress / Completed / Skipped / …)
	TestStatus       string // column 4 (empty / "test success" / "invalid" / …)
}

// Parse reads test_cases.md from path and returns its rows in file order.
// Header and separator rows are silently skipped.
func Parse(path string) ([]TestCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var cases []TestCase
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		cols := splitRow(scanner.Text())
		if len(cols) < 3 {
			continue
		}
		name := cols[0]
		// skip header / separator rows
		if name == "" ||
			strings.EqualFold(name, "case name") ||
			strings.HasPrefix(name, "---") {
			continue
		}
		tc := TestCase{
			Name:             name,
			URL:              cols[1],
			ExtractionStatus: cols[2],
		}
		if len(cols) >= 4 {
			tc.TestStatus = cols[3]
		}
		cases = append(cases, tc)
	}
	return cases, scanner.Err()
}

// UpdateExtractionStatus replaces the extraction-status column for the row
// whose first column matches caseName.
func UpdateExtractionStatus(path, caseName, newStatus string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	escaped := regexp.QuoteMeta(caseName)
	// Match: | <caseName> | <anything> | <old-status> |
	pat := regexp.MustCompile(`(\|\s*` + escaped + `\s*\|[^|]*\|\s*)([^|]+?)(\s*\|)`)
	updated := pat.ReplaceAllString(string(content), "${1}"+newStatus+"${3}")
	if updated == string(content) {
		return fmt.Errorf("case %q not found in %s", caseName, path)
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// splitRow splits a markdown table row into trimmed cell values, stripping the
// leading/trailing pipe characters.
//
//	"| foo | bar | baz |" → ["foo", "bar", "baz"]
func splitRow(line string) []string {
	parts := strings.Split(line, "|")
	// parts[0] is text before first "|"; parts[len-1] is after last "|"
	var cols []string
	for i := 1; i < len(parts)-1; i++ {
		cols = append(cols, strings.TrimSpace(parts[i]))
	}
	return cols
}

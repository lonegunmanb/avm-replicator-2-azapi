package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// cmdCleanupRGs is the Go replacement for cleanup-test-rgs.ps1.
// It lists Azure resource groups matching "acctestRG-<number>", checks their
// activity logs, and deletes those created more than -days days ago.
// Requires the "az" CLI to be installed and authenticated.
func cmdCleanupRGs(args []string) {
	fs := flag.NewFlagSet("cleanup-rgs", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be deleted without deleting")
	days := fs.Int("days", 4, "delete RGs whose creation event is older than this many days")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	threshold := time.Now().AddDate(0, 0, -*days)
	pattern := regexp.MustCompile(`^acctestRG-\d+$`)

	// List all resource group names.
	out, err := exec.Command("az", "group", "list", "--query", "[].name", "-o", "tsv").Output()
	if err != nil {
		fatal(fmt.Errorf("az group list: %w", err))
	}

	rgNames := strings.Fields(strings.TrimSpace(string(out)))
	if len(rgNames) == 0 {
		fmt.Println("no resource groups found")
		return
	}

	deleted, skipped := 0, 0

	for _, rg := range rgNames {
		if !pattern.MatchString(rg) {
			continue
		}
		fmt.Printf("checking %-40s  ", rg)

		// Query the earliest write event (creation) within the last 90 days.
		query := "sort_by([?operationName.value=='Microsoft.Resources/subscriptions/resourceGroups/write'], &eventTimestamp)[0].eventTimestamp"
		logOut, logErr := exec.Command(
			"az", "monitor", "activity-log", "list",
			"--resource-group", rg,
			"--offset", "90d",
			"--query", query,
			"--output", "tsv",
		).Output()

		shouldDelete := false
		reason := ""

		if logErr != nil || strings.TrimSpace(string(logOut)) == "" {
			// No log found within 90 days → created more than 90 days ago.
			shouldDelete = true
			reason = "created >90d ago (no log)"
		} else {
			ts := strings.TrimSpace(string(logOut))
			t, parseErr := parseAzureTimestamp(ts)
			if parseErr != nil {
				fmt.Printf("warn: cannot parse timestamp %q: %v\n", ts, parseErr)
				skipped++
				continue
			}
			if t.Before(threshold) {
				shouldDelete = true
				age := time.Since(t).Hours() / 24
				reason = fmt.Sprintf("created %.1f days ago", age)
			}
		}

		if shouldDelete {
			if *dryRun {
				fmt.Printf("[dry-run] would delete  (%s)\n", reason)
				deleted++
			} else {
				fmt.Printf("deleting (%s) ... ", reason)
				if delErr := exec.Command("az", "group", "delete", "--name", rg, "--yes", "--no-wait").Run(); delErr != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", delErr)
				} else {
					fmt.Println("ok")
					deleted++
				}
			}
		} else {
			fmt.Println("recent, skipping")
			skipped++
		}
	}

	fmt.Printf("\ndeleted: %d  skipped: %d\n", deleted, skipped)
}

// parseAzureTimestamp tries several formats that the Azure CLI returns.
func parseAzureTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999+00:00",
		"2006-01-02T15:04:05Z",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp format: %q", s)
}

package testcases

import (
	"crypto/md5" //nolint:gosec // content fingerprint, not security
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DedupResult summarises a Deduplicate run.
type DedupResult struct {
	Unique     int
	Duplicates int
	Errors     int
}

// Deduplicate scans <accDir>/<caseName>/{azurerm.tf,main.tf}, computes
// MD5(azurerm.tf+main.tf) for each directory, and marks duplicates as
// "Skipped" in tcFile. The first-seen case (sorted alphabetically) wins.
//
// log is called with a human-readable progress line for every case; pass nil
// to disable logging.
func Deduplicate(tcFile, accDir string, dryRun bool, log func(string)) (DedupResult, error) {
	if log == nil {
		log = func(string) {}
	}

	entries, err := os.ReadDir(accDir)
	if err != nil {
		return DedupResult{}, fmt.Errorf("read %s: %w", accDir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	seen := map[[16]byte]string{}
	var res DedupResult

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		azurermBytes, err1 := os.ReadFile(filepath.Join(accDir, name, "azurerm.tf"))
		mainBytes, err2 := os.ReadFile(filepath.Join(accDir, name, "main.tf"))
		if err1 != nil || err2 != nil {
			log(fmt.Sprintf("warn: %s: missing azurerm.tf or main.tf, skipping", name))
			res.Errors++
			continue
		}
		combined := append(azurermBytes, mainBytes...)
		h := md5.Sum(combined) //nolint:gosec

		if orig, dup := seen[h]; dup {
			log(fmt.Sprintf("duplicate: %s == %s", name, orig))
			if !dryRun {
				if uErr := UpdateExtractionStatus(tcFile, name, StatusSkipped); uErr != nil {
					log(fmt.Sprintf("warn: %v", uErr))
				}
			}
			res.Duplicates++
		} else {
			seen[h] = name
			res.Unique++
		}
	}
	return res, nil
}

// Package replicator embeds all AI-agent prompt (.md) files and provides a
// helper that installs them into a working directory at runtime.
//
// The binary is therefore self-contained: users no longer need to copy the
// replicator/ folder manually before running avm2azapi.
package replicator

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed *.md
var FS embed.FS

// PrepareWorkDir removes any stale "replicator" subdirectory inside dir, then
// writes every embedded .md file directly into dir (flat layout).
// Call this once at startup before the orchestrator or any subcommand runs.
func PrepareWorkDir(dir string) error {
	// Guard against an old manual-copy layout where the whole replicator/
	// directory was copied as a subdirectory instead of its contents.
	_ = os.RemoveAll(filepath.Join(dir, "replicator"))

	return fs.WalkDir(FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := FS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, path), data, 0o644)
	})
}

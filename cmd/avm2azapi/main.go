// Command avm2azapi is a Go orchestrator for the AVM AzureRM-to-AzAPI
// replication workflow. It replaces the previous PowerShell pipeline
// (`migrate.ps1` + `run-coordinator.ps1`) and the in-prompt `copilot -p ...`
// fan-out pattern that the Coordinator agent used to spawn Executor / Checker
// sub-agents.
//
// Now Go is the *only* spawner: it uses `github.com/github/copilot-sdk/go` to
// programmatically open one Copilot session per task, and a Bubble Tea TUI
// shows live progress (done / in-progress / remaining) plus a selectable
// per-job output viewer.
//
// Usage:
//
//	avm2azapi -resource azurerm_orchestrated_virtual_machine_scale_set \
//	          -dir   ./replicator
//
// Flags:
//
//	-resource    AzureRM resource type to migrate (required when -skip-planning is false)
//	-dir         Working directory containing track.md / coordinator.md / etc.
//	-dry-run     Use the in-process simulator instead of the real Copilot SDK
//	-max-tasks   Cap how many track.md tasks to process (0 = no cap)
//	-skip-planning  Skip the planner stage (track.md already exists)
//	-skip-tests     Skip the acceptance-test prep stage
//	-model       Override the default Copilot model (default: claude-sonnet-4.5)
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lonegunmanb/avm2azapi/internal/orchestrator"
	"github.com/lonegunmanb/avm2azapi/internal/runner"
	"github.com/lonegunmanb/avm2azapi/internal/tui"
)

func main() {
	// Subcommand dispatch — must happen before flag.Parse() so that subcommand
	// flags are not mistakenly consumed by the top-level FlagSet.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "extract-tests":
			cmdExtractTests(os.Args[2:])
			return
		case "dedup-tests":
			cmdDedupTests(os.Args[2:])
			return
		case "run-tests":
			cmdRunTests(os.Args[2:])
			return
		case "cleanup-rgs":
			cmdCleanupRGs(os.Args[2:])
			return
		}
	}

	var (
		resource     = flag.String("resource", "", "AzureRM resource type to migrate (required unless -skip-planning)")
		workDir      = flag.String("dir", ".", "working directory (must contain track.md once planning is done)")
		dryRun       = flag.Bool("dry-run", false, "use the in-process simulator instead of the real Copilot SDK")
		maxTasks     = flag.Int("max-tasks", 0, "cap how many track.md tasks to process (0 = no cap)")
		skipNewres   = flag.Bool("skip-newres", false, "skip the initial `newres` scaffolding stage")
		skipPlanning = flag.Bool("skip-planning", false, "skip the planner stage")
		skipTests    = flag.Bool("skip-tests", false, "skip ALL test stages (prepare/extract/dedup/run/final_check)")
		accTestDir   = flag.String("acc-test-dir", "azurermacctest", "directory holding generated acceptance tests")
		model        = flag.String("model", "claude-sonnet-4.5", "Copilot model name")
		noTUI        = flag.Bool("no-tui", false, "stream events to stdout instead of starting the TUI")
	)
	flag.Parse()

	if !*skipPlanning && *resource == "" {
		fmt.Fprintln(os.Stderr, "error: -resource is required unless -skip-planning is set")
		flag.Usage()
		os.Exit(2)
	}

	prepareWorkDir(*workDir)

	var backend runner.Backend
	if *dryRun {
		backend = runner.NewDryRunBackend()
	} else {
		backend = runner.NewSDKBackend(*workDir, *model)
	}
	defer backend.Close() //nolint:errcheck

	orch, err := orchestrator.New(orchestrator.Config{
		ResourceType: *resource,
		WorkDir:      *workDir,
		Backend:      backend,
		MaxTasks:     *maxTasks,
		SkipNewres:   *skipNewres,
		SkipPlanning: *skipPlanning,
		SkipTests:    *skipTests,
		AccTestDir:   *accTestDir,
	})
	if err != nil {
		fatal(err)
	}

	ctx, cancel := signalContext()
	defer cancel()

	updates := orch.Subscribe()

	// The orchestrator runs in its own goroutine; main blocks on the TUI.
	pipelineErr := make(chan error, 1)
	go func() { pipelineErr <- orch.Run(ctx) }()

	if *noTUI {
		runHeadless(updates, pipelineErr)
		return
	}

	prog := tea.NewProgram(tui.New(updates), tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fatal(err)
	}
	// Wait for orchestrator to finish (or cancel it if the user quit early).
	cancel()
	if err := <-pipelineErr; err != nil {
		fatal(err)
	}
}

func runHeadless(updates <-chan orchestrator.Update, done <-chan error) {
	for u := range updates {
		fmt.Printf("[stage=%s active=%s] %d jobs\n", u.Stage, u.Active, len(u.Snapshot))
		if u.Done {
			break
		}
	}
	if err := <-done; err != nil {
		fatal(err)
	}
}



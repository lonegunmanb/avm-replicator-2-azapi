# avm2azapi — Go orchestrator + Bubble Tea TUI

`cmd/avm2azapi` replaces the previous PowerShell pipeline
(`replicator/migrate.ps1` + `replicator/run-coordinator.ps1`) and the
in-prompt `copilot -p ...` fan-out pattern that the Coordinator agent used to
spawn Executor / Checker sub-agents.

## What changed

| Before | After |
| --- | --- |
| `migrate.ps1` shells out to `copilot -p "..." --allow-all-tools` for the planner stage. | `internal/orchestrator` opens a session via `github.com/github/copilot-sdk/go`. |
| `run-coordinator.ps1` loops through `track.md` and spawns `copilot -p "...coordinator role..."` per task. The Coordinator session itself further shells out to `copilot -p "...Executor..."` and `copilot -p "...Checker..."`. | The Go orchestrator iterates `track.md` and opens one Copilot **session per role per task** directly. The agent prompts in `coordinator.md` / `executor.md` no longer instruct the model to invoke the CLI. |
| Progress was a stream of PowerShell `Write-Host` lines. | A Bubble Tea TUI shows: pipeline stage, ✔/▶/…/✘ glyphs per task, and the live output of any selected job. |

## Architecture

```
cmd/avm2azapi/main.go            entry point + flag parsing
internal/orchestrator/           pipeline state machine (planning → tasks → tests)
internal/runner/                 pluggable Backend that runs ONE Copilot session
   ├─ runner.go (SDKBackend)     real impl using github.com/github/copilot-sdk/go
   └─ dryrun.go (DryRunBackend)  in-process simulator (used by -dry-run)
internal/track/                  parser for replicator/track.md
internal/prompts/                prompt templates (mirror the old copilot CLI strings)
internal/tui/                    bubbletea Model: task list (left) + output viewer (right)
```

The orchestrator publishes typed `Update` snapshots over a channel; the TUI
re-subscribes on every message it receives. Each task produces two jobs in the
TUI — `task-N-executor` and `task-N-checker` — both selectable for live output.

## Usage

Build:

```bash
go build ./cmd/avm2azapi
```

Real run (requires the GitHub Copilot CLI installed and authenticated):

```bash
./avm2azapi -resource azurerm_orchestrated_virtual_machine_scale_set \
            -dir       ./replicator \
            -model     claude-sonnet-4.5
```

Demo / smoke-test without any external dependencies (uses the in-process
simulator backend):

```bash
./avm2azapi -resource azurerm_demo \
            -dir       ./replicator \
            -dry-run \
            -skip-planning \
            -skip-tests \
            -max-tasks 3
```

### TUI keys

| Key | Action |
| --- | --- |
| `↑` `↓` / `j` `k` | Move selection in the task list |
| `pgup` / `pgdn`   | Scroll the output pane |
| `g` / `G`         | Jump to top / bottom of output |
| `f`               | Toggle "follow" mode (auto-scroll & auto-select active job) |
| `q` / `Ctrl-C`    | Quit |

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `-resource`        | _required_ unless `-skip-planning` | AzureRM resource type to migrate |
| `-dir`             | `.`                | working directory containing `track.md` etc. |
| `-dry-run`         | `false`            | use the in-process simulator instead of the real Copilot SDK |
| `-max-tasks`       | `0` (no limit)     | cap how many `track.md` rows to process |
| `-skip-planning`   | `false`            | skip the planner stage (use when `track.md` already exists) |
| `-skip-tests`      | `false`            | skip the acceptance-test prep stage |
| `-model`           | `claude-sonnet-4.5`| Copilot model name |
| `-no-tui`          | `false`            | stream stage updates to stdout instead of starting the TUI |

## Stages

The orchestrator implements three Copilot-driven stages (mirroring the
former `migrate.ps1`):

1. **planning** — one session that runs `plan.md` and produces `track.md`.
2. **tasks** — for every actionable row in `track.md`, run an Executor
   session followed by a Checker session. The prompt template is selected
   automatically based on the task's `Type` and path:
   * root-level `Argument` → `prompts.ExecutorArgument`
   * root-level `Block`    → `prompts.ExecutorBlockSkeleton`
   * nested path           → `prompts.ExecutorBlockArgument`
   * `__check_*_hidden_fields__` → `prompts.ExecutorHiddenFields`
3. **prepare_tests** — one session that drafts acceptance tests using
   `test_cases_planner.md`.

External shell steps from the old script (`newres`, `test_extractor.ps1`,
`run-all-acctests.ps1`) are intentionally **not** orchestrated here; they
remain plain shell scripts you can run before / after the Go orchestrator.

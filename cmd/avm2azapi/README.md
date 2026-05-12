# avm2azapi — Go orchestrator + Bubble Tea TUI

`cmd/avm2azapi` is the single entrypoint for the AVM AzureRM-to-AzAPI
migration workflow. It replaces the old PowerShell pipeline
(`migrate.ps1`, `run-coordinator.ps1`, `test_extractor.ps1`,
`deduplicate_tests.ps1`, `run-all-acctests.ps1`, `cleanup-test-rgs.ps1`)
and the in-prompt `copilot -p ...` fan-out pattern.

The binary is **self-contained**: every prompt `.md` file is embedded via
`go:embed` and written into the working directory on every run, so users no
longer need to copy `replicator/` manually.

## What changed

| Before | After |
| --- | --- |
| `migrate.ps1` shells out to `copilot -p "..." --allow-all-tools` and chains five sibling `.ps1` scripts. | One Go binary owns the full pipeline; every Copilot session is opened via `github.com/github/copilot-sdk/go`. |
| `run-coordinator.ps1` loops through `track.md` and spawns `copilot -p "...coordinator role..."` per task; the Coordinator session itself shells out to `copilot -p "...Executor..."` and `copilot -p "...Checker..."`. | The Go orchestrator iterates `track.md` and opens one Copilot **session per role per task** directly. Agent prompts no longer instruct the model to invoke the CLI. |
| `replicator/*.md` had to be copied next to your module before each run. | All prompts are embedded; `avm2azapi` writes them into `-dir` automatically and removes any stale `replicator/` subdirectory. |
| Progress was a stream of PowerShell `Write-Host` lines. | A Bubble Tea TUI shows: resource type, pipeline stage, ✔/▶/…/✘ glyphs per task, live output, and a phase-aware progress bar. |

## Architecture

```
cmd/avm2azapi/
   main.go               entry point + flag parsing + subcommand dispatch
   helpers.go            shared utilities (prepareWorkDir, signalContext, runJobPrint)
   extract_tests.go      `extract-tests` subcommand
   dedup_tests.go        `dedup-tests`   subcommand
   run_tests.go          `run-tests`     subcommand
   cleanup_rgs.go        `cleanup-rgs`   subcommand

internal/orchestrator/   pipeline state machine + checkpoint persistence
   orchestrator.go       Run(), task stage, planning stage, prepare-tests stage
   stages_extra.go       newres / validate / delete_main / extract / dedup / run / final_check

internal/runner/         pluggable Backend that runs ONE Copilot session
   runner.go (SDKBackend) real impl using github.com/github/copilot-sdk/go
   dryrun.go (DryRunBackend) in-process simulator (used by -dry-run)

internal/track/          parser for `track.md`
internal/testcases/      parser, status updater, MD5 dedup for `test_cases.md`
internal/prompts/        prompt templates (mirror the old copilot CLI strings)
internal/tui/            bubbletea Model: header + task list + output viewer + progress bar
replicator/              embedded .md prompt files (loaded via go:embed)
```

The orchestrator publishes typed `Update` snapshots over a channel; the TUI
re-subscribes on every message it receives. Each task produces two jobs in
the TUI — `task-N-executor` and `task-N-checker` — both selectable for live
output. Non-Copilot work (e.g. `newres`, `dedup_tests`) appears as its own
job view too.

## Usage

Build:

```bash
go install github.com/lonegunmanb/avm2azapi/cmd/avm2azapi@latest
```

Real run (requires the GitHub Copilot CLI installed and authenticated):

```bash
avm2azapi -resource azurerm_orchestrated_virtual_machine_scale_set \
          -dir       ./mymodule \
          -model     claude-sonnet-4.5
```

Demo / smoke-test without any external dependencies (uses the in-process
simulator backend):

```bash
avm2azapi -resource azurerm_demo \
          -dir       ./mymodule \
          -dry-run \
          -skip-newres \
          -skip-planning \
          -skip-tests \
          -max-tasks 3
```

Subcommands (re-run a single phase without touching the checkpoint):

```bash
avm2azapi extract-tests -dir ./mymodule
avm2azapi dedup-tests   -dir ./mymodule
avm2azapi run-tests     -dir ./mymodule
avm2azapi cleanup-rgs   -dry-run
```

### TUI layout

```
┌ avm2azapi  —  migrating  azurerm_virtual_network ─────────────────────────┐
│ stage=tasks   12/120 done   1 running   0 failed   tok=…                  │
├─────────── tasks ─────────┬──────── output ──────────────────────────────┤
│ ✔ planning                │ # Executor — Task #13 (zones)                │
│ ✔ task-1-executor         │ state: running   stage=executor   elapsed=42 │
│ ▶ task-13-executor        │ [tool] read track.md                         │
│ … task-13-checker         │ [assistant] Investigating provider source…    │
└───────────────────────────┴──────────────────────────────────────────────┘
 convert [████████░░░░░░░░░░░░░░░░░░]   13/47   (28%)
 ↑/↓ select  pgup/pgdn scroll  g/G top/bottom  f follow=on/stick=on  q quit
```

Top line shows the resource being migrated. The progress bar at the bottom
switches between `convert` (driven by `track.md`) during the conversion
phase and `tests` (driven by `test_cases.md`) during acceptance tests.

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
| `-dir`             | `.`                | working directory (prompts will be extracted here) |
| `-dry-run`         | `false`            | use the in-process simulator instead of the real Copilot SDK |
| `-max-tasks`       | `0` (no limit)     | cap how many `track.md` rows to process |
| `-skip-newres`     | `false`            | skip the `newres` scaffolding stage |
| `-skip-planning`   | `false`            | skip the planner stage (use when `track.md` already exists) |
| `-skip-tests`      | `false`            | skip ALL test stages (prepare/extract/dedup/run/final_check) |
| `-acc-test-dir`    | `azurermacctest`   | directory holding generated acceptance tests |
| `-model`           | `claude-sonnet-4.5`| Copilot model name |
| `-no-tui`          | `false`            | stream stage updates to stdout instead of starting the TUI |

## Stages

The orchestrator implements the same checkpointed pipeline that the legacy
`migrate.ps1` did. Stage completion is persisted to
`<dir>/.migrate-checkpoint.json` and re-runs resume from where the previous
run failed. The file is auto-deleted on a clean end-to-end run.

1. **newres**         — calls the external `newres` CLI to scaffold initial AzAPI files.
2. **planning**       — one Copilot session that runs `plan.md` and produces `track.md`.
3. **tasks**          — for every actionable row in `track.md`, run an Executor session followed by a Checker session. The prompt template is selected automatically based on the task's `Type` and path:
   - root-level `Argument` → `prompts.ExecutorArgument`
   - root-level `Block`    → `prompts.ExecutorBlockSkeleton`
   - nested path           → `prompts.ExecutorBlockArgument`
   - `__check_*_hidden_fields__` → `prompts.ExecutorHiddenFields`
4. **validate**       — fail the run if any row in `track.md` is not `✅ Completed` (skipped when `-max-tasks` is set).
5. **delete_main**    — remove the original `main.tf`.
6. **prepare_tests**  — one Copilot session that drafts `test_cases.md` using `test_cases_planner.md`.
7. **extract_tests**  — one Copilot session per Pending row of `test_cases.md` (uses `expand_acc_test.md`).
8. **dedup_tests**    — in-process MD5 dedup of `azurerm.tf + main.tf` per generated case; duplicates are marked `Skipped`.
9. **run_tests**      — one Copilot session per eligible row of `test_cases.md` (uses `terraform-test.md`).
10. **final_check**   — surface `warning.md` if it exists.

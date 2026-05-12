# AVM Replicator - AzureRM to AzAPI Migration Tool

An AI-powered automation toolkit that helps Terraform users migrate their AzureRM provider resources to the [AzAPI provider](https://registry.terraform.io/providers/Azure/azapi/latest). This tool leverages GitHub Copilot CLI with Claude to analyze, transform, and validate resource migrations.

## Overview

When migrating from `azurerm_*` resources to `azapi_resource`, users lose all the built-in provider logic—validations, defaults, type coercions, and automatic transformations. This toolkit creates a **Replicator Module** that preserves all that behavior, ensuring seamless migration.

### What This Tool Does

1. **Analyzes** the AzureRM provider's Go source code to understand the resource schema
2. **Generates** a task list (`track.md`) for systematic conversion
3. **Creates** a Replicator Module (`migrate_*.tf` files) that replicates all provider behavior
4. **Validates** the implementation through acceptance tests extracted from the original provider
5. **Produces** proof documents for each conversion decision

## Prerequisites

- **Go 1.24+** (only required to build the binary; release binaries are self-contained)
- **GitHub Copilot CLI** (`copilot`) installed and authenticated, with access to Claude Sonnet 4.5
- **newres** — a tool for generating initial AzAPI resource scaffolding (skip with `-skip-newres` if not installed)
- **Terraform** installed and configured
- **Azure CLI** (`az`) authenticated, for acceptance tests and resource-group cleanup
- **Azure subscription** for running acceptance tests
- An **AVM (Azure Verified Modules) template** as the target module

> The Go binary embeds every `.md` prompt file. You no longer need to copy
> `replicator/` manually — on every run `avm2azapi` writes the embedded
> prompts into the working directory (replacing any stale `replicator/`
> subdirectory if present).

### Required MCP Tools

You must set such mcp config for GitHub Copilot CLI:

```json
"terraform-mcp-eva": {
      "type": "local",
      "command": "docker",
      "tools": [
        "*"
      ],
      "args": [
        "run",
        "-i",
        "--rm",
        "-e",
        "TRANSPORT_MODE=stdio",
        "-e",
        "GITHUB_TOKEN=<your_pat>",
        "--pull=always",
        "ghcr.io/lonegunmanb/terraform-mcp-eva"
      ]
    },
    "Azure_MCP_Server": {
        "command": "docker",
         "args": [
            "run",
            "-i",
            "--rm",
            "-v",
            "<your_az_cli_credential_path>:/root/.azure",
            "ghcr.io/lonegunmanb/azure-mcp:latest"
        ],
        "tools": [
          "extension_cli_generate"
        ]
    }
```

For Windows users, please read this [note](https://github.com/lonegunmanb/azure-mcp-docker?tab=readme-ov-file#for-windows-users) to setup your az cli credential path.

## Quick Start

### Step 1: Install the binary

```bash
go install github.com/lonegunmanb/avm2azapi/cmd/avm2azapi@latest
```

### Step 2: Prepare your target AVM module

Start with a clean AVM module template. Remove all default resource code
blocks, keeping only `main.telemetry.tf`.

### Step 3: Run the migration

Point `avm2azapi` at the module directory and the AzureRM resource type:

```bash
avm2azapi -resource azurerm_virtual_network -dir /path/to/your-avm-module
```

A Bubble Tea TUI opens with:

- top line: the resource being migrated
- status line: current stage, job counts, token usage
- left pane: every job (Executor / Checker / local steps), with live state
- right pane: streamed output of the selected job
- bottom progress bar: `convert N/total` during the conversion phase, then
  `tests N/total` during acceptance tests

Useful flags:

| Flag | Default | Purpose |
| --- | --- | --- |
| `-resource`        | required | AzureRM resource type to migrate |
| `-dir`             | `.`      | working directory |
| `-model`           | `claude-sonnet-4.5` | Copilot model |
| `-max-tasks`       | `0`      | cap how many `track.md` rows to process (0 = no cap) |
| `-skip-newres`     | `false`  | skip the initial `newres` scaffolding stage |
| `-skip-planning`   | `false`  | skip the planner stage (track.md already exists) |
| `-skip-tests`      | `false`  | skip ALL test stages |
| `-acc-test-dir`    | `azurermacctest` | directory for generated acceptance tests |
| `-no-tui`          | `false`  | stream events to stdout instead of starting the TUI |
| `-dry-run`         | `false`  | use the in-process simulator instead of the real Copilot SDK |

## Migration Pipeline Stages

`avm2azapi` orchestrates the same checkpointed pipeline that the legacy
`migrate.ps1` used to run. Each stage's completion is recorded in
`.migrate-checkpoint.json` so a re-run resumes from where the previous run
failed.

| Stage | Description |
|-------|-------------|
| **1. newres**         | Scaffolds initial AzAPI files via the external `newres` CLI |
| **2. planning**       | Copilot session reads `plan.md`; produces `track.md` |
| **3. tasks**          | For every actionable row in `track.md`: Executor session, then Checker session |
| **4. validate**       | Asserts every row in `track.md` is `✅ Completed` (skipped when `-max-tasks` capped processing) |
| **5. delete_main**    | Removes the original `main.tf` (AzureRM resource definition) |
| **6. prepare_tests**  | Copilot session reads `test_cases_planner.md`; produces `test_cases.md` |
| **7. extract_tests**  | One Copilot session per Pending row of `test_cases.md` (uses `expand_acc_test.md`) |
| **8. dedup_tests**    | In-process MD5 dedup of `azurerm.tf + main.tf` per generated case |
| **9. run_tests**      | One Copilot session per eligible row of `test_cases.md` (uses `terraform-test.md`) |
| **10. final_check**   | Surfaces `warning.md` if it exists |

### Subcommands

The individual test stages are also exposed as subcommands so you can re-run
just one slice of the pipeline without touching the checkpoint:

```bash
avm2azapi extract-tests -dir /path/to/module
avm2azapi dedup-tests   -dir /path/to/module
avm2azapi run-tests     -dir /path/to/module
avm2azapi cleanup-rgs   -dry-run         # delete acctestRG-* older than 4 days
```

### Checkpoint & Resume

Progress is stored in `.migrate-checkpoint.json` inside `-dir`. To resume
after a failure, just re-run the same `avm2azapi` command. To start over,
delete the file (and the generated artefacts you want gone) before re-running.

### What Could Be Wrong

Even when `avm2azapi` reports success, some issues may go undetected. Always
verify the following:

#### Validation only checks status, not correctness

The `validate` stage just confirms every row in `track.md` is `✅ Completed`.
Completion does not guarantee the implementation is correct.

**After migration ends, always verify:**

1. Confirm `.migrate-checkpoint.json` shows every stage true (or that the
   file was removed — it is auto-deleted on a clean end-to-end run).
2. Review `track.md` to ensure all tasks show `✅ Completed`.
3. Spot-check proof documents in `proof/` for critical fields.

#### Acceptance tests may fail

After `run_tests`, check the `test status` column in `test_cases.md`:

1. **Check `test_cases.md`**: review the `test status` column for each case
   - ✅ **Success** or **Skipped** — acceptable
   - ❌ **Failed** — requires investigation

2. **For failed tests**: check the error log in the test folder:

   ```bash
   cat tests/<test_case_name>/err.log
   ```

3. **Fix all failures**: it is the user's responsibility to fix all failed
   tests before considering the migration complete. Common issues include:
   - Missing field mappings in `migrate_main.tf`
   - Incorrect validation rules in `migrate_variables.tf`
   - Missing default values
   - Azure API schema differences

**Understanding the Acceptance Test Process:**

Each acceptance test validates both migration and green-field scenarios:

1. **Apply original AzureRM config** - Create resources using the original AzureRM provider configuration
2. **Dump resource state (before)** - Use Azure CLI to export the remote resource's current state to a local JSON file
3. **Migrate to AzAPI resource** - Switch from AzureRM to the generated AzAPI module
4. **Verify acceptable changes** - Run `terraform plan` to ensure all proposed changes are acceptable (no destructive changes)
5. **Apply the migration** - Apply the AzAPI configuration
6. **Dump resource state (after)** - Use Azure CLI to export the resource state again
7. **Compare states** - Verify nothing has changed between the before/after states
8. **Destroy and re-apply** - Destroy the resources, then apply again to verify the green-field scenario works correctly

#### Final-check limitations

1. **Check for `warning.md`**: if this file exists, some tasks may not be
   properly implemented. Review its contents carefully.

2. **Timeouts are not fully verified**: the final check cannot verify whether
   `timeouts` have been implemented correctly. To verify manually:
   - Open `migrate_variables.tf`
   - Find `variable "timeouts"`
   - Verify the `default` block has correct values (e.g., `create = "30m"`, `delete = "30m"`)
   - If the defaults are set correctly, you can **ignore** any `timeouts` warnings in `warning.md`

3. **For other warnings in `warning.md`**: consult GitHub Copilot to investigate:

   ```bash
   copilot -p "Read warning.md and the migrate_*.tf files. Verify whether the listed tasks have been implemented correctly. If not, identify what could be wrong." --allow-all-tools --model claude-sonnet-4.5
   ```

## Generated Files

After migration, your module will contain:

| File | Purpose |
|------|---------|
| `migrate_main.tf` | Main resource definition with `azapi_resource` and locals |
| `migrate_variables.tf` | Input variables with validations and defaults |
| `migrate_outputs.tf` | Module outputs |
| `migrate_validation.tf` | Additional validation rules |
| `track.md` | Task tracking with proof document links |
| `proof/*.md` | Individual proof documents for each field migration |
| `test_cases.md` | List of acceptance test cases |
| `tests/*.tftest.hcl` | Extracted acceptance tests |

## File Reference

### Binary entrypoints

| Command | Purpose |
|---------|---------|
| `avm2azapi -resource <type> -dir <path>` | Run the full migration pipeline (stages 1–10 above) |
| `avm2azapi extract-tests -dir <path>`    | Re-run only the `extract_tests` stage |
| `avm2azapi dedup-tests   -dir <path>`    | Re-run only the `dedup_tests` stage |
| `avm2azapi run-tests     -dir <path>`    | Re-run only the `run_tests` stage |
| `avm2azapi cleanup-rgs`                  | Delete `acctestRG-*` resource groups older than 4 days |

### AI Agent Prompts (embedded in the binary, written to `-dir` on every run)

| File | Agent Role |
|------|------------|
| `plan.md`              | **Planner Agent** — analyzes resource schema, creates `track.md` |
| `coordinator.md`       | **Coordinator Agent** — historical reference (Go orchestrator now does this) |
| `executor.md`          | **Executor Agent** — implements individual field migrations |
| `checker.md`           | **Checker Agent** — validates completed implementations |
| `test_cases_planner.md`| **Test Planner** — identifies test cases to extract |
| `test_extractor.md`    | **Test Extractor** — extracts test configurations |
| `expand_acc_test.md`   | **Test Expander** — expands test templates |
| `terraform-test.md`    | **Tester Agent** — runs an individual acceptance test |

### Override Documents

| File | Purpose |
|------|---------|
| `diffsuppressfunc.md`         | Rules for handling `DiffSuppressFunc` fields |
| `timeouts.md`                 | Rules for handling timeout blocks |
| `common_terraform_error.md`   | Common error patterns and solutions |
| `acceptable_drift_patterns.md`| Known acceptable drift patterns in tests |

## Example Workflow

```bash
# 1. Install the orchestrator (one-off).
go install github.com/lonegunmanb/avm2azapi/cmd/avm2azapi@latest

# 2. Create / clean a target AVM module directory (keep main.telemetry.tf).
mkdir my-azapi-vnet-module && cd my-azapi-vnet-module

# 3. Run the full pipeline.
avm2azapi -resource azurerm_virtual_network -dir .

# avm2azapi will:
#   - extract every embedded .md prompt into the working directory
#   - generate track.md (typically 50–200 tasks)
#   - run Executor + Checker per task
#   - generate migrate_*.tf, proof/*.md
#   - draft test_cases.md, extract & dedup tests, then run them
```

## Troubleshooting

### Resume After Failure

If the migration fails mid-way:

```bash
# Just re-run — it will resume from the last checkpoint stage.
avm2azapi -resource azurerm_virtual_network -dir .
```

### Reset and Start Over

```bash
# Remove checkpoint to start fresh.
rm .migrate-checkpoint.json

# Or remove all generated files.
rm -f migrate_*.tf track.md test_cases.md
rm -rf proof/ tests/ azurermacctest/

avm2azapi -resource azurerm_virtual_network -dir .
```

### Check Task Status

Review `track.md` to see which tasks are completed, pending, or failed:

```markdown
| No. | Path | Type | Required | Status | Proof Doc |
|-----|------|------|----------|--------|-----------|
| 1   | name | Argument | Yes | ✅ Completed | [proof](proof/001-name.md) |
| 2   | location | Argument | Yes | Pending | |
```

### Check Acc Tests Status

## Valid Test Cases

Review `test_cases.md` to see test cases table:

| case name | file url | status | test status |
| ---       | ---      | ---    | ---         |
| basic | https://raw.githubusercontent.com/hashicorp/terraform-provider-azurerm/refs/heads/main/internal/services/network/subnet_resource_test.go | Completed | test success |
| basic_addressPrefixes | https://raw.githubusercontent.com/hashicorp/terraform-provider-azurerm/refs/heads/main/internal/services/network/subnet_resource_test.go | Completed | test success |

`status` column stands for "has the acc test folder been extracted or not", and `test status` column stands for "has this test been executed`.

## After All

Once the migration completes successfully and all tests pass, follow these final steps to prepare your module for production use:

### 1. Review Proof Documents

We strongly recommend reviewing all proof documents and their corresponding implementations one by one:

```bash
# List all proof documents
ls proof/

# For each proof document, verify:
# - The reasoning is correct
# - The implementation in migrate_*.tf matches the proof
# - Edge cases are properly handled
```

### 2. Create Usage Examples

Pick 1-2 successful acceptance tests as examples to teach users how to use this module:

1. Review `test_cases.md` for simple, representative test cases (e.g., `basic`, `complete`)
2. Copy the test configuration as a starting point
3. Create an `examples/` folder with clear documentation:
   ```
   examples/
   ├── basic/
   │   ├── main.tf
   │   ├── variables.tf
   │   └── README.md
   └── complete/
       ├── main.tf
       ├── variables.tf
       └── README.md
   ```

### 3. Rename Migration Files

Remove the `migrate_` prefix from all generated files to follow standard Terraform conventions:

```bash
mv migrate_main.tf main.tf
mv migrate_variables.tf variables.tf
mv migrate_outputs.tf outputs.tf
mv migrate_validation.tf validation.tf
```

### 4. Sort Variables and Outputs

Organize your variable and output blocks alphabetically for better maintainability:

```bash
# Use terraform-docs or manually sort:
# - All variables in variables.tf should be alphabetically ordered
# - All outputs in outputs.tf should be alphabetically ordered
# - Consider grouping required variables before optional ones
```

You can use tools like `terraform fmt` for formatting and consider using `terraform-docs` for documentation generation.

## Contributing

Contributions are welcome! Please ensure any changes to the prompt files maintain consistency with the overall migration workflow.

## License

MIT License - see [LICENSE](LICENSE) for details.

---

**Note:** This tool requires an active GitHub Copilot subscription with access to the Copilot CLI and Claude models. The migration process may consume significant API tokens depending on resource complexity.

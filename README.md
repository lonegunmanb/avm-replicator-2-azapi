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

- **PowerShell Core** (pwsh) installed
- **GitHub Copilot CLI** (`copilot`) with access to Claude Sonnet 4.5
- **newres** - A tool for generating initial AzAPI resource scaffolding
- **Terraform** installed and configured
- **Azure subscription** for running acceptance tests
- An **AVM (Azure Verified Modules) template** as the target module

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

### Step 1: Prepare Your Target AVM Module

Start with a clean AVM module template. Remove all default resource code blocks, keeping only `main.telemetry.tf`.

### Step 2: Copy Replicator Files

Copy all files from the `replicator/` folder into your AVM module's root directory:

```bash
cp -r /path/to/avm-replicator-2-azapi/replicator/* /path/to/your-avm-module/
```

### Step 3: Run the Migration

Execute the migration script with your target AzureRM resource type:

```bash
./migrate.ps1 -ResourceType "azurerm_virtual_network"
```

Or simply:

```bash
./migrate.ps1 azurerm_orchestrated_virtual_machine_scale_set
```

## Migration Pipeline Stages

The `migrate.ps1` script orchestrates the following checkpointed stages:

| Stage | Description |
|-------|-------------|
| **1. newres** | Generates initial AzAPI resource scaffolding using the `newres` tool |
| **2. planning** | AI agent reads `plan.md` to analyze the resource schema and create `track.md` |
| **3. coordinator** | Runs `run-coordinator.ps1` to delegate tasks to executor agents |
| **4. validation** | Checks that all tasks in `track.md` are marked ✅ Completed |
| **5. final-check** | Runs `final-check.ps1` for implementation verification |
| **6. delete_main** | Removes the original `main.tf` (AzureRM resource definitions) |
| **7. prepare_tests** | AI agent reads `test_cases_planner.md` to plan acceptance tests |
| **8. extract_tests** | Runs `test_extractor.ps1` to extract test configurations |
| **9. deduplicate_tests** | Runs `deduplicate_tests.ps1` to remove duplicate tests |
| **10. run_tests** | Executes `run-all-acctests.ps1` for acceptance testing |
| **11. final_check** | Final verification + warns if `warning.md` exists |

### Checkpoint & Resume

The script maintains progress in `.migrate-checkpoint.json`. If the process fails at any stage, simply re-run `./migrate.ps1` and it will resume from where it left off.

### What Could Be Wrong

Even when `migrate.ps1` completes without errors, some issues may go undetected. Always verify the following:

#### Stage 4 Validation May Pass Incorrectly

The validation stage checks if all tasks in `track.md` are marked ✅ Completed, but completion status doesn't guarantee correctness.

**After migration ends, always verify:**

1. Check `.migrate-checkpoint.json` to confirm all stages completed
2. Review `track.md` to ensure all tasks show ✅ Completed status
3. Spot-check proof documents in `proof/` folder for critical fields

#### Stage 10 Acceptance Tests May Fail

After running acceptance tests, you **must** verify the results in `test_cases.md`:

1. **Check `test_cases.md`**: Review the `test status` column for each test case
   - ✅ **Success** or **Skipped** - These are acceptable
   - ❌ **Failed** - These require investigation and fixing

2. **For failed tests**: Check the error log in the test folder:
   ```bash
   # Error logs are saved as err.log in each test case folder
   cat tests/<test_case_name>/err.log
   ```

3. **Fix all failures**: It is the user's responsibility to fix all failed tests before considering the migration complete. Common issues include:
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

#### Stage 11 Final Check Limitations

The final verification has known limitations:

1. **Check for `warning.md`**: If this file exists, some tasks may not be properly implemented. Review its contents carefully.

2. **Timeouts are not fully verified**: The final check cannot verify whether `timeouts` have been implemented correctly. To verify manually:
   - Open `migrate_variables.tf`
   - Find `variable "timeouts"`
   - Verify the `default` block has correct values (e.g., `create = "30m"`, `delete = "30m"`)
   - If the defaults are set correctly, you can **ignore** any `timeouts` warnings in `warning.md`

3. **For other warnings in `warning.md`**: Consult GitHub Copilot to investigate:
   ```bash
   copilot -p "Read migrate.ps1 and warning.md to understand the context. Then verify whether the tasks listed in warning.md have been implemented correctly in the migrate_*.tf files. If not, identify what could be wrong." --allow-all-tools --model claude-sonnet-4.5
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

### Core Scripts

| File | Purpose |
|------|---------|
| `migrate.ps1` | Main entrypoint - orchestrates the entire migration |
| `run-coordinator.ps1` | Runs the coordinator agent for task delegation |
| `test_extractor.ps1` | Extracts test configurations from provider tests |
| `deduplicate_tests.ps1` | Removes duplicate test cases |
| `run-all-acctests.ps1` | Executes all acceptance tests |
| `cleanup-test-rgs.ps1` | Cleans up Azure resource groups from testing |

### AI Agent Prompts

| File | Agent Role |
|------|------------|
| `plan.md` | **Planner Agent** - Analyzes resource schema, creates `track.md` |
| `coordinator.md` | **Coordinator Agent** - Delegates tasks to executors |
| `executor.md` | **Executor Agent** - Implements individual field migrations |
| `checker.md` | **Checker Agent** - Validates completed implementations |
| `test_cases_planner.md` | **Test Planner** - Identifies test cases to extract |
| `test_extractor.md` | **Test Extractor** - Extracts test configurations |
| `expand_acc_test.md` | **Test Expander** - Expands test templates |

### Override Documents

| File | Purpose |
|------|---------|
| `diffsuppressfunc.md` | Rules for handling `DiffSuppressFunc` fields |
| `timeouts.md` | Rules for handling timeout blocks |
| `common_terraform_error.md` | Common error patterns and solutions |
| `acceptable_drift_patterns.md` | Known acceptable drift patterns in tests |
| `terraform-test.md` | Terraform test file guidelines |

## Example Workflow

```bash
# 1. Create a new AVM module directory
mkdir my-azapi-vnet-module
cd my-azapi-vnet-module

# 2. Initialize with minimal structure (keep main.telemetry.tf if from AVM template)
# 3. Copy replicator files
cp -r ~/avm-replicator-2-azapi/replicator/* .

# 4. Run migration for azurerm_virtual_network
./migrate.ps1 azurerm_virtual_network

# 5. Wait for completion - the script will:
#    - Generate track.md with ~50-200 tasks depending on resource complexity
#    - Create migrate_*.tf files with full azapi_resource implementation
#    - Run acceptance tests to verify correctness
#    - Generate proof documents for each field migration
```

## Troubleshooting

### Resume After Failure

If the migration fails mid-way:

```bash
# Simply re-run - it will resume from the last checkpoint
./migrate.ps1 azurerm_virtual_network
```

### Reset and Start Over

```bash
# Remove checkpoint to start fresh
rm .migrate-checkpoint.json

# Or remove all generated files
rm -f migrate_*.tf track.md test_cases.md
rm -rf proof/ tests/

./migrate.ps1 azurerm_virtual_network
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

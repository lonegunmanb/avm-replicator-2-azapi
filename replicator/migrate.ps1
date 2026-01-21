param(
    [Parameter(Mandatory = $true)]
    [string]$ResourceType
)

# Checkpoint file
$checkpointFile = ".migrate-checkpoint.json"

# Initialize or load checkpoint
if (Test-Path $checkpointFile) {
    $checkpoint = Get-Content $checkpointFile | ConvertFrom-Json
    Write-Host "Resuming from checkpoint..." -ForegroundColor Yellow
} else {
    $checkpoint = @{
        newres = $false
        planning = $false
        coordinator = $false
        delete_main = $false
        prepare_tests = $false
        extract_tests = $false
        deduplicate_tests = $false
        run_tests = $false
    }
}

function Save-Checkpoint {
    param([string]$step)
    $checkpoint.$step = $true
    $checkpoint | ConvertTo-Json | Set-Content $checkpointFile
    Write-Host "Checkpoint saved: $step completed" -ForegroundColor Green
}

# Execute newres command
if (-not $checkpoint.newres) {
    Write-Host "Executing newres for resource type: $ResourceType" -ForegroundColor Cyan
    newres -r $ResourceType -dir . -variable-prefix=""

    if ($LASTEXITCODE -ne 0) {
        Write-Error "newres command failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    Save-Checkpoint -step "newres"
} else {
    Write-Host "Skipping newres (already completed)" -ForegroundColor DarkGray
}

# Execute copilot command
if (-not $checkpoint.planning) {
    Write-Host "Executing copilot for migration planning..." -ForegroundColor Cyan
    copilot -p "Read plan.md and play as planner role, follow it's instruments and prepare migration of $ResourceType defined in main.tf" --allow-all-tools --model claude-sonnet-4.5

    if ($LASTEXITCODE -ne 0) {
        Write-Error "copilot command failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    Save-Checkpoint -step "planning"
} else {
    Write-Host "Skipping migration planning (already completed)" -ForegroundColor DarkGray
}

# Execute run-coordinator script
if (-not $checkpoint.coordinator) {
    Write-Host "Executing run-coordinator script..." -ForegroundColor Cyan
    ./run-coordinator.ps1 *

    if ($LASTEXITCODE -ne 0) {
        Write-Error "coordinator script failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    Save-Checkpoint -step "coordinator"
} else {
    Write-Host "Skipping coordinator (already completed)" -ForegroundColor DarkGray
}

# Validate that all tasks in track.md are completed
Write-Host "Validating track.md task completion status..." -ForegroundColor Cyan
$trackContent = Get-Content -Path "track.md" -Raw
$taskLines = $trackContent -split "`n" | Where-Object { $_ -match '^\|\s*\d+\s*\|.*\|.*\|.*\|.*\|.*\|$' }
$incompleteTasks = @()
foreach ($line in $taskLines) {
    if ($line -notmatch '\|\s*✅\s*Completed\s*\|') {
        $taskNumber = ($line -split '\|')[1].Trim()
        $taskPath = ($line -split '\|')[2].Trim()
        $incompleteTasks += "Task $taskNumber ($taskPath)"
    }
}
if ($incompleteTasks.Count -gt 0) {
    Write-Error "The following tasks in track.md are not completed:"
    $incompleteTasks | ForEach-Object { Write-Error "  - $_" }
    Write-Error "All tasks must be completed before proceeding with migration. Please complete the tasks and try again."
    exit 1
}
Write-Host "All tasks in track.md are completed ✓" -ForegroundColor Green

# Run final verification check
Write-Host "Running final implementation verification..." -ForegroundColor Cyan
& ".\final-check.ps1"
if ($LASTEXITCODE -ne 0) {
    Write-Error "Final verification check failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}
Write-Host "Final verification check completed" -ForegroundColor Green

# Delete main.tf file
if (-not $checkpoint.delete_main) {
    Write-Host "Deleting main.tf file..." -ForegroundColor Cyan
    Remove-Item -Path "main.tf" -Force -ErrorAction Stop
    Save-Checkpoint -step "delete_main"
} else {
    Write-Host "Skipping main.tf deletion (already completed)" -ForegroundColor DarkGray
}

# Prepare acc tests
if (-not $checkpoint.prepare_tests) {
    Write-Host "Prepare acc tests" -ForegroundColor Cyan
    copilot -p "Read test_cases_planner.md and follow it's instructions to prepare acceptance tests for $ResourceType." --allow-all-tools --model claude-sonnet-4.5

    if ($LASTEXITCODE -ne 0) {
        Write-Error "copilot command for acceptance tests failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    Save-Checkpoint -step "prepare_tests"
} else {
    Write-Host "Skipping test preparation (already completed)" -ForegroundColor DarkGray
}

# Extract acceptance tests
if (-not $checkpoint.extract_tests) {
    Write-Host "Extract acceptance tests" -ForegroundColor Cyan
    ./test_extractor.ps1

    if ($LASTEXITCODE -ne 0) {
        Write-Error "test_extractor.ps1 script failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    Save-Checkpoint -step "extract_tests"
} else {
    Write-Host "Skipping test extraction (already completed)" -ForegroundColor DarkGray
}

# Deduplicate acceptance tests
if (-not $checkpoint.deduplicate_tests) {
    Write-Host "Deduplicate acceptance tests" -ForegroundColor Cyan
    ./deduplicate_tests.ps1

    if ($LASTEXITCODE -ne 0) {
        Write-Error "deduplicate_tests.ps1 script failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    Save-Checkpoint -step "deduplicate_tests"
} else {
    Write-Host "Skipping test deduplication (already completed)" -ForegroundColor DarkGray
}

# Run acceptance tests
if (-not $checkpoint.run_tests) {
    ./run-all-acctests.ps1

    if ($LASTEXITCODE -ne 0) {
        Write-Error "Acceptance tests execution failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    Save-Checkpoint -step "run_tests"
} else {
    Write-Host "Skipping test execution (already completed)" -ForegroundColor DarkGray
}

# Run final verification check after acceptance tests
if (-not $checkpoint.final_check) {
    Write-Host "Running final implementation verification after acceptance tests..." -ForegroundColor Cyan
    & ".\final-check.ps1"
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Final verification check failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    
    # Check if warning.md exists and has warnings
    if (Test-Path "warning.md") {
        Write-Host ""
        Write-Host "⚠️  WARNING: Some tasks were not properly implemented!" -ForegroundColor Yellow
        Write-Host "⚠️  Please review warning.md for details" -ForegroundColor Yellow
        Write-Host ""
    }
    
    Save-Checkpoint -step "final_check"
} else {
    Write-Host "Skipping final verification check (already completed)" -ForegroundColor DarkGray
}

# Migration completed successfully
Write-Host "`nMigration completed successfully!" -ForegroundColor Green
Write-Host "Cleaning up checkpoint file..." -ForegroundColor Cyan
Remove-Item -Path $checkpointFile -Force -ErrorAction SilentlyContinue


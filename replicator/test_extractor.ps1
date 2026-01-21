# Test Extractor PowerShell Script
# This script automates the extraction and conversion of test cases

[CmdletBinding()]
param(
    [Parameter(Mandatory=$false)]
    [string]$TestCasesFile = "test_cases.md",
    
    [Parameter(Mandatory=$false)]
    [string]$Model = "claude-sonnet-4.5",
    
    [Parameter(Mandatory=$false)]
    [switch]$DryRun,
    
    [Parameter(Mandatory=$false)]
    [int]$MaxCases = 0  # 0 means process all
)

# Function to parse test cases from markdown file
function Get-PendingTestCases {
    param([string]$FilePath)
    
    if (-not (Test-Path $FilePath)) {
        Write-Error "Test cases file not found: $FilePath"
        return @()
    }
    
    $content = Get-Content $FilePath -Raw
    $testCases = @()
    
    # Parse markdown table lines
    $lines = $content -split "`n"
    $inTable = $false
    
    foreach ($line in $lines) {
        # Check if this is a table row with data
        if ($line -match '^\|\s*([^\|]+?)\s*\|\s*(https://[^\|]+?)\s*\|\s*(Pending|Completed|In Progress)\s*\|') {
            $caseName = $matches[1].Trim()
            $fileUrl = $matches[2].Trim()
            $status = $matches[3].Trim()
            
            # Skip header rows
            if ($caseName -notmatch '^(Case Name|---)') {
                $testCases += [PSCustomObject]@{
                    CaseName = $caseName
                    FileUrl = $fileUrl
                    Status = $status
                }
            }
        }
    }
    
    return $testCases
}

# Function to update test case status in markdown file
function Update-TestCaseStatus {
    param(
        [string]$FilePath,
        [string]$CaseName,
        [string]$NewStatus
    )
    
    $content = Get-Content $FilePath -Raw
    
    # Replace the status for the specific case
    $pattern = "(\|\s*$([regex]::Escape($CaseName))\s*\|[^\|]*\|\s*)(Pending|Completed|In Progress)(\s*\|)"
    $replacement = "`${1}$NewStatus`${3}"
    
    $newContent = $content -replace $pattern, $replacement
    
    if ($content -ne $newContent) {
        Set-Content -Path $FilePath -Value $newContent -NoNewline
        Write-Host "✓ Updated status for '$CaseName' to '$NewStatus'" -ForegroundColor Green
        return $true
    } else {
        Write-Warning "Could not update status for '$CaseName'"
        return $false
    }
}

# Function to invoke copilot for a test case
function Invoke-TestCaseExtraction {
    param(
        [string]$CaseName,
        [string]$Model
    )
    
    $prompt = "You are a Test Case Agent. Read 'expand_acc_test.md' and follow ALL instructions sequentially: First complete Part 1 (extract test case) then immediately complete Part 2 (convert to AzAPI module). Extract and convert test case method '$CaseName' from the provider test file. The method_name is: $CaseName"
    
    Write-Host "`n========================================" -ForegroundColor Cyan
    Write-Host "Processing: $CaseName" -ForegroundColor Cyan
    Write-Host "========================================`n" -ForegroundColor Cyan
    
    # Execute copilot command
    $copilotArgs = @(
        "-p", $prompt,
        "--allow-all-tools",
        "--model", $Model
    )
    
    Write-Host "Command: copilot $($copilotArgs -join ' ')`n" -ForegroundColor Gray
    
    try {
        & copilot @copilotArgs
        $exitCode = $LASTEXITCODE
        
        if ($exitCode -eq 0) {
            Write-Host "`n✓ Successfully processed: $CaseName" -ForegroundColor Green
            return $true
        } else {
            Write-Host "`n✗ Failed to process: $CaseName (Exit code: $exitCode)" -ForegroundColor Red
            return $false
        }
    } catch {
        Write-Host "`n✗ Error processing: $CaseName - $_" -ForegroundColor Red
        return $false
    }
}

# Main execution
Write-Host "==============================================`n" -ForegroundColor Yellow
Write-Host "Test Case Extractor Automation Script" -ForegroundColor Yellow
Write-Host "==============================================`n" -ForegroundColor Yellow

# Get all test cases
Write-Host "Reading test cases from: $TestCasesFile" -ForegroundColor White
$allCases = Get-PendingTestCases -FilePath $TestCasesFile

if ($allCases.Count -eq 0) {
    Write-Host "No test cases found in $TestCasesFile" -ForegroundColor Red
    exit 1
}

# Filter pending cases
$pendingCases = $allCases | Where-Object { $_.Status -eq "Pending" }

Write-Host "Total test cases: $($allCases.Count)" -ForegroundColor White
Write-Host "Pending test cases: $($pendingCases.Count)" -ForegroundColor Yellow
Write-Host "Completed test cases: $(($allCases | Where-Object { $_.Status -eq 'Completed' }).Count)`n" -ForegroundColor Green

if ($pendingCases.Count -eq 0) {
    Write-Host "All test cases are already completed!" -ForegroundColor Green
    exit 0
}

# Limit cases if MaxCases is specified
if ($MaxCases -gt 0 -and $pendingCases.Count -gt $MaxCases) {
    Write-Host "Limiting to first $MaxCases cases (use -MaxCases 0 to process all)`n" -ForegroundColor Yellow
    $pendingCases = $pendingCases | Select-Object -First $MaxCases
}

if ($DryRun) {
    Write-Host "DRY RUN MODE - No changes will be made`n" -ForegroundColor Yellow
    Write-Host "Would process the following cases:" -ForegroundColor White
    $pendingCases | ForEach-Object { Write-Host "  - $($_.CaseName)" }
    exit 0
}

# Process each pending case
$successCount = 0
$failCount = 0
$startTime = Get-Date

foreach ($case in $pendingCases) {
    $caseStartTime = Get-Date
    
    # Update status to In Progress
    Update-TestCaseStatus -FilePath $TestCasesFile -CaseName $case.CaseName -NewStatus "In Progress"
    
    # Process the test case
    $success = Invoke-TestCaseExtraction -CaseName $case.CaseName -Model $Model
    
    # Update final status
    if ($success) {
        Update-TestCaseStatus -FilePath $TestCasesFile -CaseName $case.CaseName -NewStatus "Completed"
        $successCount++
    } else {
        Update-TestCaseStatus -FilePath $TestCasesFile -CaseName $case.CaseName -NewStatus "Pending"
        $failCount++
    }
    
    $caseElapsed = (Get-Date) - $caseStartTime
    Write-Host "`nCase processing time: $($caseElapsed.ToString('mm\:ss'))`n" -ForegroundColor Gray
}

# Summary
$totalElapsed = (Get-Date) - $startTime
Write-Host "`n==============================================`n" -ForegroundColor Yellow
Write-Host "Extraction Summary" -ForegroundColor Yellow
Write-Host "==============================================`n" -ForegroundColor Yellow
Write-Host "Total processed: $($successCount + $failCount)" -ForegroundColor White
Write-Host "Successful: $successCount" -ForegroundColor Green
Write-Host "Failed: $failCount" -ForegroundColor Red
Write-Host "Total time: $($totalElapsed.ToString('hh\:mm\:ss'))`n" -ForegroundColor White

if ($failCount -gt 0) {
    Write-Host "Some test cases failed. Review the output above for details." -ForegroundColor Yellow
    exit 1
} else {
    Write-Host "All test cases processed successfully!" -ForegroundColor Green
    exit 0
}

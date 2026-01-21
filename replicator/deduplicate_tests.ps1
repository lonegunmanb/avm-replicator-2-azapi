# Test Deduplication Script
# This script identifies duplicate test cases by comparing MD5 hashes of their configurations

[CmdletBinding()]
param(
    [Parameter(Mandatory=$false)]
    [string]$AccTestDir = "azurermacctest",
    
    [Parameter(Mandatory=$false)]
    [string]$TestCasesFile = "test_cases.md",
    
    [Parameter(Mandatory=$false)]
    [switch]$DryRun
)

# Function to convert between snake_case and camelCase
function Convert-CaseVariants {
    param([string]$Name)
    
    $variants = @($Name)
    
    # If contains underscore, convert to camelCase
    if ($Name -match '_') {
        $parts = $Name -split '_'
        $camelCase = $parts[0].ToLower()
        for ($i = 1; $i -lt $parts.Length; $i++) {
            if ($parts[$i].Length -gt 0) {
                $camelCase += $parts[$i].Substring(0, 1).ToUpper() + $parts[$i].Substring(1).ToLower()
            }
        }
        $variants += $camelCase
    }
    # If camelCase, convert to snake_case
    elseif ($Name -cmatch '[a-z][A-Z]') {
        $snakeCase = $Name -creplace '([A-Z])', '_$1'
        $snakeCase = $snakeCase.ToLower().TrimStart('_')
        $variants += $snakeCase
    }
    
    return $variants
}

# Function to update test case status in markdown
function Update-TestCaseStatus {
    param(
        [string]$FilePath,
        [string]$CaseName,
        [string]$NewStatus
    )
    
    if (-not (Test-Path $FilePath)) {
        Write-Warning "Test cases file not found: $FilePath"
        return $false
    }
    
    $content = Get-Content $FilePath -Raw
    
    # Try all case variants
    $variants = Convert-CaseVariants -Name $CaseName
    $updated = $false
    
    foreach ($variant in $variants) {
        $escapedName = [regex]::Escape($variant)
        $pattern = "(\|\s*$escapedName\s*\|[^\|]*\|\s*)(Pending|Completed|In Progress|Skipped)(\s*\|)"
        
        if ($content -match $pattern) {
            $newContent = $content -replace $pattern, "`${1}$NewStatus`${3}"
            
            if ($content -ne $newContent) {
                if (-not $DryRun) {
                    Set-Content -Path $FilePath -Value $newContent -NoNewline
                }
                Write-Host "  ✓ Updated '$variant' status to '$NewStatus'" -ForegroundColor Yellow
                $updated = $true
                break
            }
        }
    }
    
    if (-not $updated) {
        Write-Warning "  Could not find test case '$CaseName' (tried variants: $($variants -join ', '))"
    }
    
    return $updated
}

# Function to compute MD5 hash
function Get-FileMD5 {
    param([string]$Content)
    
    $md5 = [System.Security.Cryptography.MD5]::Create()
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($Content)
    $hashBytes = $md5.ComputeHash($bytes)
    $hash = [System.BitConverter]::ToString($hashBytes) -replace '-', ''
    
    return $hash.ToLower()
}

# Main execution
Write-Host "`n========================================" -ForegroundColor Cyan
Write-Host "Test Deduplication Script" -ForegroundColor Cyan
Write-Host "========================================`n" -ForegroundColor Cyan

if ($DryRun) {
    Write-Host "DRY RUN MODE - No changes will be made`n" -ForegroundColor Yellow
}

# Check if test directory exists
if (-not (Test-Path $AccTestDir)) {
    Write-Error "Test directory not found: $AccTestDir"
    exit 1
}

# Track hashes in memory
$seenHashes = @{}

# Scan all test directories
$testDirs = Get-ChildItem -Path $AccTestDir -Directory | Sort-Object Name
Write-Host "Found $($testDirs.Count) test directories`n" -ForegroundColor White

$processedCount = 0
$skippedCount = 0
$newCount = 0
$errorCount = 0

foreach ($dir in $testDirs) {
    $caseName = $dir.Name
    $azurermFile = Join-Path $dir.FullName "azurerm.tf"
    $mainFile = Join-Path $dir.FullName "main.tf"
    
    Write-Host "Processing: $caseName" -ForegroundColor Cyan
    
    # Check if both files exist
    if (-not (Test-Path $azurermFile)) {
        Write-Warning "  Missing azurerm.tf, skipping"
        $errorCount++
        continue
    }
    
    if (-not (Test-Path $mainFile)) {
        Write-Warning "  Missing main.tf, skipping"
        $errorCount++
        continue
    }
    
    # Read and concatenate file contents
    try {
        $azurermContent = Get-Content $azurermFile -Raw -ErrorAction Stop
        $mainContent = Get-Content $mainFile -Raw -ErrorAction Stop
        $combinedContent = $azurermContent + $mainContent
        
        # Compute MD5 hash
        $md5Hash = Get-FileMD5 -Content $combinedContent
        
        Write-Host "  MD5: $md5Hash" -ForegroundColor Gray
        
        # Check if hash already exists
        if ($seenHashes.ContainsKey($md5Hash)) {
            $originalCase = $seenHashes[$md5Hash]
            Write-Host "  ⚠ Duplicate detected! Same as: $originalCase" -ForegroundColor Yellow
            
            # Update status to Skipped
            $updated = Update-TestCaseStatus -FilePath $TestCasesFile -CaseName $caseName -NewStatus "Skipped"
            
            if ($updated -or $DryRun) {
                $skippedCount++
            }
        } else {
            Write-Host "  ✓ Unique test case" -ForegroundColor Green
            $seenHashes[$md5Hash] = $caseName
            $newCount++
        }
        
        $processedCount++
    } catch {
        Write-Error "  Error processing files: $_"
        $errorCount++
    }
    
    Write-Host ""
}

# Summary
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Deduplication Summary" -ForegroundColor Cyan
Write-Host "========================================`n" -ForegroundColor Cyan
Write-Host "Total processed: $processedCount" -ForegroundColor White
Write-Host "Unique test cases: $newCount" -ForegroundColor Green
Write-Host "Duplicates skipped: $skippedCount" -ForegroundColor Yellow
Write-Host "Errors: $errorCount" -ForegroundColor Red
Write-Host ""

if ($DryRun) {
    Write-Host "DRY RUN - No changes were made. Run without -DryRun to apply changes." -ForegroundColor Yellow
} else {
    Write-Host "Deduplication complete!" -ForegroundColor Green
    Write-Host "Updated: $TestCasesFile" -ForegroundColor White
}

exit 0

#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Clean up test resource groups with activity logs older than 4 days
.DESCRIPTION
    This script identifies resource groups matching the pattern 'acctestRG-<number>',
    checks their activity logs, and deletes those with operations from 4+ days ago.
.PARAMETER DryRun
    If specified, only shows what would be deleted without actually deleting.
#>

param(
    [Parameter()]
    [switch]$DryRun
)

# Set error action preference
$ErrorActionPreference = "Continue"

# Calculate the date threshold (4 days ago)
$thresholdDate = (Get-Date).AddDays(-4)
$thresholdDateString = $thresholdDate.ToString("yyyy-MM-ddTHH:mm:ssZ")

Write-Host "=== Test Resource Group Cleanup ===" -ForegroundColor Cyan
Write-Host "Threshold date: $thresholdDate" -ForegroundColor Yellow
if ($DryRun) {
    Write-Host "Mode: DRY RUN (no actual deletions will be performed)" -ForegroundColor Magenta
}
Write-Host ""

# Get all resource groups
Write-Host "Fetching all resource groups..." -ForegroundColor Green
$allResourceGroups = az group list --query "[].name" -o tsv

if (-not $allResourceGroups) {
    Write-Host "No resource groups found or failed to fetch." -ForegroundColor Red
    exit 1
}

# Filter resource groups matching the pattern acctestRG-<number>
$pattern = '^acctestRG-\d+$'
$matchingRGs = $allResourceGroups | Where-Object { $_ -match $pattern }

if ($matchingRGs.Count -eq 0) {
    Write-Host "No resource groups matching pattern 'acctestRG-<number>' found." -ForegroundColor Yellow
    exit 0
}

Write-Host "Found $($matchingRGs.Count) matching resource groups." -ForegroundColor Green
Write-Host ""

# Process each matching resource group
$deletedCount = 0
$skippedCount = 0

foreach ($rgName in $matchingRGs) {
    Write-Host "Processing: $rgName" -ForegroundColor Cyan
    
    try {
        # Query resource group creation time using activity logs
        # Find the earliest 'write' operation which indicates creation time
        $logTimestamp = az monitor activity-log list `
            --resource-group $rgName `
            --offset 90d `
            --query "sort_by([?operationName.value=='Microsoft.Resources/subscriptions/resourceGroups/write'], &eventTimestamp)[0].eventTimestamp" `
            --output tsv 2>&1
        
        # Check if command failed
        if ($LASTEXITCODE -ne 0) {
            Write-Host "  ⚠ Failed to query activity logs. Skipping." -ForegroundColor Yellow
            $skippedCount++
            continue
        }
        
        # Check if we found the creation log
        if ([string]::IsNullOrWhiteSpace($logTimestamp)) {
            # No log found - created more than 90 days ago, definitely older than 4 days
            if ($DryRun) {
                Write-Host "  ✓ [DRY RUN] Would delete: $rgName (created >90 days ago)" -ForegroundColor Magenta
                $deletedCount++
            } else {
                Write-Host "  ✓ Created >90 days ago. Deleting..." -ForegroundColor Yellow
                
                # Delete the resource group without waiting
                az group delete --name $rgName --yes --no-wait
                
                if ($LASTEXITCODE -eq 0) {
                    Write-Host "  ✓ Deletion initiated for: $rgName" -ForegroundColor Green
                    $deletedCount++
                } else {
                    Write-Host "  ✗ Failed to delete: $rgName" -ForegroundColor Red
                }
            }
        } else {
            # Found creation timestamp
            $creationDate = [DateTime]$logTimestamp
            $daysSinceCreation = [Math]::Round(((Get-Date) - $creationDate).TotalDays, 1)
            
            if ($creationDate -lt $thresholdDate) {
                if ($DryRun) {
                    Write-Host "  ✓ [DRY RUN] Would delete: $rgName (created $daysSinceCreation days ago)" -ForegroundColor Magenta
                    $deletedCount++
                } else {
                    Write-Host "  ✓ Created $daysSinceCreation days ago. Deleting..." -ForegroundColor Yellow
                    
                    # Delete the resource group without waiting
                    az group delete --name $rgName --yes --no-wait
                    
                    if ($LASTEXITCODE -eq 0) {
                        Write-Host "  ✓ Deletion initiated for: $rgName" -ForegroundColor Green
                        $deletedCount++
                    } else {
                        Write-Host "  ✗ Failed to delete: $rgName" -ForegroundColor Red
                    }
                }
            } else {
                Write-Host "  - Created within 4 days ($daysSinceCreation days ago). Skipping." -ForegroundColor Gray
                $skippedCount++
            }
        }
    }
    catch {
        Write-Host "  ✗ Error processing $rgName : $_" -ForegroundColor Red
        $skippedCount++
    }
    
    Write-Host ""
}

# Summary
Write-Host "=== Summary ===" -ForegroundColor Cyan
if ($DryRun) {
    Write-Host "Mode: DRY RUN" -ForegroundColor Magenta
}
Write-Host "Total matching resource groups: $($matchingRGs.Count)" -ForegroundColor White
if ($DryRun) {
    Write-Host "Would delete: $deletedCount" -ForegroundColor Magenta
} else {
    Write-Host "Deletion initiated: $deletedCount" -ForegroundColor Green
}
Write-Host "Skipped: $skippedCount" -ForegroundColor Yellow

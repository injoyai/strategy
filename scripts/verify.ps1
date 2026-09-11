# Unified verification entry point for the strategy repository.
# Usage:  .\scripts\verify.ps1 [-SkipWeb] [-KeepContract]
# Steps:  gofmt -> go vet -> go test -> openapi build/check -> generated diff -> web (typecheck/build/test)
[CmdletBinding()]
param(
    [switch]$SkipWeb,
    # Skip the generated openapi.json diff check (useful before the contract exists in git history).
    [switch]$KeepContract
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $failed = @()

    function Invoke-Step([string]$Name, [scriptblock]$Action) {
        Write-Host "==> $Name" -ForegroundColor Cyan
        & $Action
        if ($LASTEXITCODE -ne 0) {
            $script:failed += $Name
            throw "$Name failed (exit $LASTEXITCODE)"
        }
    }

    # 1. Formatting: fail if gofmt would rewrite anything.
    $fmt = @(gofmt -l .) | Where-Object { $_ }
    if ($fmt.Count -gt 0) {
        Write-Host "gofmt would reformat:" $fmt -ForegroundColor Red
        $failed += 'gofmt'
        throw 'gofmt check failed'
    }
    Write-Host "gofmt: clean"

    Invoke-Step 'go vet' { go vet ./... }
    Invoke-Step 'go test' { go test ./... }

    # 2. OpenAPI contract chain: generate then validate. openapi.json is never hand-edited.
    Invoke-Step 'build_openapi' { python docs/api/build_openapi.py }
    Invoke-Step 'check_contract' { python docs/api/check_contract.py }

    if (-not $KeepContract) {
        # Generated contract must be committed; flag uncommitted drift.
        $diff = git diff -- docs/api/openapi.json
        if ($diff) {
            Write-Host "generated openapi.json has uncommitted changes:" -ForegroundColor Red
            Write-Host $diff -ForegroundColor Red
            $failed += 'contract-diff'
            throw 'generated openapi.json differs from committed version; run build_openapi.py and commit'
        }
        Write-Host "contract diff: clean"
    }

    # 3. Web toolchain: typecheck, production build, unit tests.
    if (-not $SkipWeb) {
        Push-Location web
        try {
            Invoke-Step 'web typecheck' { npm run typecheck }
            Invoke-Step 'web build' { npm run build }
            # Vitest 5 unconditionally writes an API token under %LOCALAPPDATA%\vitest
            # before falling back to the workspace; keep that first write inside the
            # project so the run works in restricted environments. Scoped to this step.
            $env:LOCALAPPDATA = Join-Path (Get-Location) '.local'
            Invoke-Step 'web test' { npm test }
        } finally {
            Pop-Location
        }
    }

    if ($failed.Count -gt 0) {
        Write-Host "FAILED steps: $($failed -join ', ')" -ForegroundColor Red
        exit 1
    }
    Write-Host "verify: OK" -ForegroundColor Green
} finally {
    Pop-Location
}

<#
.SYNOPSIS
    Windows equivalent of the Makefile.

.DESCRIPTION
    `make` is not installed on a default Windows box, and this project's primary
    persona is a Windows user. This script runs the same commands as the Makefile
    so the documented workflow works without installing extra tooling.

    Keep this file and the Makefile in sync.

.PARAMETER Target
    The target to run. Run without arguments to list them.

.PARAMETER Level
    Level id used by the `run` target. Defaults to nav-01.

.EXAMPLE
    .\make.ps1 build
    .\make.ps1 ci
    .\make.ps1 run -Level pipe-05
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Target = 'help',

    [string]$Level = 'nav-01'
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

$Binary  = 'shellforge.exe'
$Pkg     = './cmd/shellforge'
$BinDir  = 'bin'
$Image   = 'shellforge-sandbox'
$Tag     = 'dev'

function Get-GitValue([string[]]$GitArgs, [string]$Fallback) {
    try {
        $value = & git @GitArgs 2>$null
        if ($LASTEXITCODE -eq 0 -and $value) { return "$value".Trim() }
    } catch { }
    return $Fallback
}

$Version   = Get-GitValue @('describe', '--tags', '--always', '--dirty') 'dev'
$Commit    = Get-GitValue @('rev-parse', '--short', 'HEAD') 'unknown'
$BuildDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$LdFlags   = "-s -w -X main.version=$Version -X main.commit=$Commit -X main.buildDate=$BuildDate"

function Invoke-Step([string]$Name, [scriptblock]$Body) {
    Write-Host "==> $Name" -ForegroundColor Cyan
    & $Body
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL: $Name (exit $LASTEXITCODE)" -ForegroundColor Red
        exit $LASTEXITCODE
    }
}

function Get-ContainerEngine {
    foreach ($candidate in @('docker', 'podman')) {
        $cmd = Get-Command $candidate -ErrorAction SilentlyContinue
        if ($cmd) { return $candidate }
    }
    return 'docker'
}

function Invoke-Bash([string]$ScriptPath) {
    # Git for Windows ships bash. Prefer it over WSL so the check does not depend
    # on a provisioned distro.
    $bash = Get-Command bash -ErrorAction SilentlyContinue
    if (-not $bash) {
        Write-Host "SKIP: bash not found, cannot run $ScriptPath" -ForegroundColor Yellow
        Write-Host "      Install Git for Windows, or run this target in CI." -ForegroundColor Yellow
        return
    }
    & $bash.Source $ScriptPath
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

function Show-Help {
    Write-Host 'Shellforge make.ps1 targets:' -ForegroundColor Green
    Write-Host ''
    $rows = [ordered]@{
        'build'    = 'Compile the binary into bin\'
        'install'  = 'Install the binary into GOBIN'
        'test'     = 'Run unit tests'
        'race'     = 'Run tests under the race detector'
        'cover'    = 'Run tests and print total coverage'
        'fmt'      = 'Format all Go source in place'
        'vet'      = 'Run go vet'
        'punct'    = 'Fail on em dashes, en dashes, smart quotes (Rule 0)'
        'links'    = 'Fail on broken relative links in Markdown'
        'arch'     = 'Enforce the layer dependency rule'
        'labels'   = 'Sync GitHub labels from .github/labels.yml'
        'lint'     = 'gofmt check, vet, punctuation, links, layer test'
        'vuln'     = 'govulncheck against dependencies and toolchain'
        'gosec'    = 'Static security analysis'
        'sec'      = 'All security checks'
        'image'    = 'Build the sandbox container image'
        'rootfs'   = 'Export the WSL rootfs tarball'
        'run'      = 'Play one level (-Level <id>)'
        'validate' = 'Validate the content pack'
        'golden'   = 'Run the golden test for every level'
        'tools'    = 'Report expected toolchain versions'
        'clean'    = 'Remove build output'
        'ci'       = 'Everything CI runs'
    }
    foreach ($k in $rows.Keys) { '  {0,-10} {1}' -f $k, $rows[$k] | Write-Host }
}

switch ($Target.ToLowerInvariant()) {

    'help' { Show-Help }

    'build' {
        New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
        Invoke-Step 'build' { go build -trimpath -ldflags $LdFlags -o "$BinDir\$Binary" $Pkg }
        Write-Host "built $BinDir\$Binary $Version" -ForegroundColor Green
    }

    'install' { Invoke-Step 'install' { go install -trimpath -ldflags $LdFlags $Pkg } }

    'test'  { Invoke-Step 'test'  { go test ./... } }
    'race'  { Invoke-Step 'race'  { go test -race ./... } }
    'vet'   { Invoke-Step 'vet'   { go vet ./... } }
    'fmt'   { Invoke-Step 'fmt'   { gofmt -s -w . } }
    'arch'  { Invoke-Step 'arch'  { go test ./internal/archtest/... } }

    'cover' {
        Invoke-Step 'cover' { go test -coverprofile=coverage.out ./... }
        go tool cover -func=coverage.out | Select-Object -Last 1
    }

    'punct'  { Invoke-Bash 'scripts/check-punctuation.sh' }
    'links'  { Invoke-Bash 'scripts/check-links.sh' }
    'labels' { Invoke-Bash 'scripts/sync-labels.sh' }

    'lint' {
        Invoke-Step 'vet' { go vet ./... }
        Invoke-Bash 'scripts/check-punctuation.sh'
        Invoke-Bash 'scripts/check-links.sh'
        Invoke-Step 'arch' { go test ./internal/archtest/... }
        $unformatted = & gofmt -s -l .
        if ($unformatted) {
            Write-Host 'FAIL: these files are not gofmt -s clean:' -ForegroundColor Red
            $unformatted | ForEach-Object { Write-Host "  $_" }
            exit 1
        }
        Write-Host 'OK: lint clean' -ForegroundColor Green
    }

    'vuln'  { Invoke-Step 'govulncheck' { go run golang.org/x/vuln/cmd/govulncheck@latest ./... } }
    'gosec' { Invoke-Step 'gosec' { go run github.com/securego/gosec/v2/cmd/gosec@latest -quiet ./... } }

    'sec' {
        Invoke-Step 'govulncheck' { go run golang.org/x/vuln/cmd/govulncheck@latest ./... }
        Invoke-Step 'gosec' { go run github.com/securego/gosec/v2/cmd/gosec@latest -quiet ./... }
    }

    'image' {
        $engine = Get-ContainerEngine
        Invoke-Step "image ($engine)" { & $engine build -f images/Containerfile -t "${Image}:${Tag}" images/ }
    }

    'rootfs' {
        $engine = Get-ContainerEngine
        New-Item -ItemType Directory -Force -Path 'images/out' | Out-Null
        Invoke-Step "image ($engine)" { & $engine build -f images/Containerfile -t "${Image}:${Tag}" images/ }
        & $engine rm -f "$Image-export" 2>$null | Out-Null
        Invoke-Step 'create' { & $engine create --name "$Image-export" "${Image}:${Tag}" /bin/true }
        Invoke-Step 'export' { & $engine export "$Image-export" -o 'images/out/rootfs.tar' }
        & $engine rm -f "$Image-export" | Out-Null
        $hash = (Get-FileHash -Algorithm SHA256 'images/out/rootfs.tar').Hash.ToLowerInvariant()
        "$hash  rootfs.tar" | Out-File -FilePath 'images/out/rootfs.tar.sha256' -Encoding ascii
        Write-Host "exported images/out/rootfs.tar" -ForegroundColor Green
        Write-Host "sha256 $hash"
    }

    'run' {
        New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
        Invoke-Step 'build' { go build -trimpath -ldflags $LdFlags -o "$BinDir\$Binary" $Pkg }
        & "$BinDir\$Binary" run $Level
    }

    'validate' {
        Invoke-Step 'build' { go build -trimpath -ldflags $LdFlags -o "$BinDir\$Binary" $Pkg }
        & "$BinDir\$Binary" author validate packs/core-linux-basics
    }

    'golden' {
        Invoke-Step 'build' { go build -trimpath -ldflags $LdFlags -o "$BinDir\$Binary" $Pkg }
        & "$BinDir\$Binary" author test --all
    }

    'tools' {
        go version
        $engine = Get-ContainerEngine
        Write-Host "container engine: $engine"
        try { & $engine --version } catch { Write-Host '  not available' }
        Write-Host "wsl:"
        try { & wsl.exe --version } catch { Write-Host '  not available' }
    }

    'clean' {
        foreach ($p in @($BinDir, 'images/out', 'coverage.out', 'coverage.html')) {
            if (Test-Path $p) { Remove-Item -Recurse -Force $p }
        }
        go clean -testcache
        Write-Host 'cleaned' -ForegroundColor Green
    }

    'ci' {
        & $PSCommandPath lint
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        Invoke-Step 'test' { go test ./... }
        Invoke-Step 'race' { go test -race ./... }
        & $PSCommandPath sec
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        Write-Host 'OK: ci clean' -ForegroundColor Green
    }

    default {
        Write-Host "Unknown target: $Target" -ForegroundColor Red
        Write-Host ''
        Show-Help
        exit 2
    }
}

<#
.SYNOPSIS
    Shellforge installer for Windows.

.DESCRIPTION
    powershell -ExecutionPolicy Bypass -Scope Process -File install.ps1

    Verification happens before placement. Test-Checksum runs before
    Install-Binary ever does, and Install-Binary cannot run at all unless
    Test-Checksum succeeded. An installer that verifies after placing
    something is not verifying.

    The release published by .github/workflows/release.yml carries three
    archives: shellforge_VERSION_linux_amd64.tar.gz,
    shellforge_VERSION_linux_arm64.tar.gz, and
    shellforge_VERSION_windows_amd64.zip, plus one SHA256SUMS listing all of
    them. This script only ever fetches the Windows one; the Linux archives
    belong to scripts/install.sh.

    -Scope Process is all this script needs. It never changes the machine or
    user execution policy, and it never elevates: no Start-Process -Verb
    RunAs, anywhere.

.PARAMETER Version
    A tag such as v0.1.0. Default: the latest release.

.PARAMETER BinDir
    Where the binary goes. Default: $env:LOCALAPPDATA\Programs\shellforge.

.PARAMETER BaseUrl
    Where to fetch from. Default:
    https://github.com/JoottunAtish/ShellForge/releases/download

.PARAMETER NoPathChange
    Do not touch the user PATH. Print what to add instead.

.PARAMETER Force
    Overwrite an existing file at the target path.
#>

[CmdletBinding()]
param(
    [string]$Version,
    [string]$BinDir,
    [string]$BaseUrl,
    [switch]$NoPathChange,
    [switch]$Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$DefaultBaseUrl = 'https://github.com/JoottunAtish/ShellForge/releases/download'
$ReservedDeviceNames = @(
    'CON', 'PRN', 'AUX', 'NUL',
    'COM1', 'COM2', 'COM3', 'COM4', 'COM5', 'COM6', 'COM7', 'COM8', 'COM9',
    'LPT1', 'LPT2', 'LPT3', 'LPT4', 'LPT5', 'LPT6', 'LPT7', 'LPT8', 'LPT9'
)

$script:WorkDir = $null

function Write-Failure {
    # Message plus remediation to stderr, then throw. Every refusal and
    # every failure goes through here, so every message names what failed,
    # why, and the next step, per non-negotiable 6 in CLAUDE.md.
    param(
        [Parameter(Mandatory = $true)][string]$Message,
        [Parameter(Mandatory = $true)][string]$Remediation
    )
    [Console]::Error.WriteLine("install.ps1: $Message")
    [Console]::Error.WriteLine("  $Remediation")
    throw $Message
}

function Resolve-FinalUri {
    # Extracts the resolved URL from an Invoke-WebRequest response,
    # portably across PowerShell 5.1 and PowerShell 7. On 5.1,
    # Invoke-WebRequest's BaseResponse is a .NET HttpWebResponse, which
    # exposes ResponseUri directly. On 7 it is a
    # System.Net.Http.HttpResponseMessage instead, which has no
    # ResponseUri property at all: the final URL after redirects lives at
    # RequestMessage.RequestUri there, because HttpClient rewrites the
    # request's own RequestUri to match wherever the last redirect landed.
    # Checking PSObject.Properties rather than dot-referencing a property
    # that might not exist is required under Set-StrictMode -Version
    # Latest, set at the top of this script: a bare miss there throws
    # instead of returning $null, which is why the untested version of
    # this function could never work on PowerShell 7 at all.
    param(
        [Parameter(Mandatory = $true)]$Response
    )
    $base = $Response.BaseResponse
    if ($base.PSObject.Properties['ResponseUri']) {
        return $base.ResponseUri.AbsoluteUri
    }
    if ($base.PSObject.Properties['RequestMessage'] -and $base.RequestMessage -and
        $base.RequestMessage.PSObject.Properties['RequestUri'] -and $base.RequestMessage.RequestUri) {
        return $base.RequestMessage.RequestUri.AbsoluteUri
    }
    return $null
}

function Resolve-Version {
    param(
        [string]$RequestedVersion,
        [string]$EffectiveBaseUrl
    )

    if ($RequestedVersion) {
        return $RequestedVersion
    }

    if ($EffectiveBaseUrl -ne $DefaultBaseUrl) {
        Write-Failure "-Version is not set, and -BaseUrl is set to a custom value" `
            "Pass -Version with a tag such as v0.1.0. A custom base URL cannot be resolved to a latest version, because the asset names carry the version."
    }

    $latestUrl = 'https://github.com/JoottunAtish/ShellForge/releases/latest'
    try {
        $response = Invoke-WebRequest -Uri $latestUrl -MaximumRedirection 5 -UseBasicParsing -Method Head
        $finalUri = Resolve-FinalUri -Response $response
    }
    catch {
        Write-Failure "could not resolve the latest release" `
            "Check your network connection, or pass -Version with a specific tag such as v0.1.0."
    }

    $tag = ($finalUri -split '/')[-1]
    if (-not $tag) {
        Write-Failure "could not determine the latest release tag from $finalUri" `
            "Pass -Version with a specific tag such as v0.1.0."
    }
    return $tag
}

function Get-Asset {
    # Download to a path. A file:// URL (the -BaseUrl test seam) is copied
    # directly rather than handed to Invoke-WebRequest, which does not speak
    # that scheme; a real http or https URL goes through Invoke-WebRequest as
    # usual.
    param(
        [Parameter(Mandatory = $true)][string]$Url,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    try {
        if ($Url -match '^file://') {
            $localPath = [System.Uri]::new($Url).LocalPath
            Copy-Item -LiteralPath $localPath -Destination $Destination -Force
        }
        else {
            Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing
        }
    }
    catch {
        Write-Failure "download failed: $Url" `
            "Check the URL and your network connection, then run the installer again."
    }
}

function Test-Checksum {
    # Get-FileHash -Algorithm SHA256, compare, refuse on mismatch. Exact
    # filename field match, not a substring: the two Linux asset names share
    # a long prefix with each other and, on this platform, with nothing;
    # still matched exactly for the same reason install.sh does.
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$SumsPath,
        [Parameter(Mandatory = $true)][string]$AssetName
    )

    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $ArchivePath).Hash.ToLowerInvariant()

    $matchLine = $null
    foreach ($rawLine in Get-Content -LiteralPath $SumsPath) {
        $parts = $rawLine -split '\s+', 2
        if ($parts.Count -ge 2 -and $parts[1].Trim() -eq $AssetName) {
            $matchLine = $parts
            break
        }
    }
    if (-not $matchLine) {
        Write-Failure "SHA256SUMS has no entry for $AssetName" `
            "The release may be incomplete. Try a different -Version, or report this."
    }

    $expected = $matchLine[0].Trim().ToLowerInvariant()
    if ($actual -ne $expected) {
        Write-Failure "checksum mismatch for $AssetName`: expected $expected, got $actual" `
            "Nothing was installed. The download may be corrupted or tampered with. Run the installer again, and if this repeats, report it."
    }
}

function Test-BinDir {
    # Every refusal in the table in docs/LEVEL... no, in the install.sh
    # safety table, plus two Windows specifics: a reserved device name as
    # the last path segment, and alternate data stream syntax.
    param([Parameter(Mandatory = $true)][string]$Candidate)

    if ([string]::IsNullOrEmpty($Candidate)) {
        Write-Failure "-BinDir is empty" `
            "Pass -BinDir with an absolute path such as `$env:LOCALAPPDATA\Programs\shellforge, or omit it to use that default."
    }

    if ($Candidate -eq '~' -or $Candidate.StartsWith('~\') -or $Candidate.StartsWith('~/')) {
        Write-Failure "-BinDir is '$Candidate': a literal ~ is not expanded here" `
            "Use an explicit path such as `$env:LOCALAPPDATA\Programs\shellforge."
    }

    if (-not [System.IO.Path]::IsPathRooted($Candidate)) {
        Write-Failure "-BinDir is not an absolute path: $Candidate" `
            "Pass an absolute path, for example `$env:LOCALAPPDATA\Programs\shellforge."
    }

    $trimmed = $Candidate.TrimEnd('\', '/')
    if ($trimmed -eq '' -or $trimmed -match '^[A-Za-z]:$') {
        Write-Failure "-BinDir is a drive root: $Candidate" `
            "Pass a real directory such as `$env:LOCALAPPDATA\Programs\shellforge."
    }

    $homeDir = $env:USERPROFILE
    if ($homeDir -and ($trimmed -ieq $homeDir.TrimEnd('\', '/'))) {
        Write-Failure "-BinDir is your home directory" `
            "shellforge will not scatter a binary directly into your home directory. Use the documented default instead."
    }

    $segments = $Candidate -split '[\\/]'
    if ($segments -contains '..') {
        Write-Failure "-BinDir contains a '..' segment: $Candidate" `
            "Pass a plain absolute path with no '..' segments."
    }

    # Alternate data stream syntax: a colon after the drive letter's own
    # colon. The stream would be invisible to the user and to uninstall.
    $afterDrive = $Candidate -replace '^[A-Za-z]:', ''
    if ($afterDrive.Contains(':')) {
        Write-Failure "-BinDir contains a colon after the drive letter: $Candidate" `
            "That is alternate data stream syntax. Pass a plain path with no extra colons."
    }

    # A reserved device name as the last segment is a device, not a file,
    # compared case-insensitively and after stripping any extension, because
    # NUL.exe is also the device.
    $lastSegment = $segments[-1]
    $lastSegmentNoExt = [System.IO.Path]::GetFileNameWithoutExtension($lastSegment)
    if ($ReservedDeviceNames -icontains $lastSegmentNoExt) {
        Write-Failure "-BinDir's last segment is a reserved device name: $lastSegment" `
            "$lastSegmentNoExt is a device name, not a directory that can be created. Pass a different -BinDir."
    }

    $target = Join-Path $Candidate 'shellforge.exe'
    $existingItem = Get-Item -LiteralPath $target -ErrorAction SilentlyContinue
    if ($existingItem -and $existingItem.LinkType) {
        Write-Failure "$target is a symlink or junction" `
            "Remove it by hand and run the installer again. Writing through it can land the binary somewhere you did not name, so this is refused even with -Force."
    }
    if ((Test-Path -LiteralPath $target -PathType Leaf) -and -not $Force) {
        Write-Failure "$target already exists" `
            "Re-run with -Force to overwrite it, or remove it yourself first."
    }
}

function Install-Binary {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$TargetBinDir
    )

    $extractDir = Join-Path $script:WorkDir 'extract'
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null
    try {
        Expand-Archive -LiteralPath $ArchivePath -DestinationPath $extractDir -Force
    }
    catch {
        Write-Failure "could not extract $ArchivePath" `
            "The archive may be corrupted. Delete it and run the installer again."
    }

    New-Item -ItemType Directory -Path $TargetBinDir -Force | Out-Null

    $source = Join-Path $extractDir 'shellforge.exe'
    $target = Join-Path $TargetBinDir 'shellforge.exe'
    try {
        Copy-Item -LiteralPath $source -Destination $target -Force
    }
    catch {
        Write-Failure "could not place the binary at $target" `
            "Check that you have write permission there."
    }
}

function Get-UserPathRegistryValue {
    # Reads the RAW, unexpanded Path value straight out of the user
    # Environment registry key, along with its REG_SZ or REG_EXPAND_SZ
    # kind. [Environment]::GetEnvironmentVariable always returns the
    # EXPANDED value and [Environment]::SetEnvironmentVariable always
    # WRITES REG_SZ, so going through those two together silently
    # flattens a REG_EXPAND_SZ Path (one carrying a token such as
    # %USERPROFILE% or %LOCALAPPDATA%, both common) into a literal
    # expanded path, permanently, the moment this installer runs. Reading
    # and writing through the registry key directly, and preserving
    # whichever kind was already there, is what avoids that.
    #
    # A missing Path value, or a missing Environment key altogether (rare,
    # but a brand new profile may not have written to it yet), reads back
    # as an empty String-kind value: the same starting point
    # SetEnvironmentVariable would have had on a machine with nothing set.
    $envKey = Get-Item -LiteralPath 'HKCU:\Environment' -ErrorAction SilentlyContinue
    if (-not $envKey) {
        return @{ Value = ''; Kind = [Microsoft.Win32.RegistryValueKind]::String }
    }
    $raw = $envKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $kind = [Microsoft.Win32.RegistryValueKind]::String
    if ($envKey.GetValueNames() -contains 'Path') {
        $kind = $envKey.GetValueKind('Path')
    }
    return @{ Value = $raw; Kind = $kind }
}

function Update-UserPath {
    # The one deliberate asymmetry with install.sh: docs/01-install-windows.md
    # is written for somebody who has never opened a terminal, and "add this
    # to your PATH" is not an instruction that person can follow unaided.
    #
    # 'User' scope only, never 'Machine', and never elevation. Reads the
    # current value first and skips entirely when the directory is already
    # present, compared case-insensitively and after trimming a trailing
    # backslash, so running this twice does not append twice.
    param([Parameter(Mandatory = $true)][string]$TargetBinDir)

    if ($NoPathChange) {
        Write-Host ''
        Write-Host "-NoPathChange was passed. $TargetBinDir was not added to your PATH."
        Write-Host 'Add it yourself: Settings > System > About > Advanced system settings >'
        Write-Host 'Environment Variables, then add this directory to your user Path variable:'
        Write-Host ''
        Write-Host "    $TargetBinDir"
        return
    }

    $currentExpanded = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($null -eq $currentExpanded) { $currentExpanded = '' }

    $normalizedTarget = $TargetBinDir.TrimEnd('\')
    $alreadyPresent = $currentExpanded -split ';' | Where-Object {
        $_.TrimEnd('\') -ieq $normalizedTarget
    }
    if ($alreadyPresent) {
        Write-Host ''
        Write-Host "$TargetBinDir is already on your user PATH. Nothing changed."
        return
    }

    # From here on, work with the RAW value and write the SAME kind back:
    # appending our own already-absolute $TargetBinDir to a raw value that
    # still carries %USERPROFILE% or %LOCALAPPDATA% tokens leaves those
    # tokens exactly as they were, rather than expanding the whole string.
    $regValue = Get-UserPathRegistryValue
    $currentRaw = $regValue.Value
    $new = if ($currentRaw -eq '') { $TargetBinDir } else { "$currentRaw;$TargetBinDir" }

    if (-not (Test-Path -LiteralPath 'HKCU:\Environment')) {
        New-Item -Path 'HKCU:\Environment' -Force | Out-Null
    }
    Set-ItemProperty -LiteralPath 'HKCU:\Environment' -Name 'Path' -Value $new -Type $regValue.Kind
    Write-Host ''
    Write-Host "Added $TargetBinDir to your user PATH."
    Write-Host "  old length: $($currentRaw.Length) characters"
    Write-Host "  new length: $($new.Length) characters"
    Write-Host 'Open a new terminal for this to take effect.'
}

function Invoke-Doctor {
    # Never fails the install. A failing doctor is information (a missing
    # Docker, say), not an install error.
    param([Parameter(Mandatory = $true)][string]$TargetBinDir)

    $exe = Join-Path $TargetBinDir 'shellforge.exe'
    Write-Host ''
    Write-Host "shellforge is installed at $exe"
    Write-Host 'Next: run shellforge init'
    Write-Host ''
    & $exe doctor
    if ($LASTEXITCODE -eq 0) {
        Write-Host 'doctor: no problems found'
    }
    else {
        Write-Host 'doctor reported a problem. This does not mean the install failed: the binary is installed.'
        Write-Host 'Run shellforge doctor for details once you have addressed it.'
    }
}

function Remove-WorkDir {
    # The only recursive delete in this script, and it only ever operates on
    # the directory this script itself created under $env:TEMP, guarded to
    # match its own naming template. It never takes a value derived from
    # user input.
    if (-not $script:WorkDir) { return }
    $leaf = Split-Path -Leaf $script:WorkDir
    if ($leaf -like 'shellforge-install.*') {
        Remove-Item -LiteralPath $script:WorkDir -Recurse -Force -ErrorAction SilentlyContinue
    }
    else {
        Write-Host "install.ps1: refusing to remove unexpected directory: $script:WorkDir"
    }
}

function Invoke-Main {
    if (-not $BinDir) {
        $BinDir = Join-Path $env:LOCALAPPDATA 'Programs\shellforge'
    }
    if (-not $BaseUrl) {
        $BaseUrl = $DefaultBaseUrl
    }

    Test-BinDir -Candidate $BinDir
    $resolvedVersion = Resolve-Version -RequestedVersion $Version -EffectiveBaseUrl $BaseUrl

    $workDirName = "shellforge-install.$([System.Guid]::NewGuid().ToString('N'))"
    $script:WorkDir = Join-Path $env:TEMP $workDirName
    New-Item -ItemType Directory -Path $script:WorkDir -Force | Out-Null

    try {
        $asset = "shellforge_${resolvedVersion}_windows_amd64.zip"
        $archivePath = Join-Path $script:WorkDir $asset
        $sumsPath = Join-Path $script:WorkDir 'SHA256SUMS'

        Get-Asset -Url "$BaseUrl/$resolvedVersion/$asset" -Destination $archivePath
        Get-Asset -Url "$BaseUrl/$resolvedVersion/SHA256SUMS" -Destination $sumsPath
        Test-Checksum -ArchivePath $archivePath -SumsPath $sumsPath -AssetName $asset
        Install-Binary -ArchivePath $archivePath -TargetBinDir $BinDir

        Update-UserPath -TargetBinDir $BinDir
        Invoke-Doctor -TargetBinDir $BinDir
    }
    finally {
        Remove-WorkDir
    }
}

Invoke-Main

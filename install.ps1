param(
    [string]$ReleaseBase = "https://github.com/egv/yolo-runner/releases/latest/download",
    [string]$InstallDir = ""
)

$ProgressPreference = "SilentlyContinue"
$ErrorActionPreference = "Stop"

$artifact = "yolo-runner_windows_amd64.zip"
$artifactUrl = "${ReleaseBase}/${artifact}"
$checksumFile = "checksums-${artifact}.txt"
$checksumUrl = "${ReleaseBase}/${checksumFile}"

$tmpRoot = Join-Path $env:TEMP ("yolo-runner-install-" + [guid]::NewGuid())
$artifactPath = Join-Path $tmpRoot $artifact
$checksumPath = Join-Path $tmpRoot $checksumFile
$extractDir = Join-Path $tmpRoot "extract"

New-Item -ItemType Directory -Path $tmpRoot -Force | Out-Null
New-Item -ItemType Directory -Path $extractDir -Force | Out-Null

Invoke-WebRequest -Uri $artifactUrl -OutFile $artifactPath
Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumPath

$expected = $null
Get-Content -Path $checksumPath | ForEach-Object {
    $parts = $_ -split "\s+"
    if ($parts.Count -lt 2) {
        return
    }

    $path = $parts[1]
    if ($path -eq $artifact -or $path -eq ("./" + $artifact) -or $path -eq ("dist/" + $artifact) -or (Split-Path -Leaf $path) -eq $artifact) {
        $expected = $parts[0].ToLowerInvariant()
    }
}

if (-not $expected) {
    throw "checksum not found for artifact: $artifact"
}

$actual = (Get-FileHash -Algorithm SHA256 -Path $artifactPath).Hash.ToLowerInvariant()
if ($actual -ne $expected) {
    throw "checksum mismatch for $artifact"
}

Expand-Archive -Path $artifactPath -DestinationPath $extractDir -Force

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    if ($env:LOCALAPPDATA) {
        $InstallDir = Join-Path $env:LOCALAPPDATA "yolo-runner\bin"
    } else {
        $InstallDir = Join-Path $HOME ".local\bin"
    }
}

New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

$installed = $false
Get-ChildItem -Path $extractDir -File | ForEach-Object {
    Move-Item -Path $_.FullName -Destination (Join-Path $InstallDir $_.Name) -Force
    Write-Output "installed $($_.Name) to $InstallDir"
    $installed = $true
}

if (-not $installed) {
    throw "expected binaries not found in artifact: $artifact"
}

Write-Output "next: install the repo-local OpenCode assets under .opencode/agent, .opencode/skills, and .opencode/commands inside your repository"

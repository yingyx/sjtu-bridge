# Dot-source this file; changes apply only to the current PowerShell process.
[CmdletBinding()]
param(
    [string]$GoBin
)

$bridgeGoMod = Get-Content -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'go.mod') -Raw -Encoding UTF8
$bridgeVersionMatch = [regex]::Match($bridgeGoMod, '(?m)^go (\d+\.\d+\.\d+)\s*$')
if (-not $bridgeVersionMatch.Success) { throw 'go.mod must pin a Go patch version.' }
$bridgeExpectedVersion = 'go' + $bridgeVersionMatch.Groups[1].Value
$bridgePreviousToolchain = $env:GOTOOLCHAIN
$env:GOTOOLCHAIN = 'local' # Never download a toolchain while detecting it.

try {
    if ($GoBin) {
        $bridgeGoCandidates = @(Join-Path $GoBin 'go.exe')
    }
    else {
        $bridgeGoCandidates = @((Get-Command go.exe -CommandType Application -All -ErrorAction SilentlyContinue).Source)
        $bridgeModuleCache = if ($env:GOMODCACHE) {
            $env:GOMODCACHE
        } elseif ($env:GOPATH) {
            Join-Path (($env:GOPATH -split ';')[0]) 'pkg\mod'
        } else {
            Join-Path ([Environment]::GetFolderPath('UserProfile')) 'go\pkg\mod'
        }
        $bridgeProcessor = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        $bridgeGoArch = switch ($bridgeProcessor) { 'ARM64' { 'arm64' } 'x86' { '386' } default { 'amd64' } }
        $bridgeGoCandidates += Join-Path $bridgeModuleCache "golang.org\toolchain@v0.0.1-$bridgeExpectedVersion.windows-$bridgeGoArch\bin\go.exe"
    }

    $bridgeSelectedGo = $null
    foreach ($bridgeCandidate in $bridgeGoCandidates) {
        if (-not $bridgeCandidate -or -not (Test-Path -LiteralPath $bridgeCandidate -PathType Leaf)) { continue }
        try {
            $bridgeVersionText = (& $bridgeCandidate version 2>$null) -join ''
            $bridgeDetectedVersion = [regex]::Match($bridgeVersionText, '^go version (go\d+\.\d+\.\d+) windows/')
            if ($LASTEXITCODE -eq 0 -and $bridgeDetectedVersion.Success -and $bridgeDetectedVersion.Groups[1].Value -eq $bridgeExpectedVersion) {
                $bridgeSelectedGo = (Get-Item -LiteralPath $bridgeCandidate).FullName
                break
            }
        }
        catch { continue }
    }
    if (-not $bridgeSelectedGo) {
        throw "Cannot find $bridgeExpectedVersion for Windows. Install that version or dot-source this script with -GoBin PATH_TO_GO_BIN. No toolchain was downloaded."
    }
}
catch {
    $env:GOTOOLCHAIN = $bridgePreviousToolchain
    throw
}

$bridgeSelectedBin = Split-Path -Parent $bridgeSelectedGo
$bridgePathTail = (($env:PATH -split ';') | Where-Object { $_ -ne $bridgeSelectedBin }) -join ';'
$env:PATH = "$bridgeSelectedBin;$bridgePathTail"
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = $OutputEncoding
Write-Output $bridgeVersionText

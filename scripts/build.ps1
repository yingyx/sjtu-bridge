# Build the Windows executable with symbols retained for inspection and debugging.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$bridgeRoot = Split-Path -Parent $PSScriptRoot
$bridgeSavedEnvironment = @{}
foreach ($bridgeVariable in @('GOTOOLCHAIN', 'GOOS', 'GOARCH', 'CGO_ENABLED')) {
    $bridgeSavedEnvironment[$bridgeVariable] = [Environment]::GetEnvironmentVariable($bridgeVariable, 'Process')
}

Push-Location -LiteralPath $bridgeRoot
try {
    $env:GOTOOLCHAIN = 'local'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'

    $bridgeGoMod = Get-Content -LiteralPath 'go.mod' -Raw -Encoding UTF8
    $bridgeVersionMatch = [regex]::Match($bridgeGoMod, '(?m)^go (\d+\.\d+\.\d+)\s*$')
    if (-not $bridgeVersionMatch.Success) { throw 'go.mod must pin a Go patch version.' }
    $bridgeExpectedVersion = 'go' + $bridgeVersionMatch.Groups[1].Value
    $bridgeVersion = (& go env GOVERSION).Trim()
    if ($LASTEXITCODE -ne 0 -or $bridgeVersion -ne $bridgeExpectedVersion) {
        throw "Use $bridgeExpectedVersion; enable scripts/dev-env.ps1 or the pinned CI toolchain."
    }
    $bridgeGoFlags = & go env GOFLAGS
    if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect GOFLAGS.' }
    if (-not [string]::IsNullOrWhiteSpace($bridgeGoFlags)) {
        throw 'GOFLAGS must be empty for this build; inspect its environment or go env configuration.'
    }

    & go mod verify
    if ($LASTEXITCODE -ne 0) { throw 'Module verification failed.' }
    $bridgeArguments = @('build', '-mod=readonly', '-trimpath', '-ldflags=', '-o', 'bin/sjtu.exe', './cmd/sjtu')
    & go @bridgeArguments
    if ($LASTEXITCODE -ne 0) { throw 'Build failed; no retry or alternate output path was attempted.' }

    $bridgeMainSymbol = & go tool nm 'bin/sjtu.exe' | Select-String -Pattern '\bT main\.main$'
    if ($LASTEXITCODE -ne 0 -or -not $bridgeMainSymbol) {
        throw 'Cannot verify retained symbols in bin/sjtu.exe.'
    }
    $bridgeFile = Get-Item -LiteralPath 'bin/sjtu.exe'
    $bridgeMetadata = [ordered]@{
        file = 'sjtu.exe'
        sha256 = (Get-FileHash -LiteralPath $bridgeFile.FullName -Algorithm SHA256).Hash
        size_bytes = $bridgeFile.Length
        go_version = $bridgeVersion
        goos = $env:GOOS
        goarch = $env:GOARCH
        cgo_enabled = $env:CGO_ENABLED
        symbols = 'retained'
        arguments = $bridgeArguments
    }
    $bridgeJson = ($bridgeMetadata | ConvertTo-Json -Depth 4) + [Environment]::NewLine
    [IO.File]::WriteAllText((Join-Path $bridgeRoot 'bin/sjtu.build.json'), $bridgeJson, [Text.UTF8Encoding]::new($false))
    Write-Output $bridgeJson
}
finally {
    foreach ($bridgeVariable in $bridgeSavedEnvironment.Keys) {
        if ($null -eq $bridgeSavedEnvironment[$bridgeVariable]) {
            Remove-Item -LiteralPath "Env:$bridgeVariable" -ErrorAction SilentlyContinue
        }
        else {
            Set-Item -LiteralPath "Env:$bridgeVariable" -Value $bridgeSavedEnvironment[$bridgeVariable]
        }
    }
    Pop-Location
}

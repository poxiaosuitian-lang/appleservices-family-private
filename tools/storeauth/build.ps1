param(
    [string]$EnvironmentFile = 'D:\appleservices-deps\environment.ps1',
    [string]$OutputPath,
    [switch]$Prepare
)

$ErrorActionPreference = 'Stop'
$savedLocation = Get-Location
$savedEnvironment = [Environment]::GetEnvironmentVariables('Process')

try {
    if (-not (Test-Path -LiteralPath $EnvironmentFile -PathType Leaf)) {
        throw "Dependency environment not found: $EnvironmentFile"
    }
    if ($OutputPath) {
        $OutputPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputPath)
    }
    . $EnvironmentFile
    $env:GOTOOLCHAIN = 'auto'

    $projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
    if (-not $OutputPath) { $OutputPath = Join-Path $projectRoot 'bin\storeauth.exe' }
    if ([IO.Path]::GetPathRoot($OutputPath) -ieq 'C:\') {
        throw 'Choose an output directory outside C:.'
    }
    if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'go.mod') -PathType Leaf)) {
        throw "Store authentication module not found: $PSScriptRoot"
    }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw 'Go is unavailable. Check the dependency environment file.'
    }
    New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($OutputPath)) -Force | Out-Null
    Set-Location -LiteralPath $PSScriptRoot
    & go build -mod=readonly -trimpath -o $OutputPath .
    if ($LASTEXITCODE -ne 0) {
        throw "Store authentication helper build failed (exit $LASTEXITCODE)."
    }
    Write-Host "Built Store authentication helper: $OutputPath"

    if ($Prepare) {
        if (-not $env:APPLESERVICES_CACHE_DIR) {
            $env:APPLESERVICES_CACHE_DIR = 'D:\appleservices-deps\runtime-cache'
        }
        if (-not [IO.Path]::IsPathRooted($env:APPLESERVICES_CACHE_DIR)) {
            throw 'APPLESERVICES_CACHE_DIR must be an absolute path.'
        }
        $env:LOCALAPPDATA = $env:APPLESERVICES_CACHE_DIR
        $env:XDG_CACHE_HOME = $env:APPLESERVICES_CACHE_DIR
        & $OutputPath --prepare
        if ($LASTEXITCODE -ne 0) {
            throw "Store runtime preparation failed (exit $LASTEXITCODE)."
        }
    }
} finally {
    Set-Location -LiteralPath $savedLocation.Path
    foreach ($name in [Environment]::GetEnvironmentVariables('Process').Keys) {
        if (-not $savedEnvironment.Contains($name)) {
            Remove-Item -LiteralPath "Env:\$name"
        }
    }
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
    }
}

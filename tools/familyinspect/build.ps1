param(
    [string]$EnvironmentFile = 'D:\appleservices-deps\environment.ps1',
    [string]$OutputPath
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
    $projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
    if (-not $OutputPath) { $OutputPath = Join-Path $projectRoot 'bin\familyinspect.exe' }
    if ([IO.Path]::GetPathRoot($OutputPath) -ieq 'C:\') {
        throw 'Choose an output directory outside C:.'
    }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw 'Go is unavailable. Check the dependency environment file.'
    }
    New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($OutputPath)) -Force | Out-Null
    Set-Location -LiteralPath $projectRoot
    & go build -mod=readonly -trimpath -o $OutputPath ./tools/familyinspect
    if ($LASTEXITCODE -ne 0) {
        throw "Family Sharing helper build failed (exit $LASTEXITCODE)."
    }
    Write-Host "Built Family Sharing helper: $OutputPath"
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

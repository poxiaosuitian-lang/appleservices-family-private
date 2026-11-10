param(
    [ValidateRange(1024, 65535)][int]$Port = 18741,
    [switch]$NoOpen,
    [string]$EnvironmentFile = 'D:\appleservices-deps\environment.ps1'
)

$ErrorActionPreference = 'Stop'
$url = "http://127.0.0.1:$Port/"

function Get-DashboardResponse {
    try { return Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2 }
    catch { return $null }
}

function Assert-DashboardResponse($Response) {
    if ($Response.Headers['X-AppleServices-UI'] -ne '1') {
        throw "Port $Port is occupied by another application."
    }
}

$existing = Get-DashboardResponse
if ($existing) {
    Assert-DashboardResponse $existing
    Write-Host "Dashboard already running: $url"
    if (-not $NoOpen) { Start-Process $url }
    return
}

if (-not (Test-Path -LiteralPath $EnvironmentFile -PathType Leaf)) {
    throw "Dependency environment not found: $EnvironmentFile"
}
. $EnvironmentFile
$env:APPLESERVICES_CACHE_DIR = 'D:\appleservices-deps\runtime-cache'

foreach ($relative in @('bin\dashboard.exe', 'bin\trusted-device-gui.exe', 'bin\storeauth.exe',
        '.cache\openbubbles-store\rust_lib_bluebubbles.dll', 'tools\mac-push-check.py')) {
    if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot $relative) -PathType Leaf)) {
        throw "Required file missing: $relative"
    }
}
foreach ($command in @('python', 'gcc')) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "Required dependency missing: $command"
    }
}

# Credentials are entered in the dashboard, never inherited from this launcher.
$savedAppleID = $env:APPLESERVICES_APPLEID
$savedPassword = $env:APPLESERVICES_PASSWORD
try {
    $env:APPLESERVICES_APPLEID = $null
    $env:APPLESERVICES_PASSWORD = $null
    $process = Start-Process -FilePath (Join-Path $PSScriptRoot 'bin\dashboard.exe') `
        -ArgumentList @('-port', [string]$Port) -WorkingDirectory $PSScriptRoot `
        -WindowStyle Hidden -PassThru
} finally {
    $env:APPLESERVICES_APPLEID = $savedAppleID
    $env:APPLESERVICES_PASSWORD = $savedPassword
}

$deadline = [DateTime]::UtcNow.AddSeconds(20)
while ([DateTime]::UtcNow -lt $deadline) {
    if ($process.HasExited) {
        throw "Dashboard exited during startup. Check whether port $Port is occupied."
    }
    $response = Get-DashboardResponse
    if ($response) {
        Assert-DashboardResponse $response
        Write-Host "Dashboard ready: $url (PID $($process.Id))"
        if (-not $NoOpen) { Start-Process $url }
        return
    }
    Start-Sleep -Milliseconds 200
}
if (-not $process.HasExited) { Stop-Process -Id $process.Id -ErrorAction SilentlyContinue }
throw 'Dashboard did not become ready within 20 seconds. Please try again.'

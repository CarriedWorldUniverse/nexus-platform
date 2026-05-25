# install.ps1 — nexus-platform bundle operator entry point (Windows).
#
# Run from the root of an extracted bundle archive. Mirrors install.sh:
#   1. Verify .\bin\nexus.exe exists.
#   2. Pick a data dir (--data-dir param or %LOCALAPPDATA%\nexus default).
#   3. Run 'nexus init' to bootstrap the substrate + mint admin token.
#   4. Start 'nexus serve' as a background job.
#   5. Probe /health until 200 or 30s timeout.
#   6. Print summary (URL, token, MCP config path, next steps).
#   7. Block in the foreground; Ctrl+C cleanly stops the broker.
#
# Same privacy posture as install.sh: no internet required after
# archive download, no telemetry, all state under the data dir.

param(
    [string]$DataDir = "",
    [string]$Addr = ":7888",
    [switch]$Help
)

$ErrorActionPreference = "Stop"

# ── Help ────────────────────────────────────────────────────────────
if ($Help) {
    Write-Host @"
nexus-platform install — bring up a working nexus on this machine.

Usage: .\install.ps1 [-DataDir <path>] [-Addr <addr>]

Options:
  -DataDir <path>   directory for nexus state (default: `$env:LOCALAPPDATA\nexus)
  -Addr <addr>      broker listen address (default: :7888)
  -Help             show this help

After install:
  - Open the dashboard URL printed below
  - Log in using the admin token printed below
  - Set provider credentials via 'nexus credential set …'
  - Drop the sample.mcp.json into a Claude Code project's .mcp.json

Press Ctrl+C to stop the broker. State persists under the data dir;
re-running install.ps1 against an existing data dir is idempotent.
"@
    exit 0
}

# ── Locate the bundle ──────────────────────────────────────────────
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$BinDir = Join-Path $ScriptDir "bin"
$NexusExe = Join-Path $BinDir "nexus.exe"

if (-not (Test-Path $NexusExe)) {
    Write-Host "X $NexusExe not found" -ForegroundColor Red
    Write-Host "  Are you running install.ps1 from inside the extracted bundle archive?" -ForegroundColor Red
    exit 1
}

# ── Resolve data dir ────────────────────────────────────────────────
if ([string]::IsNullOrEmpty($DataDir)) {
    $DataDir = Join-Path $env:LOCALAPPDATA "nexus"
}
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
$DataDir = (Resolve-Path $DataDir).Path

# Compute dashboard URL from addr.
if ($Addr.StartsWith(":")) {
    $NexusHost = "localhost"
    $Port = $Addr.TrimStart(":")
} else {
    $parts = $Addr.Split(":")
    $NexusHost = $parts[0]
    $Port = $parts[1]
}
$DashboardUrl = "https://${NexusHost}:${Port}/"
$HealthUrl = "https://${NexusHost}:${Port}/health"

# ── Init the substrate ─────────────────────────────────────────────
Write-Host "> Bootstrapping nexus state at $DataDir"
$initOutput = & $NexusExe init --data-dir $DataDir --quiet 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "X nexus init failed:" -ForegroundColor Red
    Write-Host $initOutput -ForegroundColor Red
    exit 1
}
$AdminToken = ($initOutput | Out-String).Trim()
Write-Host "+ Substrate ready" -ForegroundColor Green

# ── Start the broker ───────────────────────────────────────────────
# nexus.exe still requires NEXUS_TOKEN as a hard-fail legacy
# shared-bearer var (cmd/nexus/main.go:152). The operator admin token
# minted by 'nexus init' serves both roles — same identity resolves
# from both the env var and the TokenStore reconciled in broker.db.
$env:NEXUS_TOKEN = $AdminToken
Write-Host "> Starting broker on $Addr..."
$BrokerLog = Join-Path $DataDir "broker.log"
$BrokerProcess = Start-Process -FilePath $NexusExe `
    -ArgumentList "--data-dir", $DataDir, "--addr", $Addr `
    -RedirectStandardOutput $BrokerLog `
    -RedirectStandardError "${BrokerLog}.err" `
    -PassThru -NoNewWindow

# Cleanup on exit / Ctrl+C.
$cleanup = {
    Write-Host ""
    Write-Host "> Shutting down broker (pid $($BrokerProcess.Id))..."
    if (-not $BrokerProcess.HasExited) {
        Stop-Process -Id $BrokerProcess.Id -Force -ErrorAction SilentlyContinue
        $BrokerProcess.WaitForExit(5000) | Out-Null
    }
    Write-Host "+ Stopped" -ForegroundColor Green
}
Register-EngineEvent PowerShell.Exiting -Action $cleanup | Out-Null

# ── Probe /health (30s timeout) ────────────────────────────────────
Write-Host "> Waiting for broker to be ready (probing $HealthUrl)..."
# Allow self-signed certs (broker uses one by default).
$origCertPolicy = [System.Net.ServicePointManager]::ServerCertificateValidationCallback
[System.Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
$deadline = (Get-Date).AddSeconds(30)
$ready = $false
while ((Get-Date) -lt $deadline) {
    try {
        $resp = Invoke-WebRequest -Uri $HealthUrl -TimeoutSec 1 -UseBasicParsing -ErrorAction Stop
        if ($resp.StatusCode -eq 200) {
            $ready = $true
            break
        }
    } catch {
        # Connection refused / cert issues / server not ready yet — try again.
    }
    Start-Sleep -Milliseconds 500
}
[System.Net.ServicePointManager]::ServerCertificateValidationCallback = $origCertPolicy

if (-not $ready) {
    Write-Host "X Broker did not become ready within 30s — see ${BrokerLog}:" -ForegroundColor Red
    Write-Host "-- last 30 lines --"
    Get-Content $BrokerLog -Tail 30 -ErrorAction SilentlyContinue
    & $cleanup
    exit 1
}
Write-Host "+ Broker ready" -ForegroundColor Green

# ── Print summary ──────────────────────────────────────────────────
$MCPPath = Join-Path $DataDir "sample.mcp.json"
Write-Host @"

+- nexus is live ----------------------------------------------+
| Dashboard:    $DashboardUrl
| Data dir:     $DataDir
| MCP template: $MCPPath
| Broker log:   $BrokerLog
+--------------------------------------------------------------+

+- Admin token (Bearer-prefix for Authorization header) -------+
| $AdminToken
+--------------------------------------------------------------+

Next steps:
  1. Set provider credentials:
     $NexusExe credential set anthropic   # see 'nexus credential --help'
  2. Open the dashboard URL above; log in with the admin token
  3. Edit $MCPPath into your Claude Code project's .mcp.json
     to wire the nexus MCPs

Press Ctrl+C to stop the broker.
"@

# ── Block in the foreground ────────────────────────────────────────
$BrokerProcess.WaitForExit()

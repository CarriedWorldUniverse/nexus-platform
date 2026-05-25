#!/usr/bin/env bash
# install.sh — nexus-platform bundle operator entry point (POSIX).
#
# Run from the root of an extracted bundle archive. Steps:
#   1. Verify ./bin/nexus exists + is executable.
#   2. Pick a data dir (--data-dir flag or ~/.nexus default).
#   3. Run `nexus init` to bootstrap the substrate + mint admin token.
#   4. Start `nexus serve` in the background.
#   5. Probe /health until 200 or 30s timeout.
#   6. Print summary (URL, token, MCP config path, next steps).
#   7. Block in the foreground; Ctrl+C cleanly stops the broker.
#
# Designed for the personal-use-on-work-machine case: no internet
# required after archive download, no telemetry, no auto-update,
# everything stays under the chosen data dir.

set -euo pipefail

# ── Locate the bundle ──────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"

if [[ ! -x "$BIN_DIR/nexus" ]]; then
  echo "✗ $BIN_DIR/nexus not found or not executable" >&2
  echo "  Are you running install.sh from inside the extracted bundle archive?" >&2
  exit 1
fi

# ── Parse args ─────────────────────────────────────────────────────
DATA_DIR=""
ADDR=":7888"
SHOW_HELP=0

print_help() {
  cat <<EOF
nexus-platform install — bring up a working nexus on this machine.

Usage: ./install.sh [--data-dir <path>] [--addr <addr>]

Options:
  --data-dir <path>   directory for nexus state (default: \$HOME/.nexus)
  --addr <addr>       broker listen address (default: :7888)
  --help              show this help

After install:
  • Open the dashboard URL printed below
  • Log in using the admin token printed below
  • Set provider credentials via 'nexus credential set …'
  • Drop the sample.mcp.json into a Claude Code project's .mcp.json

Press Ctrl+C to stop the broker. State persists under the data dir;
re-running install.sh against an existing data dir is idempotent.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir) DATA_DIR="$2"; shift 2;;
    --addr)     ADDR="$2";     shift 2;;
    --help|-h)  SHOW_HELP=1;   shift;;
    *) echo "✗ unknown flag: $1" >&2; print_help; exit 2;;
  esac
done

if [[ $SHOW_HELP -eq 1 ]]; then
  print_help
  exit 0
fi

if [[ -z "$DATA_DIR" ]]; then
  DATA_DIR="${HOME}/.nexus"
fi

# Resolve to absolute path so output is unambiguous.
mkdir -p "$DATA_DIR"
DATA_DIR="$(cd "$DATA_DIR" && pwd)"

# Compute dashboard URL from addr. Strip leading colon, fall back to
# explicit host if user passed e.g. "127.0.0.1:9999".
ADDR_NO_HOST="${ADDR#*:}"
if [[ "$ADDR" == ":"* ]]; then
  HOST="localhost"
  PORT="$ADDR_NO_HOST"
else
  HOST="${ADDR%:*}"
  PORT="$ADDR_NO_HOST"
fi
DASHBOARD_URL="https://$HOST:$PORT/"
HEALTH_URL="https://$HOST:$PORT/health"

# ── Init the substrate ─────────────────────────────────────────────
echo "▸ Bootstrapping nexus state at $DATA_DIR"
if ! ADMIN_TOKEN="$("$BIN_DIR/nexus" init --data-dir "$DATA_DIR" --quiet 2>&1)"; then
  echo "✗ nexus init failed:" >&2
  echo "$ADMIN_TOKEN" >&2
  exit 1
fi

# nexus identity init populates the application-layer identity row
# that the broker requires at startup. Idempotent: already-initialised
# returns exit 1 with a clear "already initialised" message — we
# tolerate that case since re-running install.sh is supposed to work.
echo "▸ Initialising nexus application identity"
if ! IDENTITY_OUT="$("$BIN_DIR/nexus" identity init --data-dir "$DATA_DIR" 2>&1)"; then
  if echo "$IDENTITY_OUT" | grep -q "already initialised"; then
    echo "  (identity already initialised; using existing row)"
  else
    echo "✗ nexus identity init failed:" >&2
    echo "$IDENTITY_OUT" >&2
    exit 1
  fi
fi

# nexus cert init generates a self-signed loopback TLS cert. The
# broker requires --tls-cert + --tls-key (no plaintext HTTP path).
# Idempotent at the script level via the file-exists check below.
TLS_DIR="$DATA_DIR/tls"
TLS_CERT="$TLS_DIR/server.crt"
TLS_KEY="$TLS_DIR/server.key"
if [[ ! -f "$TLS_CERT" || ! -f "$TLS_KEY" ]]; then
  echo "▸ Generating self-signed TLS cert in $TLS_DIR"
  if ! CERT_OUT="$("$BIN_DIR/nexus" cert init --out "$TLS_DIR" 2>&1)"; then
    echo "✗ nexus cert init failed:" >&2
    echo "$CERT_OUT" >&2
    exit 1
  fi
else
  echo "  (TLS cert already present at $TLS_CERT)"
fi

echo "✓ Substrate ready"

# ── Start the broker ────────────────────────────────────────────────
# nexus.exe still requires NEXUS_TOKEN as a hard-fail legacy
# shared-bearer var (cmd/nexus/main.go:152). The operator admin token
# minted by `nexus init` serves both roles — same identity resolves
# from both the env var and the TokenStore reconciled in broker.db.
export NEXUS_TOKEN="$ADMIN_TOKEN"
echo "▸ Starting broker on $ADDR..."
"$BIN_DIR/nexus" --data-dir "$DATA_DIR" --addr "$ADDR" \
  --tls-cert "$TLS_CERT" --tls-key "$TLS_KEY" \
  >"$DATA_DIR/broker.log" 2>&1 &
BROKER_PID=$!

cleanup() {
  echo
  echo "▸ Shutting down broker (pid $BROKER_PID)..."
  if kill -0 "$BROKER_PID" 2>/dev/null; then
    kill -TERM "$BROKER_PID" 2>/dev/null || true
    # Wait up to 5s for graceful shutdown before SIGKILL.
    for _ in $(seq 1 50); do
      kill -0 "$BROKER_PID" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "$BROKER_PID" 2>/dev/null || true
  fi
  echo "✓ Stopped"
}
trap cleanup INT TERM EXIT

# ── Probe /health (30s timeout) ─────────────────────────────────────
echo "▸ Waiting for broker to be ready (probing $HEALTH_URL)..."
DEADLINE=$(( $(date +%s) + 30 ))
while true; do
  if curl -fsSk --max-time 1 "$HEALTH_URL" >/dev/null 2>&1; then
    echo "✓ Broker ready"
    break
  fi
  if [[ $(date +%s) -ge $DEADLINE ]]; then
    echo "✗ Broker did not become ready within 30s — see $DATA_DIR/broker.log:" >&2
    echo "── last 30 lines ──" >&2
    tail -30 "$DATA_DIR/broker.log" >&2 || true
    exit 1
  fi
  sleep 0.5
done

# ── Print summary ───────────────────────────────────────────────────
cat <<EOF

╭─ nexus is live ──────────────────────────────────────────────────╮
│ Dashboard:    $DASHBOARD_URL
│ Data dir:     $DATA_DIR
│ MCP template: $DATA_DIR/sample.mcp.json
│ Broker log:   $DATA_DIR/broker.log
╰──────────────────────────────────────────────────────────────────╯

╭─ Admin token (Bearer-prefix for Authorization header) ──────────╮
│ $ADMIN_TOKEN
╰──────────────────────────────────────────────────────────────────╯

Next steps:
  1. Set provider credentials:
     $BIN_DIR/nexus credential set anthropic   # see 'nexus credential --help'
  2. Open the dashboard URL above; log in with the admin token
  3. Edit $DATA_DIR/sample.mcp.json into your Claude Code project's .mcp.json
     to wire the nexus MCPs

Press Ctrl+C to stop the broker.
EOF

# ── Block in the foreground ─────────────────────────────────────────
# `wait` returns the broker's exit code on natural exit; the trap
# handles Ctrl+C / SIGTERM cleanup.
wait "$BROKER_PID" || true

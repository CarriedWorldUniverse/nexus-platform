#!/usr/bin/env bash
# install.sh — nexus-platform bundle operator entry point (POSIX).
#
# This is the placeholder scaffold from NEX-282. The real flow lands in
# NEX-286 (operator-facing install + health-probe + Frame smoke). For
# now this script just prints a not-yet-implemented banner so anyone
# running it from a scaffold-era checkout knows the bundle isn't ready.
#
# Future shape (per NEX-286 spec):
#   1. Detect bundle layout (./bin/ alongside this script)
#   2. Pick default data dir (~/.nexus, overridable via --data-dir)
#   3. Run ./bin/nexus init --data-dir <path> --quiet (NEX-275 in nexus repo)
#   4. Capture admin token + sample.mcp.json path
#   5. Start ./bin/nexus serve --data-dir <path> (foreground)
#   6. Probe /healthz until 200 or 30s timeout
#   7. Spawn agentfunnel for the Frame aspect (keel.keyfile.json)
#   8. Probe roster.list until keel appears
#   9. Print summary (URL, token, MCP config path, next steps)
#  10. Block until ^C; clean shutdown of broker + agentfunnel

set -euo pipefail

cat <<EOF
nexus-platform install.sh — scaffold placeholder.

Real install flow lands in NEX-286. See README.md + NEX-281 epic for
the full design.

If you're running this from a scaffold-era checkout, the bundle isn't
assembled yet. Check the project status at:

  https://carriedworlduniverse.atlassian.net/browse/NEX-281

EOF

exit 0

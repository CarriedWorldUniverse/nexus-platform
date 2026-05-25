# nexus-platform

Umbrella distribution repo for the [nexus](https://github.com/CarriedWorldUniverse/nexus)
deployable bundle.

This repo does NOT contain nexus source code. It pins specific versions of each
component (nexus, ledger, bridle, plus the MCPs / agentfunnel / agora that ship
from the nexus repo), runs cross-component integration tests against those
pins, and assembles single-archive bundles per OS/arch that operators (or
AI agents on locked-down machines) can install in one step.

## Layout

```
nexus-platform/
├── bundle.toml              # pinned component versions — the source of truth
├── Makefile                 # fetch + assemble + test + sign
├── install.sh / install.ps1 # operator entry point (packed into each archive)
├── README.md                # this file
├── LICENSE                  # Apache 2.0
├── templates/
│   ├── sample.mcp.json      # MCP wiring template emitted by install
│   └── bundle-README.md     # the README shipped INSIDE each archive
├── tests/
│   └── integration/         # cross-component bundle smoke test (NEX-284)
└── .github/workflows/
    ├── ci.yml               # PR-time integration test (NEX-288)
    └── bundle.yml           # on-tag bundle assemble + release (NEX-288)
```

## Design

Each component repo (nexus, ledger, bridle, ...) keeps its own independent
versioning + release cadence. This avoids the code-churn cost of a monorepo
while still giving operators a coherent "which versions work together" answer
via `bundle.toml`.

When a new bundle is needed:
1. Update `bundle.toml` with the desired component versions
2. PR — CI runs integration tests against the new pins
3. On merge + `bundle-v*` tag, the release workflow:
   a. Fetches binaries from each component's GitHub release (no source)
   b. Runs the full integration smoke against the fetched binaries
   c. Assembles single archive per OS/arch
   d. Publishes the GitHub release with archives + checksums + changelog

Operators download one archive, run `./install.sh`, get a working network.

## Status

Initial scaffold (NEX-282). Implementation in progress per
[NEX-281 epic](https://carriedworlduniverse.atlassian.net/browse/NEX-281):

- [ ] NEX-283 — bundle.toml schema + `bundlectl` validation tooling
- [ ] NEX-284 — cross-component integration test harness
- [ ] NEX-285 — bundle assembly (fetch + assemble + sign placeholder)
- [ ] NEX-286 — install.sh / install.ps1
- [ ] NEX-287 — bundle README + first-time-operator guide
- [ ] NEX-288 — CI workflows (PR + on-tag)

## License

Apache 2.0 — same as the underlying nexus components. Permissive use for
personal + commercial deployments. See [LICENSE](LICENSE).

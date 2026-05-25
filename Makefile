# nexus-platform Makefile — placeholder targets for the scaffold.
#
# Real implementations land in subsequent NEX-281 children:
#   - fetch     → NEX-285 (download pinned binaries from component releases)
#   - assemble  → NEX-285 (assemble per-OS/arch archives)
#   - sign      → NEX-285 (placeholder; real signing pending cert decision)
#   - test      → NEX-284 (cross-component integration smoke)

.PHONY: validate validate-online resolve diff fetch assemble sign test test-go clean help

help:
	@echo "nexus-platform — bundle assembly + integration testing"
	@echo ""
	@echo "Manifest tooling (NEX-283, bundlectl):"
	@echo "  make validate        offline schema check on bundle.toml"
	@echo "  make validate-online schema check + probe GitHub for each pinned tag"
	@echo "  make resolve         emit resolved JSON (download URLs per asset)"
	@echo "  make diff OLD=...    diff bundle.toml against a previous version"
	@echo ""
	@echo "Bundle assembly (NEX-285):"
	@echo "  make fetch           fetch pinned component binaries [not yet implemented]"
	@echo "  make assemble        assemble per-OS/arch bundle archives [not yet implemented]"
	@echo "  make sign            sign archives (placeholder; no cert yet)"
	@echo ""
	@echo "Integration test (NEX-284):"
	@echo "  make test            cross-component bundle smoke [not yet implemented]"
	@echo ""
	@echo "Misc:"
	@echo "  make test-go         run bundlectl's own Go tests"
	@echo "  make clean           remove bin/ and dist/"

validate:
	@go run ./cmd/bundlectl validate bundle.toml

validate-online:
	@go run ./cmd/bundlectl validate --online bundle.toml

resolve:
	@go run ./cmd/bundlectl resolve bundle.toml

diff:
	@if [ -z "$(OLD)" ]; then echo "usage: make diff OLD=<path-to-old-bundle.toml>" >&2; exit 1; fi
	@go run ./cmd/bundlectl diff $(OLD) bundle.toml

test-go:
	@go test ./...

fetch:
	@go run ./cmd/bundlectl fetch bundle.toml --target-dir bin

assemble:
	@go run ./cmd/bundlectl assemble bundle.toml --bin-dir bin --dist-dir dist

sign:
	@echo "sign: no signing cert configured; skipping" >&2
	@exit 0

test:
	@echo "test: not yet implemented (NEX-284)" >&2
	@exit 1

clean:
	rm -rf bin/ dist/

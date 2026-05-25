# nexus-platform Makefile — placeholder targets for the scaffold.
#
# Real implementations land in subsequent NEX-281 children:
#   - fetch     → NEX-285 (download pinned binaries from component releases)
#   - assemble  → NEX-285 (assemble per-OS/arch archives)
#   - sign      → NEX-285 (placeholder; real signing pending cert decision)
#   - test      → NEX-284 (cross-component integration smoke)

.PHONY: fetch assemble sign test clean help

help:
	@echo "nexus-platform — bundle assembly + integration testing"
	@echo ""
	@echo "Targets:"
	@echo "  make fetch     fetch pinned component binaries (NEX-285)"
	@echo "  make assemble  assemble per-OS/arch bundle archives (NEX-285)"
	@echo "  make sign      sign archives (placeholder until cert lands)"
	@echo "  make test      run cross-component integration smoke (NEX-284)"
	@echo "  make clean     remove bin/ and dist/"
	@echo ""
	@echo "All targets currently print 'not yet implemented' — this scaffold"
	@echo "only proves the repo layout. See NEX-281 cluster for impl tickets."

fetch:
	@echo "fetch: not yet implemented (NEX-285)" >&2
	@exit 1

assemble:
	@echo "assemble: not yet implemented (NEX-285)" >&2
	@exit 1

sign:
	@echo "sign: no signing cert configured; skipping" >&2
	@exit 0

test:
	@echo "test: not yet implemented (NEX-284)" >&2
	@exit 1

clean:
	rm -rf bin/ dist/

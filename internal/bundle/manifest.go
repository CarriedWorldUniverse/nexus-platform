// Package bundle parses + validates the nexus-platform bundle.toml
// manifest. bundle.toml is the source of truth for "which versions of
// each component go together"; this package is the only place that
// knows the schema, so the rest of the codebase reads versions through
// these structs rather than poking the TOML directly.
package bundle

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Manifest is the on-disk shape of bundle.toml. Unknown fields cause
// a decode error so typos surface loudly rather than getting silently
// ignored — the operator's intent for "which version ships" is too
// load-bearing to allow drift.
type Manifest struct {
	Bundle     BundleMeta        `toml:"bundle"`
	Components map[string]string `toml:"components"`
	Addons     map[string]string `toml:"addons,omitempty"`
}

// BundleMeta captures the bundle-level metadata.
type BundleMeta struct {
	Version  string `toml:"version"`  // semver of the bundle itself
	Released string `toml:"released"` // ISO date (YYYY-MM-DD)
}

// Load parses bundle.toml at path and returns the Manifest. Returns
// any TOML decode error verbatim — the message includes the line
// number so operator fixes are fast.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bundle: read %s: %w", path, err)
	}
	var m Manifest
	meta, err := toml.Decode(string(data), &m)
	if err != nil {
		return nil, fmt.Errorf("bundle: decode %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("bundle: unknown keys in %s: %s", path, strings.Join(keys, ", "))
	}
	return &m, nil
}

// Validate runs structural checks on the manifest. Network-side
// checks (does this tag actually exist on GitHub?) live in resolve.go
// so Validate stays offline-runnable for the local-dev fast path.
func (m *Manifest) Validate() error {
	var errs []string
	if m.Bundle.Version == "" {
		errs = append(errs, "[bundle].version is required")
	} else if !isSemver(m.Bundle.Version) {
		errs = append(errs, fmt.Sprintf("[bundle].version %q is not a valid semver (want e.g. 1.2.3 or v1.2.3)", m.Bundle.Version))
	}
	if m.Bundle.Released == "" {
		errs = append(errs, "[bundle].released is required")
	} else if _, perr := time.Parse("2006-01-02", m.Bundle.Released); perr != nil {
		errs = append(errs, fmt.Sprintf("[bundle].released %q is not YYYY-MM-DD", m.Bundle.Released))
	}
	if len(m.Components) == 0 {
		errs = append(errs, "[components] table is empty (need at least one component pinned)")
	}
	for name, ver := range m.Components {
		if !isComponentVersion(ver) {
			errs = append(errs, fmt.Sprintf("[components].%s = %q does not look like a tag or pseudo-version", name, ver))
		}
	}
	for name, ver := range m.Addons {
		// Addon "?" suffix marks unreleased; allow.
		if strings.HasSuffix(ver, "?") {
			continue
		}
		if !isComponentVersion(ver) {
			errs = append(errs, fmt.Sprintf("[addons].%s = %q does not look like a tag or pseudo-version", name, ver))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n  - "))
	}
	return nil
}

// semverPattern accepts "v1.2.3", "1.2.3", and pre-release suffixes
// like "v1.2.3-rc.1". Pragmatic — full semver regex is gnarlier than
// we need; the bundle's own version is operator-set anyway.
var semverPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[a-zA-Z0-9.\-+]+)?$`)

func isSemver(s string) bool {
	return semverPattern.MatchString(s)
}

// pseudoVersionPattern matches Go module pseudo-versions like
// "v0.1.2-0.20260523081828-4e1548b62f7c" (bridle's current shape).
// Pinning to a pseudo-version is legitimate when a component hasn't
// tagged a release that captures the wanted commit yet.
var pseudoVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+-(0\.)?\d{14}-[a-f0-9]{12}$`)

func isComponentVersion(s string) bool {
	return isSemver(s) || pseudoVersionPattern.MatchString(s)
}

package bundle

import (
	"fmt"
	"sort"
	"strings"
)

// Diff describes the changes from one manifest to another. Designed to
// be rendered into a human-readable changelog entry: each field is a
// list of strings ready for "- " prefixing.
type Diff struct {
	BundleVersionChange string   // empty if unchanged; "0.1.0 → 0.2.0" otherwise
	ReleasedChange      string   // empty if unchanged
	ComponentsAdded     []string // "openai-mcp added: v0.1.0"
	ComponentsRemoved   []string // "old-thing removed (was v0.0.3)"
	ComponentsBumped    []string // "nexus: v0.2.0 → v0.3.0"
	AddonsAdded         []string
	AddonsRemoved       []string
	AddonsChanged       []string
}

// IsEmpty reports whether the diff has zero changes — useful for the
// CLI to print "no changes" cleanly rather than a header with no body.
func (d Diff) IsEmpty() bool {
	return d.BundleVersionChange == "" && d.ReleasedChange == "" &&
		len(d.ComponentsAdded) == 0 && len(d.ComponentsRemoved) == 0 && len(d.ComponentsBumped) == 0 &&
		len(d.AddonsAdded) == 0 && len(d.AddonsRemoved) == 0 && len(d.AddonsChanged) == 0
}

// DiffManifests compares two manifests and returns a Diff describing
// the transition from old → new. Both arguments must be non-nil.
func DiffManifests(old, new *Manifest) Diff {
	d := Diff{}
	if old.Bundle.Version != new.Bundle.Version {
		d.BundleVersionChange = fmt.Sprintf("%s → %s", old.Bundle.Version, new.Bundle.Version)
	}
	if old.Bundle.Released != new.Bundle.Released {
		d.ReleasedChange = fmt.Sprintf("%s → %s", old.Bundle.Released, new.Bundle.Released)
	}
	d.ComponentsAdded, d.ComponentsRemoved, d.ComponentsBumped = diffVersionMap(old.Components, new.Components)
	d.AddonsAdded, d.AddonsRemoved, d.AddonsChanged = diffVersionMap(old.Addons, new.Addons)
	return d
}

// diffVersionMap is the shared comparator. Returns sorted slices so
// the changelog is deterministic regardless of map iteration order.
func diffVersionMap(old, new map[string]string) (added, removed, bumped []string) {
	for k, nv := range new {
		ov, ok := old[k]
		if !ok {
			added = append(added, fmt.Sprintf("%s added: %s", k, nv))
			continue
		}
		if ov != nv {
			bumped = append(bumped, fmt.Sprintf("%s: %s → %s", k, ov, nv))
		}
	}
	for k, ov := range old {
		if _, ok := new[k]; !ok {
			removed = append(removed, fmt.Sprintf("%s removed (was %s)", k, ov))
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(bumped)
	return
}

// Render returns the diff as a markdown-friendly block ready to drop
// into a GitHub release body or PR description.
func (d Diff) Render() string {
	if d.IsEmpty() {
		return "(no changes)"
	}
	var sb strings.Builder
	if d.BundleVersionChange != "" {
		fmt.Fprintf(&sb, "**Bundle version:** %s\n", d.BundleVersionChange)
	}
	if d.ReleasedChange != "" {
		fmt.Fprintf(&sb, "**Released:** %s\n", d.ReleasedChange)
	}
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&sb, "\n**%s:**\n", title)
		for _, item := range items {
			fmt.Fprintf(&sb, "- %s\n", item)
		}
	}
	section("Components bumped", d.ComponentsBumped)
	section("Components added", d.ComponentsAdded)
	section("Components removed", d.ComponentsRemoved)
	section("Addons changed", d.AddonsChanged)
	section("Addons added", d.AddonsAdded)
	section("Addons removed", d.AddonsRemoved)
	return strings.TrimRight(sb.String(), "\n")
}

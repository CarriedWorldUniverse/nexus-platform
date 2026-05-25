package bundle

import (
	"strings"
	"testing"
)

func TestDiff_BumpAndAdd(t *testing.T) {
	old := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0", "ledger": "v0.1.1"},
	}
	new := &Manifest{
		Bundle:     BundleMeta{Version: "0.2.0", Released: "2026-05-26"},
		Components: map[string]string{"nexus": "v0.2.0", "ledger": "v0.1.2", "bridle": "v0.1.2"},
	}
	d := DiffManifests(old, new)
	if d.BundleVersionChange != "0.1.0 → 0.2.0" {
		t.Errorf("bundle version: %q", d.BundleVersionChange)
	}
	if d.ReleasedChange != "2026-05-25 → 2026-05-26" {
		t.Errorf("released: %q", d.ReleasedChange)
	}
	if len(d.ComponentsBumped) != 1 || d.ComponentsBumped[0] != "ledger: v0.1.1 → v0.1.2" {
		t.Errorf("bumped: %v", d.ComponentsBumped)
	}
	if len(d.ComponentsAdded) != 1 || d.ComponentsAdded[0] != "bridle added: v0.1.2" {
		t.Errorf("added: %v", d.ComponentsAdded)
	}
}

func TestDiff_Remove(t *testing.T) {
	old := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0", "obsolete": "v0.0.1"},
	}
	new := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.1", Released: "2026-05-26"},
		Components: map[string]string{"nexus": "v0.2.0"},
	}
	d := DiffManifests(old, new)
	if len(d.ComponentsRemoved) != 1 || d.ComponentsRemoved[0] != "obsolete removed (was v0.0.1)" {
		t.Errorf("removed: %v", d.ComponentsRemoved)
	}
}

func TestDiff_Empty(t *testing.T) {
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0"},
	}
	d := DiffManifests(m, m)
	if !d.IsEmpty() {
		t.Errorf("identical manifests should produce empty diff: %+v", d)
	}
	if d.Render() != "(no changes)" {
		t.Errorf("render of empty: %q", d.Render())
	}
}

func TestDiff_Render(t *testing.T) {
	old := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0"},
	}
	new := &Manifest{
		Bundle:     BundleMeta{Version: "0.2.0", Released: "2026-05-26"},
		Components: map[string]string{"nexus": "v0.3.0", "ledger": "v0.1.2"},
	}
	out := DiffManifests(old, new).Render()
	for _, want := range []string{
		"**Bundle version:** 0.1.0 → 0.2.0",
		"**Released:** 2026-05-25 → 2026-05-26",
		"**Components bumped:**",
		"- nexus: v0.2.0 → v0.3.0",
		"**Components added:**",
		"- ledger added: v0.1.2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n---\n%s", want, out)
		}
	}
}

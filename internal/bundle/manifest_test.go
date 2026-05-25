package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_ValidManifest(t *testing.T) {
	body := `
[bundle]
version  = "0.1.0"
released = "2026-05-25"

[components]
nexus  = "v0.2.0"
ledger = "v0.1.2"

[addons]
interchange = "v0.0.0?"
`
	m, err := Load(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if m.Bundle.Version != "0.1.0" || m.Bundle.Released != "2026-05-25" {
		t.Errorf("bundle meta: %+v", m.Bundle)
	}
	if m.Components["nexus"] != "v0.2.0" {
		t.Errorf("nexus version: %q", m.Components["nexus"])
	}
	if m.Addons["interchange"] != "v0.0.0?" {
		t.Errorf("interchange addon: %q", m.Addons["interchange"])
	}
}

func TestLoad_RejectsUnknownKeys(t *testing.T) {
	body := `
[bundle]
version  = "0.1.0"
released = "2026-05-25"
typo     = "oops"

[components]
nexus = "v0.2.0"
`
	_, err := Load(writeManifest(t, body))
	if err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("expected unknown-keys rejection, got %v", err)
	}
	if !strings.Contains(err.Error(), "bundle.typo") {
		t.Errorf("error didn't name the bad key: %v", err)
	}
}

func TestValidate_AcceptsPseudoVersions(t *testing.T) {
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"bridle": "v0.1.2-0.20260523081828-4e1548b62f7c"},
	}
	if err := m.Validate(); err != nil {
		t.Errorf("pseudo-version should validate: %v", err)
	}
}

func TestValidate_RejectsMalformedVersion(t *testing.T) {
	m := &Manifest{
		Bundle:     BundleMeta{Version: "not-a-semver", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0"},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "not a valid semver") {
		t.Errorf("expected semver rejection, got %v", err)
	}
}

func TestValidate_RejectsBadDate(t *testing.T) {
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "yesterday"},
		Components: map[string]string{"nexus": "v0.2.0"},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("expected date format rejection, got %v", err)
	}
}

func TestValidate_RejectsEmptyComponents(t *testing.T) {
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "components") {
		t.Errorf("expected empty-components rejection, got %v", err)
	}
}

func TestValidate_AcceptsAddonQuestionMark(t *testing.T) {
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0"},
		Addons:     map[string]string{"interchange": "v0.0.0?"},
	}
	if err := m.Validate(); err != nil {
		t.Errorf("? suffix on addon should pass: %v", err)
	}
}

func TestValidate_AggregatesErrors(t *testing.T) {
	m := &Manifest{
		Bundle:     BundleMeta{Version: "bad", Released: "also-bad"},
		Components: map[string]string{"nexus": "?wat"},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	// All three problems should surface in one go.
	for _, want := range []string{"semver", "YYYY-MM-DD", "tag or pseudo-version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

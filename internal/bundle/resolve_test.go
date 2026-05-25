package bundle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolve_AddonQuestionMarkSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("addon with ? should not hit GitHub; got %s", r.URL.Path)
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), BaseURL: srv.URL}
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{},
		Addons:     map[string]string{"interchange": "v0.0.0?"},
	}
	out, err := r.Resolve(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Addons) != 1 || out.Addons[0].Skipped == "" {
		t.Errorf("addon should be skipped: %+v", out.Addons)
	}
}

func TestResolve_PseudoVersionSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("pseudo-version should not hit GitHub; got %s", r.URL.Path)
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), BaseURL: srv.URL}
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"bridle": "v0.1.2-0.20260523081828-4e1548b62f7c"},
	}
	out, err := r.Resolve(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Components) != 1 || !strings.Contains(out.Components[0].Skipped, "pseudo-version") {
		t.Errorf("pseudo-version should be skipped with explanation: %+v", out.Components)
	}
}

func TestResolve_FetchesRealRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the URL shape mirrors the real GitHub releases-by-tag API.
		if r.URL.Path != "/repos/CarriedWorldUniverse/nexus/releases/tags/v0.2.0" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "nexus_0.2.0_linux_amd64.tar.gz", "browser_download_url": "https://example/dl/a", "size": 12345},
				{"name": "nexus_0.2.0_darwin_arm64.tar.gz", "browser_download_url": "https://example/dl/b", "size": 67890},
			},
		})
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), BaseURL: srv.URL}
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0"},
	}
	out, err := r.Resolve(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Components) != 1 {
		t.Fatalf("components: %+v", out.Components)
	}
	c := out.Components[0]
	if c.Name != "nexus" || c.Tag != "v0.2.0" || len(c.Assets) != 2 {
		t.Errorf("component: %+v", c)
	}
	if c.Assets[0].Name != "nexus_0.2.0_linux_amd64.tar.gz" || c.Assets[0].Size != 12345 {
		t.Errorf("first asset: %+v", c.Assets[0])
	}
}

func TestResolve_404OnMissingRelease(t *testing.T) {
	// Tag genuinely doesn't exist — both releases AND git/refs/tags 404.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), BaseURL: srv.URL}
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v999.0.0"},
	}
	_, err := r.Resolve(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "release not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestResolve_TagExistsWithoutRelease(t *testing.T) {
	// Tag exists in the repo (git/refs/tags/X = 200) but no release
	// has been published (releases/tags/X = 404). Should be marked
	// Skipped rather than failing the whole resolve.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/refs/tags/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ref":"refs/tags/v0.1.0"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), BaseURL: srv.URL}
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"ledger": "v0.1.2"},
	}
	out, err := r.Resolve(context.Background(), m)
	if err != nil {
		t.Fatalf("expected success with Skipped marker, got %v", err)
	}
	if len(out.Components) != 1 || !strings.Contains(out.Components[0].Skipped, "no GitHub release") {
		t.Errorf("expected Skipped marker: %+v", out.Components)
	}
}

func TestResolve_403OnRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), BaseURL: srv.URL}
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"nexus": "v0.2.0"},
	}
	_, err := r.Resolve(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "rate-limited") {
		t.Errorf("expected rate-limit error, got %v", err)
	}
}

func TestResolve_OrderIsDeterministic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": []any{}})
	}))
	defer srv.Close()
	r := &Resolver{HTTP: srv.Client(), BaseURL: srv.URL}
	m := &Manifest{
		Bundle:     BundleMeta{Version: "0.1.0", Released: "2026-05-25"},
		Components: map[string]string{"zebra": "v0.1.0", "alpha": "v0.1.0", "mango": "v0.1.0"},
	}
	out, err := r.Resolve(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "mango", "zebra"}
	for i, c := range out.Components {
		if c.Name != want[i] {
			t.Errorf("component[%d] = %q, want %q", i, c.Name, want[i])
		}
	}
}

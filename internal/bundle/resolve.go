package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Org is the GitHub org all component repos live under. Hardcoded for
// nexus-platform; if a future bundle needs to point at a different
// org, this becomes a per-component field on the manifest.
const Org = "CarriedWorldUniverse"

// ResolvedComponent is one component's GitHub release info — the
// resolved version (tag), the asset list (per-OS/arch binary names +
// download URLs), and a sentinel if validation skipped this component
// (pseudo-versions don't have a tag-based release to query).
type ResolvedComponent struct {
	Name    string           `json:"name"`
	Tag     string           `json:"tag"`
	Skipped string           `json:"skipped,omitempty"` // reason if we couldn't validate
	Assets  []ResolvedAsset  `json:"assets,omitempty"`
}

// ResolvedAsset is one downloadable file from the release.
type ResolvedAsset struct {
	Name string `json:"name"`         // e.g. "nexus_0.2.0_linux_amd64.tar.gz"
	URL  string `json:"download_url"` // direct asset URL
	Size int64  `json:"size_bytes"`
}

// ResolvedManifest is the JSON shape emitted by `bundlectl resolve` —
// downstream consumers (Makefile's `make fetch`) read this rather than
// re-parsing bundle.toml + re-querying GitHub.
type ResolvedManifest struct {
	BundleVersion string              `json:"bundle_version"`
	Released      string              `json:"released"`
	Components    []ResolvedComponent `json:"components"`
	Addons        []ResolvedComponent `json:"addons,omitempty"`
}

// Resolver hits the GitHub releases API to translate the manifest
// pins into concrete asset download URLs. http and token are exposed
// so tests can inject a httptest.Server-backed client + the production
// caller can plumb a GITHUB_TOKEN for higher rate limits.
type Resolver struct {
	HTTP    *http.Client
	Token   string // optional; empty falls back to unauthenticated (low rate limit)
	BaseURL string // override for tests; empty = https://api.github.com
}

// DefaultResolver returns a Resolver wired to api.github.com with a
// reasonable timeout. Token is pulled from GITHUB_TOKEN env so CI runs
// inherit it automatically.
func DefaultResolver() *Resolver {
	return &Resolver{
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		Token:   os.Getenv("GITHUB_TOKEN"),
		BaseURL: "https://api.github.com",
	}
}

// Resolve walks every entry in the manifest and produces a ResolvedManifest
// suitable for `make fetch` consumption. Pseudo-versions and "?"-suffixed
// addons are reported with a Skipped reason rather than failing — the
// validator already vetted their shape; downstream consumers know to
// skip them too.
func (r *Resolver) Resolve(ctx context.Context, m *Manifest) (*ResolvedManifest, error) {
	out := &ResolvedManifest{
		BundleVersion: m.Bundle.Version,
		Released:      m.Bundle.Released,
	}
	for _, name := range sortedKeys(m.Components) {
		rc, err := r.resolveOne(ctx, name, m.Components[name])
		if err != nil {
			return nil, fmt.Errorf("resolve %s@%s: %w", name, m.Components[name], err)
		}
		out.Components = append(out.Components, rc)
	}
	for _, name := range sortedKeys(m.Addons) {
		rc, err := r.resolveOne(ctx, name, m.Addons[name])
		if err != nil {
			return nil, fmt.Errorf("resolve addon %s@%s: %w", name, m.Addons[name], err)
		}
		out.Addons = append(out.Addons, rc)
	}
	return out, nil
}

func (r *Resolver) resolveOne(ctx context.Context, name, version string) (ResolvedComponent, error) {
	rc := ResolvedComponent{Name: name, Tag: version}
	if strings.HasSuffix(version, "?") {
		rc.Skipped = "addon unreleased (? suffix)"
		return rc, nil
	}
	if pseudoVersionPattern.MatchString(version) {
		rc.Skipped = "pseudo-version (no GitHub release tag)"
		return rc, nil
	}
	rel, err := r.fetchRelease(ctx, name, version)
	if err != nil {
		// Non-fatal: tag exists but no release was published. Mark
		// Skipped + carry on so the rest of the manifest resolves.
		// Downstream (Makefile fetch) knows to skip these too.
		if errors.Is(err, ErrTagWithoutRelease) {
			rc.Skipped = "tag exists but no GitHub release published"
			return rc, nil
		}
		return rc, err
	}
	for _, a := range rel.Assets {
		rc.Assets = append(rc.Assets, ResolvedAsset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rc, nil
}

// githubRelease is the slice of the releases-by-tag API response we
// actually use. Decoded into this struct so we don't have to invent a
// dependency on go-github for two fields.
type githubRelease struct {
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// ErrTagWithoutRelease signals "the git tag exists in the repo but no
// GitHub release has been published for it" — common when a component
// repo tags but hasn't wired its release workflow yet. Treated as
// non-fatal by the resolver (returns a Skipped component) but exposed
// so callers can distinguish from "tag genuinely doesn't exist".
var ErrTagWithoutRelease = errors.New("tag exists in repo but no GitHub release published for it")

func (r *Resolver) fetchRelease(ctx context.Context, repo, tag string) (*githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", r.BaseURL, Org, repo, tag)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		// Disambiguate: tag-exists-no-release vs tag-doesn't-exist.
		// Probe the git refs API to see if the tag itself is present.
		exists, perr := r.tagExists(ctx, repo, tag)
		if perr != nil {
			return nil, perr
		}
		if exists {
			return nil, fmt.Errorf("%s@%s: %w", repo, tag, ErrTagWithoutRelease)
		}
		return nil, fmt.Errorf("release not found at %s/%s tag %s", Org, repo, tag)
	case http.StatusForbidden:
		return nil, errors.New("github API forbidden (rate-limited?) — set GITHUB_TOKEN for higher limits")
	default:
		return nil, fmt.Errorf("github GET %s: HTTP %d", url, resp.StatusCode)
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// tagExists probes whether <tag> exists as a git ref in the repo, even
// if no GitHub release object exists for it.
func (r *Resolver) tagExists(ctx context.Context, repo, tag string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/git/refs/tags/%s", r.BaseURL, Org, repo, tag)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return false, fmt.Errorf("github GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("github GET %s: HTTP %d", url, resp.StatusCode)
	}
}

// sortedKeys returns map keys in deterministic order so resolve output
// is stable across runs (matters for diff-of-resolved-manifests).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// stdlib sort is good enough — strings.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

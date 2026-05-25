package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

// Target identifies one platform binaries are produced for. Matches
// goreleaser's OS+arch matrix; values are the lowercased strings the
// asset filenames carry.
type Target struct {
	OS   string // linux | darwin | windows
	Arch string // amd64 | arm64
}

func (t Target) String() string { return t.OS + "-" + t.Arch }

// DefaultTargets enumerates the (OS, arch) pairs the nexus-platform
// bundle ships. Matches `.goreleaser.yml` in the nexus repo. Add new
// rows here when a new target lands upstream.
var DefaultTargets = []Target{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

// assetNamePattern matches the goreleaser-conventional asset name:
// "{binary}_{version}_{os}_{arch}.tar.gz". Version is captured but
// not used by Fetch — version comes from the manifest pin instead.
// Windows binaries get `.exe` suffix inside the archive but the
// archive name still follows the same convention.
var assetNamePattern = regexp.MustCompile(`^(?P<binary>[a-zA-Z0-9._-]+?)_(?P<version>[0-9][^_]*)_(?P<os>linux|darwin|windows)_(?P<arch>amd64|arm64)\.tar\.gz$`)

// parseAssetName extracts (binary, os, arch) from an asset name. Returns
// ok=false for files that aren't matrix-style binaries (e.g.
// checksums.txt, source archives).
func parseAssetName(name string) (binary, os, arch string, ok bool) {
	m := assetNamePattern.FindStringSubmatch(name)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[3], m[4], true
}

// FetchOptions controls Fetch's behaviour. TargetDir is required; the
// rest have sensible defaults.
type FetchOptions struct {
	TargetDir string   // root of the extracted bin/ tree
	Targets   []Target // which OS/arch combos to fetch; default DefaultTargets
	HTTP      *http.Client
	Token     string // GITHUB_TOKEN equivalent; empty falls back to unauthenticated
	Logger    func(string, ...any)
}

// Fetch downloads every binary asset referenced by the resolved
// manifest, verifies SHA256 against the published checksums.txt, and
// extracts each archive to TargetDir/<os>-<arch>/<binary>. Components
// marked Skipped (pseudo-versions, unreleased addons, tag-without-
// release) are skipped — fetch's job is "get what's available", not
// "fail loudly when there's nothing to get."
//
// The function is idempotent: an existing extracted binary with the
// expected SHA256 is left in place + the download skipped. A binary
// whose checksum has changed is overwritten.
func Fetch(ctx context.Context, rm *ResolvedManifest, opts FetchOptions) error {
	if opts.TargetDir == "" {
		return errors.New("fetch: TargetDir required")
	}
	if opts.HTTP == nil {
		opts.HTTP = http.DefaultClient
	}
	if opts.Logger == nil {
		opts.Logger = func(string, ...any) {}
	}
	if len(opts.Targets) == 0 {
		opts.Targets = DefaultTargets
	}
	targetSet := map[Target]bool{}
	for _, t := range opts.Targets {
		targetSet[t] = true
	}

	for _, comp := range rm.Components {
		if comp.Skipped != "" {
			opts.Logger("skipping %s: %s", comp.Name, comp.Skipped)
			continue
		}
		if err := fetchComponent(ctx, comp, targetSet, opts); err != nil {
			return fmt.Errorf("fetch %s: %w", comp.Name, err)
		}
	}
	return nil
}

func fetchComponent(ctx context.Context, comp ResolvedComponent, targets map[Target]bool, opts FetchOptions) error {
	checksums, err := loadChecksums(ctx, comp.Assets, opts)
	if err != nil {
		return fmt.Errorf("load checksums for %s: %w", comp.Name, err)
	}
	if len(checksums) == 0 {
		opts.Logger("%s has no checksums.txt asset; binaries cannot be verified — skipping", comp.Name)
		return nil
	}

	for _, asset := range comp.Assets {
		binary, os, arch, ok := parseAssetName(asset.Name)
		if !ok {
			continue
		}
		t := Target{OS: os, Arch: arch}
		if !targets[t] {
			continue
		}
		want, hasChecksum := checksums[asset.Name]
		if !hasChecksum {
			opts.Logger("%s: no checksum for %s; skipping", comp.Name, asset.Name)
			continue
		}
		destDir := filepath.Join(opts.TargetDir, t.String())
		if err := os_MkdirAll(destDir); err != nil {
			return err
		}
		destBinary := filepath.Join(destDir, binary)
		if existing, err := sha256OfBinary(destBinary); err == nil && existing == want {
			opts.Logger("%s/%s: already present (sha256 match)", t, binary)
			continue
		}
		opts.Logger("%s/%s: downloading %s", t, binary, asset.URL)
		if err := downloadAndExtract(ctx, asset, want, destDir, opts); err != nil {
			return fmt.Errorf("download %s: %w", asset.Name, err)
		}
	}
	return nil
}

// loadChecksums downloads checksums.txt (always one per release in
// goreleaser convention) and parses its `sha256  filename` lines.
func loadChecksums(ctx context.Context, assets []ResolvedAsset, opts FetchOptions) (map[string]string, error) {
	var checksumURL string
	for _, a := range assets {
		if a.Name == "checksums.txt" {
			checksumURL = a.URL
			break
		}
	}
	if checksumURL == "" {
		return nil, nil
	}
	body, err := httpGet(ctx, checksumURL, opts)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		out[fields[1]] = fields[0]
	}
	return out, nil
}

// downloadAndExtract pulls the asset, verifies its SHA256, then
// extracts the tar.gz into destDir, writing a single binary file.
// Goreleaser's per-binary archive convention has exactly one
// executable file inside (the binary itself), so a single-file
// extraction matches the shape.
func downloadAndExtract(ctx context.Context, asset ResolvedAsset, wantSHA, destDir string, opts FetchOptions) error {
	body, err := httpGet(ctx, asset.URL, opts)
	if err != nil {
		return err
	}
	got := sha256.Sum256(body)
	gotHex := hex.EncodeToString(got[:])
	if gotHex != wantSHA {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", gotHex, wantSHA)
	}
	return extractTarGz(body, destDir)
}

func extractTarGz(body []byte, destDir string) error {
	gzr, err := gzip.NewReader(bytesReader(body))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Skip directory entries + metadata files; only extract
		// regular files. goreleaser archives typically contain the
		// binary + LICENSE + README.md; the binary is what matters
		// for bundle assembly. Strip windows .exe suffix here so
		// downstream consumers don't have to special-case.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		// Heuristic: skip docs the bundle doesn't need at runtime.
		if name == "LICENSE" || name == "README" || strings.HasSuffix(name, ".md") {
			continue
		}
		// Keep .exe suffix on windows so the binary stays executable
		// on the target platform. Other OSes get the bare name.
		dest := filepath.Join(destDir, name)
		f, err := os_Create(dest)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if err := os_Chmod(dest, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func httpGet(ctx context.Context, url string, opts FetchOptions) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	resp, err := opts.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func sha256OfBinary(path string) (string, error) {
	f, err := os_Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Thin os-package wrappers — keeps the test surface stubbable if we
// ever need to inject a fake FS. Today they just call through.
var (
	os_MkdirAll = func(dir string) error { return osMkdirAllImpl(dir) }
	os_Create   = func(path string) (io.WriteCloser, error) { return osCreateImpl(path) }
	os_Open     = func(path string) (io.ReadCloser, error) { return osOpenImpl(path) }
	os_Chmod    = func(path string, mode int) error { return osChmodImpl(path, mode) }
)

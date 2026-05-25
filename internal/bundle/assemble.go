package bundle

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AssembleOptions controls Assemble's behaviour. BinDir is the root
// of the per-OS-arch binary tree produced by Fetch; DistDir receives
// the assembled archives + checksums + MANIFEST.txt.
type AssembleOptions struct {
	BinDir   string   // root containing <os>-<arch>/<binary>
	DistDir  string   // output dir for archives + checksums.txt + MANIFEST.txt
	Targets  []Target // which combos to assemble; default DefaultTargets
	Manifest *Manifest
	// Extras are files included verbatim in every bundle archive
	// (install scripts, README, templates, LICENSE, bundle.toml).
	// Path is the source path; ArchivePath is the path inside the
	// archive (e.g. "templates/sample.mcp.json").
	Extras []ExtraFile
	Logger func(string, ...any)
}

// ExtraFile describes one non-binary file to pack into each archive.
type ExtraFile struct {
	Path        string // source on disk
	ArchivePath string // path inside the produced archive
	Executable  bool   // chmod +x on the way in (install.sh etc.)
}

// AssemblyResult is one per-target output bundle, returned so callers
// can construct the GitHub release with the right asset paths.
type AssemblyResult struct {
	Target      Target
	ArchivePath string
	SHA256      string
	SizeBytes   int64
}

// Assemble produces one archive per target by combining the
// pre-fetched binaries with the Extras. Format is tar.gz on
// Unix-shaped targets (linux/darwin) and zip on windows — operators
// expect the platform-native archive shape when receiving a bundle.
//
// Writes <DistDir>/checksums.txt with the SHA256 for each archive,
// and <DistDir>/MANIFEST.txt with a human-readable contents listing
// for audit trail.
func Assemble(opts AssembleOptions) ([]AssemblyResult, error) {
	if opts.BinDir == "" {
		return nil, errors.New("assemble: BinDir required")
	}
	if opts.DistDir == "" {
		return nil, errors.New("assemble: DistDir required")
	}
	if opts.Manifest == nil {
		return nil, errors.New("assemble: Manifest required")
	}
	if len(opts.Targets) == 0 {
		opts.Targets = DefaultTargets
	}
	if opts.Logger == nil {
		opts.Logger = func(string, ...any) {}
	}
	if err := os.MkdirAll(opts.DistDir, 0o755); err != nil {
		return nil, err
	}

	// Validate Extras exist before starting any archive — failing
	// halfway through assembly leaves the operator with a partial
	// dist dir to clean up.
	for _, e := range opts.Extras {
		if _, err := os.Stat(e.Path); err != nil {
			return nil, fmt.Errorf("extras: %s missing: %w", e.Path, err)
		}
	}

	results := make([]AssemblyResult, 0, len(opts.Targets))
	for _, t := range opts.Targets {
		binSrc := filepath.Join(opts.BinDir, t.String())
		binEntries, err := os.ReadDir(binSrc)
		if err != nil {
			opts.Logger("skip %s: %v", t, err)
			continue
		}
		if len(binEntries) == 0 {
			opts.Logger("skip %s: no binaries in %s", t, binSrc)
			continue
		}

		var archivePath string
		switch t.OS {
		case "windows":
			archivePath = filepath.Join(opts.DistDir,
				fmt.Sprintf("nexus-platform-%s-%s-%s.zip", opts.Manifest.Bundle.Version, t.OS, t.Arch))
			if err := writeZip(archivePath, t, binSrc, binEntries, opts.Extras); err != nil {
				return nil, fmt.Errorf("zip %s: %w", t, err)
			}
		default:
			archivePath = filepath.Join(opts.DistDir,
				fmt.Sprintf("nexus-platform-%s-%s-%s.tar.gz", opts.Manifest.Bundle.Version, t.OS, t.Arch))
			if err := writeTarGz(archivePath, t, binSrc, binEntries, opts.Extras); err != nil {
				return nil, fmt.Errorf("tar.gz %s: %w", t, err)
			}
		}

		sum, size, err := sha256OfFile(archivePath)
		if err != nil {
			return nil, fmt.Errorf("sha256 %s: %w", archivePath, err)
		}
		results = append(results, AssemblyResult{
			Target: t, ArchivePath: archivePath, SHA256: sum, SizeBytes: size,
		})
		opts.Logger("assembled %s (%d bytes, %s)", filepath.Base(archivePath), size, sum[:12])
	}

	if err := writeChecksumsFile(opts.DistDir, results); err != nil {
		return nil, fmt.Errorf("write checksums.txt: %w", err)
	}
	if err := writeManifestFile(opts.DistDir, opts.Manifest, results); err != nil {
		return nil, fmt.Errorf("write MANIFEST.txt: %w", err)
	}
	return results, nil
}

func writeTarGz(archivePath string, t Target, binSrc string, binEntries []os.DirEntry, extras []ExtraFile) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// Binaries go under bin/<binary>. Preserve executable bit.
	for _, ent := range binEntries {
		if ent.IsDir() {
			continue
		}
		src := filepath.Join(binSrc, ent.Name())
		if err := addFileToTar(tw, src, "bin/"+ent.Name(), 0o755); err != nil {
			return err
		}
	}
	// Extras go at their declared ArchivePath.
	for _, e := range extras {
		mode := 0o644
		if e.Executable {
			mode = 0o755
		}
		if err := addFileToTar(tw, e.Path, e.ArchivePath, mode); err != nil {
			return err
		}
	}
	return nil
}

func writeZip(archivePath string, t Target, binSrc string, binEntries []os.DirEntry, extras []ExtraFile) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	for _, ent := range binEntries {
		if ent.IsDir() {
			continue
		}
		src := filepath.Join(binSrc, ent.Name())
		if err := addFileToZip(zw, src, "bin/"+ent.Name()); err != nil {
			return err
		}
	}
	for _, e := range extras {
		if err := addFileToZip(zw, e.Path, e.ArchivePath); err != nil {
			return err
		}
	}
	return nil
}

func addFileToTar(tw *tar.Writer, src, archivePath string, mode int) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    archivePath,
		Size:    info.Size(),
		Mode:    int64(mode),
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func addFileToZip(zw *zip.Writer, src, archivePath string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = archivePath
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func sha256OfFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), nil
}

// writeChecksumsFile uses the goreleaser-compatible "<sha>  <name>\n"
// format so downstream verification tools (sha256sum -c) accept it as-is.
func writeChecksumsFile(distDir string, results []AssemblyResult) error {
	sort.Slice(results, func(i, j int) bool {
		return filepath.Base(results[i].ArchivePath) < filepath.Base(results[j].ArchivePath)
	})
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "%s  %s\n", r.SHA256, filepath.Base(r.ArchivePath))
	}
	return os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644)
}

// writeManifestFile produces a human-readable listing of what's in
// each archive — bundle version + per-archive contents + checksums.
// Goes into the GitHub release notes as a transparency artifact;
// operators can `cat MANIFEST.txt` post-extract to verify shape.
func writeManifestFile(distDir string, m *Manifest, results []AssemblyResult) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "nexus-platform bundle v%s (released %s)\n", m.Bundle.Version, m.Bundle.Released)
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Components:")
	for _, name := range sortedKeys(m.Components) {
		fmt.Fprintf(&sb, "  %s = %s\n", name, m.Components[name])
	}
	if len(m.Addons) > 0 {
		fmt.Fprintln(&sb)
		fmt.Fprintln(&sb, "Addons (not included in this bundle):")
		for _, name := range sortedKeys(m.Addons) {
			fmt.Fprintf(&sb, "  %s = %s\n", name, m.Addons[name])
		}
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Archives:")
	for _, r := range results {
		fmt.Fprintf(&sb, "  %s\n", filepath.Base(r.ArchivePath))
		fmt.Fprintf(&sb, "    target  = %s\n", r.Target)
		fmt.Fprintf(&sb, "    size    = %d bytes\n", r.SizeBytes)
		fmt.Fprintf(&sb, "    sha256  = %s\n", r.SHA256)
	}
	return os.WriteFile(filepath.Join(distDir, "MANIFEST.txt"), []byte(sb.String()), 0o644)
}

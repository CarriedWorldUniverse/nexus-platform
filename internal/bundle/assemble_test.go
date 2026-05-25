package bundle

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// assembleRig stages a minimal binSrc + extras tree under a temp dir
// and returns the AssembleOptions ready for use.
func assembleRig(t *testing.T) AssembleOptions {
	t.Helper()
	dir := t.TempDir()

	// Stage bin/linux-amd64/foo + bin/windows-amd64/foo.exe
	for _, target := range []struct {
		path string
		name string
	}{
		{"linux-amd64", "foo"},
		{"windows-amd64", "foo.exe"},
	} {
		d := filepath.Join(dir, "bin", target.path)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, target.name), []byte("binary contents"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Stage extras at dir root.
	for _, e := range []struct {
		name string
		body string
	}{
		{"install.sh", "#!/bin/sh\necho hi\n"},
		{"README.md", "# bundle"},
		{"LICENSE", "Apache 2.0"},
	} {
		if err := os.WriteFile(filepath.Join(dir, e.name), []byte(e.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return AssembleOptions{
		BinDir:  filepath.Join(dir, "bin"),
		DistDir: filepath.Join(dir, "dist"),
		Targets: []Target{{"linux", "amd64"}, {"windows", "amd64"}},
		Manifest: &Manifest{
			Bundle:     BundleMeta{Version: "1.2.3", Released: "2026-05-25"},
			Components: map[string]string{"nexus": "v0.2.0"},
		},
		Extras: []ExtraFile{
			{Path: filepath.Join(dir, "install.sh"), ArchivePath: "install.sh", Executable: true},
			{Path: filepath.Join(dir, "README.md"), ArchivePath: "README.md"},
			{Path: filepath.Join(dir, "LICENSE"), ArchivePath: "LICENSE"},
		},
	}
}

func TestAssemble_ProducesTarGzAndZip(t *testing.T) {
	opts := assembleRig(t)
	results, err := Assemble(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	names := []string{}
	for _, r := range results {
		names = append(names, filepath.Base(r.ArchivePath))
	}
	sort.Strings(names)
	want := []string{
		"nexus-platform-1.2.3-linux-amd64.tar.gz",
		"nexus-platform-1.2.3-windows-amd64.zip",
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("name[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestAssemble_TarContents(t *testing.T) {
	opts := assembleRig(t)
	_, err := Assemble(opts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(opts.DistDir, "nexus-platform-1.2.3-linux-amd64.tar.gz")
	f, _ := os.Open(path)
	defer f.Close()
	gz, _ := gzip.NewReader(f)
	defer gz.Close()
	tr := tar.NewReader(gz)
	got := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = true
	}
	for _, want := range []string{"bin/foo", "install.sh", "README.md", "LICENSE"} {
		if !got[want] {
			t.Errorf("tar missing %q (saw %v)", want, got)
		}
	}
}

func TestAssemble_ZipContents(t *testing.T) {
	opts := assembleRig(t)
	_, err := Assemble(opts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(opts.DistDir, "nexus-platform-1.2.3-windows-amd64.zip")
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := map[string]bool{}
	for _, f := range r.File {
		got[f.Name] = true
	}
	for _, want := range []string{"bin/foo.exe", "install.sh", "README.md", "LICENSE"} {
		if !got[want] {
			t.Errorf("zip missing %q (saw %v)", want, got)
		}
	}
}

func TestAssemble_WritesChecksumsAndManifest(t *testing.T) {
	opts := assembleRig(t)
	_, err := Assemble(opts)
	if err != nil {
		t.Fatal(err)
	}
	checksums, err := os.ReadFile(filepath.Join(opts.DistDir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(checksums)), "\n")
	if len(lines) != 2 {
		t.Errorf("checksums.txt: expected 2 lines, got %d", len(lines))
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Errorf("checksum line malformed: %q", line)
			continue
		}
		if len(fields[0]) != 64 {
			t.Errorf("sha256 not 64 chars: %q", fields[0])
		}
	}

	manifest, err := os.ReadFile(filepath.Join(opts.DistDir, "MANIFEST.txt"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(manifest)
	for _, want := range []string{"bundle v1.2.3", "nexus = v0.2.0", "linux-amd64.tar.gz", "windows-amd64.zip"} {
		if !strings.Contains(body, want) {
			t.Errorf("MANIFEST.txt missing %q\n---\n%s", want, body)
		}
	}
}

func TestAssemble_MissingExtraFails(t *testing.T) {
	opts := assembleRig(t)
	opts.Extras = append(opts.Extras, ExtraFile{Path: "/does/not/exist", ArchivePath: "ghost"})
	_, err := Assemble(opts)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected missing-extra error, got %v", err)
	}
}

func TestAssemble_SkipsEmptyTargetDir(t *testing.T) {
	opts := assembleRig(t)
	// Add a target for which we never staged binaries.
	opts.Targets = append(opts.Targets, Target{"darwin", "arm64"})
	results, err := Assemble(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Should still get only 2 results — darwin-arm64 silently skipped.
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d (third target should have been skipped)", len(results))
	}
}

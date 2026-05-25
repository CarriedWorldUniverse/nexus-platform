// bundlectl — validate, diff, and resolve nexus-platform bundle manifests.
//
// Subcommands:
//
//	bundlectl validate <bundle.toml>
//	    Schema-check the manifest + optionally probe GitHub to confirm
//	    each pinned tag exists. Exits 0 on success; non-zero on any
//	    validation error.
//
//	bundlectl diff <old.toml> <new.toml>
//	    Human-readable summary of what changed between two manifests.
//	    Suitable for pasting into a changelog or release body.
//
//	bundlectl resolve <bundle.toml> [-o resolved.json]
//	    Emit a JSON manifest with concrete asset download URLs per
//	    component. Consumed by Makefile's `make fetch` target.
//
// All commands operate on local files — bundle.toml is the source of
// truth and lives in this repo's git history.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CarriedWorldUniverse/nexus-platform/internal/bundle"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "validate":
		err = runValidate(args)
	case "diff":
		err = runDiff(args)
	case "resolve":
		err = runResolve(args)
	case "fetch":
		err = runFetch(args)
	case "assemble":
		err = runAssemble(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "bundlectl: unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bundlectl: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  bundlectl validate <bundle.toml> [--online]")
	fmt.Fprintln(os.Stderr, "  bundlectl diff <old.toml> <new.toml>")
	fmt.Fprintln(os.Stderr, "  bundlectl resolve <bundle.toml> [-o <path>]")
	fmt.Fprintln(os.Stderr, "  bundlectl fetch <bundle.toml> --target-dir <bin>")
	fmt.Fprintln(os.Stderr, "  bundlectl assemble <bundle.toml> --bin-dir <bin> --dist-dir <dist>")
}

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	online := fs.Bool("online", false, "also probe GitHub to confirm each pinned tag exists (skips pseudo-versions + ? addons)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("validate: expected exactly one bundle.toml path")
	}
	m, err := bundle.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	if verr := m.Validate(); verr != nil {
		return fmt.Errorf("schema validation failed:\n  - %s", verr)
	}
	fmt.Printf("✓ schema valid: bundle v%s released %s, %d components, %d addons\n",
		m.Bundle.Version, m.Bundle.Released, len(m.Components), len(m.Addons))
	if !*online {
		return nil
	}
	r := bundle.DefaultResolver()
	resolved, err := r.Resolve(context.Background(), m)
	if err != nil {
		return fmt.Errorf("online check failed: %w", err)
	}
	skips := 0
	for _, c := range resolved.Components {
		if c.Skipped != "" {
			fmt.Printf("  ~ %s (%s)\n", c.Name, c.Skipped)
			skips++
			continue
		}
		fmt.Printf("  ✓ %s@%s (%d assets)\n", c.Name, c.Tag, len(c.Assets))
	}
	for _, c := range resolved.Addons {
		if c.Skipped != "" {
			fmt.Printf("  ~ [addon] %s (%s)\n", c.Name, c.Skipped)
			skips++
		}
	}
	if skips > 0 {
		fmt.Printf("(%d entries skipped — pseudo-versions or unreleased addons; this is expected)\n", skips)
	}
	return nil
}

func runDiff(args []string) error {
	if len(args) != 2 {
		return errors.New("diff: expected two manifest paths (old new)")
	}
	old, err := bundle.Load(args[0])
	if err != nil {
		return fmt.Errorf("load old: %w", err)
	}
	new, err := bundle.Load(args[1])
	if err != nil {
		return fmt.Errorf("load new: %w", err)
	}
	d := bundle.DiffManifests(old, new)
	out := d.Render()
	fmt.Println(out)
	if d.IsEmpty() {
		// Not an error; just informational. Caller can grep "(no changes)".
		return nil
	}
	return nil
}

func runResolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	outPath := fs.String("o", "", "write resolved JSON to this path instead of stdout")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("resolve: expected exactly one bundle.toml path")
	}
	m, err := bundle.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	if verr := m.Validate(); verr != nil {
		return fmt.Errorf("schema validation failed:\n  - %s", verr)
	}
	r := bundle.DefaultResolver()
	resolved, err := r.Resolve(context.Background(), m)
	if err != nil {
		return err
	}
	buf, err := json.MarshalIndent(resolved, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	if *outPath == "" || *outPath == "-" {
		if _, err := os.Stdout.Write(buf); err != nil {
			return err
		}
		return nil
	}
	if err := os.WriteFile(*outPath, buf, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(buf), strings.TrimSpace(*outPath))
	return nil
}

func runFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	targetDir := fs.String("target-dir", "bin", "directory to extract per-OS binary trees into")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("fetch: expected exactly one bundle.toml path")
	}
	m, err := bundle.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	if verr := m.Validate(); verr != nil {
		return fmt.Errorf("schema validation failed:\n  - %s", verr)
	}
	r := bundle.DefaultResolver()
	resolved, err := r.Resolve(context.Background(), m)
	if err != nil {
		return err
	}
	return bundle.Fetch(context.Background(), resolved, bundle.FetchOptions{
		TargetDir: *targetDir,
		Token:     os.Getenv("GITHUB_TOKEN"),
		Logger:    func(format string, args ...any) { fmt.Fprintf(os.Stderr, "  "+format+"\n", args...) },
	})
}

func runAssemble(args []string) error {
	fs := flag.NewFlagSet("assemble", flag.ExitOnError)
	binDir := fs.String("bin-dir", "bin", "root of per-OS-arch binary tree (output of `bundlectl fetch`)")
	distDir := fs.String("dist-dir", "dist", "where to write the assembled bundle archives + checksums + MANIFEST")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("assemble: expected exactly one bundle.toml path")
	}
	m, err := bundle.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	if verr := m.Validate(); verr != nil {
		return fmt.Errorf("schema validation failed:\n  - %s", verr)
	}
	// Extras: install scripts + README + sample MCP + LICENSE +
	// bundle.toml (for traceability). Paths are relative to the
	// repo root where bundlectl is run.
	extras := []bundle.ExtraFile{
		{Path: "install.sh", ArchivePath: "install.sh", Executable: true},
		{Path: "install.ps1", ArchivePath: "install.ps1"},
		{Path: "README.md", ArchivePath: "README.md"},
		{Path: "LICENSE", ArchivePath: "LICENSE"},
		{Path: "bundle.toml", ArchivePath: "bundle.toml"},
		{Path: "templates/sample.mcp.json", ArchivePath: "templates/sample.mcp.json"},
	}
	results, err := bundle.Assemble(bundle.AssembleOptions{
		BinDir:   *binDir,
		DistDir:  *distDir,
		Manifest: m,
		Extras:   extras,
		Logger:   func(format string, args ...any) { fmt.Fprintf(os.Stderr, "  "+format+"\n", args...) },
	})
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Fprintf(os.Stdout, "%s  %s\n", r.SHA256, filepath.Base(r.ArchivePath))
	}
	return nil
}

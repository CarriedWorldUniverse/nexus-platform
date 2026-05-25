package bundle

import (
	"bytes"
	"io"
	"os"
)

// fs.go gathers the few os-package wrappers fetch.go + assemble.go
// reach through. Kept thin so tests don't have to mock the FS for
// the common path, but the indirection makes failure injection
// (disk-full, permission-denied) easy if it ever becomes worthwhile.

func osMkdirAllImpl(dir string) error             { return os.MkdirAll(dir, 0o755) }
func osCreateImpl(path string) (io.WriteCloser, error) {
	return os.Create(path)
}
func osOpenImpl(path string) (io.ReadCloser, error) {
	return os.Open(path)
}
func osChmodImpl(path string, mode int) error { return os.Chmod(path, os.FileMode(mode)) }

// bytesReader wraps a byte slice as an io.Reader. Local helper so
// fetch + assemble don't need to import bytes individually for the
// one-line case.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Package atomicfile writes files via a temp file + rename so a failed
// write never leaves a truncated file at the destination.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// defaultMode matches what os.Create yields under the usual 022 umask.
// os.CreateTemp uses 0600, which would make outputs owner-only.
const defaultMode fs.FileMode = 0o644

// Write creates path's parent directory, calls write with a temp file in
// that directory, then renames it over path. An existing file's
// permission bits are preserved; new files get 0644. On any error the
// temp file is removed and path is left untouched.
func Write(path string, write func(f *os.File) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	mode := defaultMode
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*.part")
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	tmpName := tmp.Name()
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	failed = false
	return nil
}

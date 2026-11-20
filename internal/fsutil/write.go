// Package fsutil writes files that may hold secrets.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path through a temporary file with a random
// name in the same directory, so readers see either the old or the new
// content, never a partial one. The result always has mode perm, even when
// path already existed with wider permissions (os.WriteFile keeps the mode
// of an existing file), and it is synced to disk before the rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

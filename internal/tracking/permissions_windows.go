//go:build windows

package tracking

import (
	"fmt"
	"os"
)

func ensureTrackingDir(dir string) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tracking directory is a symlink: %s", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("tracking directory is not a directory: %s", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	// Windows access is controlled by the user's profile ACLs; Go file modes are
	// mode-neutral there, so do not repair modes on existing path targets.
	return os.MkdirAll(dir, 0o700)
}

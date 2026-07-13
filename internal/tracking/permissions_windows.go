//go:build windows

package tracking

import "os"

func ensureTrackingDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func secureTrackingFile(*os.File) error {
	return nil
}

func secureTrackingPath(string) error {
	return nil
}

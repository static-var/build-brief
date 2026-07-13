//go:build !windows

package tracking

import "os"

const (
	privateTrackingDirMode  = 0o700
	privateTrackingFileMode = 0o600
)

func ensureTrackingDir(dir string) error {
	if err := os.MkdirAll(dir, privateTrackingDirMode); err != nil {
		return err
	}
	return os.Chmod(dir, privateTrackingDirMode)
}

func secureTrackingFile(file *os.File) error {
	return file.Chmod(privateTrackingFileMode)
}

func secureTrackingPath(path string) error {
	return os.Chmod(path, privateTrackingFileMode)
}

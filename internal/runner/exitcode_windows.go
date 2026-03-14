//go:build windows

package runner

func signalExitCode(error) (int, bool) {
	return 0, false
}

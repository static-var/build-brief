//go:build windows

package tracking

func processExists(pid int) bool {
	return pid > 0
}

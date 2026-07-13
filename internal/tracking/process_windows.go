//go:build windows

package tracking

import "syscall"

const errorInvalidParameter = syscall.Errno(87)

func processLiveness(pid int) (known, alive bool) {
	if pid <= 0 {
		return false, false
	}

	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err == nil {
		syscall.CloseHandle(handle)
		return true, true
	}
	if err == errorInvalidParameter {
		return true, false
	}
	return false, false
}

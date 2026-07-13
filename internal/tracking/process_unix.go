//go:build !windows

package tracking

import "syscall"

func processLiveness(pid int) (known, alive bool) {
	if pid <= 0 {
		return false, false
	}

	err := syscall.Kill(pid, 0)
	switch err {
	case nil, syscall.EPERM:
		return true, true
	case syscall.ESRCH:
		return true, false
	default:
		return false, false
	}
}

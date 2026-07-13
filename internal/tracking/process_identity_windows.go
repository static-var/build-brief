//go:build windows

package tracking

import (
	"strconv"

	"golang.org/x/sys/windows"
)

const processQueryLimitedInformation = 0x1000

func processIdentity(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	handle, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(handle)

	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", false
	}
	start := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	return "v1:windows:" + strconv.FormatUint(start, 10), true
}

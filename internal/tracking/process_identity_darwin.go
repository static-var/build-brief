//go:build darwin

package tracking

import (
	"strconv"

	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || process.Proc.P_pid != int32(pid) {
		return "", false
	}
	started := process.Proc.P_starttime
	return "v1:darwin:" + strconv.FormatInt(int64(started.Sec), 10) + ":" + strconv.FormatInt(int64(started.Usec), 10), true
}

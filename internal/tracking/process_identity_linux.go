//go:build linux

package tracking

import (
	"os"
	"strconv"
	"strings"
)

// processIdentity combines Linux's per-boot ID with /proc's process start
// tick, so a PID from an earlier boot cannot be mistaken for a current owner.
func processIdentity(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	endCommand := strings.LastIndexByte(string(stat), ')')
	if endCommand < 0 {
		return "", false
	}
	fields := strings.Fields(string(stat[endCommand+1:]))
	// fields starts at stat field 3; process start time is field 22.
	if len(fields) <= 19 {
		return "", false
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(bootID))
	if id == "" {
		return "", false
	}
	return "v1:linux:" + id + ":" + strconv.FormatUint(startTicks, 10), true
}

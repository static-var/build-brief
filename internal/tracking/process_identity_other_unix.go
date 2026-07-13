//go:build !windows && !linux && !darwin

package tracking

// Some Unix kernels do not expose a portable, permission-free process start
// identity through Go's system-call APIs. Callers fall back to PID liveness
// and retain live locks rather than risking reclamation of an original owner.
func processIdentity(pid int) (string, bool) {
	return "", false
}

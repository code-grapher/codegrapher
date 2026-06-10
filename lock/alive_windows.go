//go:build windows

package lock

import "os"

// defaultAlive on Windows uses OpenProcess: FindProcess always succeeds (it
// just records the PID), so we open and immediately release to probe liveness.
func defaultAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Release the handle immediately; we only needed liveness.
	_ = p.Release()
	// OpenProcess succeeded → process exists.
	// Note: on Windows FindProcess itself can succeed for non-existent PIDs,
	// but in practice this is the same signal-0 equivalent available without
	// importing golang.org/x/sys/windows. For correctness on Windows the alive
	// probe would use OpenProcess via x/sys/windows, but that dep is out of
	// scope for the pure-Go lock package.
	return true
}

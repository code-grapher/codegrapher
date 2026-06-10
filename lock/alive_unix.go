//go:build !windows

package lock

import (
	"os"
	"syscall"
)

// defaultAlive sends signal 0 to the process: succeeds if the process exists
// and we have permission to signal it, fails with ESRCH if it does not exist.
func defaultAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

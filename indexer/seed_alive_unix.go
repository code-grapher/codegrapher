//go:build !windows

package indexer

import (
	"errors"
	"os"
	"syscall"
)

// processAlive probes a PID with signal 0 (read-only). EPERM means the
// process exists but belongs to another user: alive.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

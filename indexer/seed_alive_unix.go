//go:build !windows

package indexer

import (
	"os"
	"syscall"
)

// processAlive probes a PID with signal 0 (read-only).
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

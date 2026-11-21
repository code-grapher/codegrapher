//go:build windows

package indexer

// processAlive cannot cheaply probe a PID on Windows; assume the holder is
// alive so seeding waits (bounded) and then drops the base revision.
func processAlive(pid int) bool { return true }

//go:build !windows

package daemon

func isTransientStateIOError(error) bool { return false }

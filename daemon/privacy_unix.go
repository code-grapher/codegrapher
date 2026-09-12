//go:build !windows

package daemon

func protectUserOnly(string) error { return nil }

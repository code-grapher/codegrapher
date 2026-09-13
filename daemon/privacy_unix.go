//go:build !windows

package daemon

import "github.com/strongo/cli-helpers/daemonlifecycle"

func protectUserOnly(path string) error { return daemonlifecycle.ProtectOwnerOnly(path) }

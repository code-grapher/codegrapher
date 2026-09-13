//go:build windows

package daemon

import (
	"github.com/strongo/cli-helpers/daemonlifecycle"
)

// FILE_ALL_ACCESS is STANDARD_RIGHTS_REQUIRED | SYNCHRONIZE plus the nine
// file-specific rights. x/sys intentionally does not export this composite.
const userFileAccess = 0x1f01ff

func protectUserOnly(path string) error {
	return daemonlifecycle.ProtectOwnerOnly(path)
}

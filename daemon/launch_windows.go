//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

func configureDetached(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008}
}

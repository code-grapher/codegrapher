package daemon

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/strongo/cli-helpers/daemonlifecycle"
)

func acquireDaemonLock(ctx context.Context, path string) (*os.File, error) {
	file, err := openDaemonLock(path)
	if err != nil {
		return nil, err
	}
	if err := daemonlifecycle.Lock(ctx, file, 25*time.Millisecond); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func tryAcquireDaemonLock(path string) (*os.File, bool, error) {
	file, err := openDaemonLock(path)
	if err != nil {
		return nil, false, err
	}
	locked, err := daemonlifecycle.TryLock(file)
	if err != nil || !locked {
		_ = file.Close()
		return nil, locked, err
	}
	return file, true, nil
}

func openDaemonLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	created := err == nil
	if os.IsExist(err) {
		if err := daemonlifecycle.ValidateOwnerOnly(path); err != nil {
			return nil, err
		}
		file, err = os.OpenFile(path, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, err
	}
	if created {
		err = daemonlifecycle.ProtectOwnerOnlyFile(file)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := daemonlifecycle.ValidateOwnerOnlyFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseDaemonLock(file *os.File) error {
	if file == nil {
		return nil
	}
	if err := daemonlifecycle.Unlock(file); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func daemonLockHeld(path string) (bool, error) {
	file, locked, err := tryAcquireDaemonLock(path)
	if err != nil {
		return false, err
	}
	if !locked {
		return true, nil
	}
	if err := releaseDaemonLock(file); err != nil {
		return false, fmt.Errorf("release lock probe: %w", err)
	}
	return false, nil
}

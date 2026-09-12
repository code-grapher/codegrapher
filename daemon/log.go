package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

func openDaemonLog(dir string) (*os.File, string, error) {
	if err := ensureStateDir(dir); err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, logFileName)
	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogSize {
		previous := path + ".1"
		if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("remove prior daemon log: %w", err)
		}
		if err := os.Rename(path, previous); err != nil {
			return nil, "", fmt.Errorf("rotate daemon log: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("inspect daemon log: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open daemon log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("protect daemon log: %w", err)
	}
	return file, path, nil
}

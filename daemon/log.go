package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	if err := protectUserOnly(path); err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("protect daemon log ownership: %w", err)
	}
	return file, path, nil
}

type rotatingLogWriter struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	size int64
}

func newRotatingLogWriter(dir string) (*rotatingLogWriter, error) {
	file, _, err := openDaemonLog(dir)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &rotatingLogWriter{dir: dir, file: file, size: info.Size()}, nil
}

func (w *rotatingLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(data)) > maxLogSize {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func (w *rotatingLogWriter) rotateLocked() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	path := filepath.Join(w.dir, logFileName)
	previous := path + ".1"
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(path, previous); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := protectUserOnly(path); err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = 0
	return nil
}

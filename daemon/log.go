package daemon

import (
	"errors"
	"fmt"
	"io"
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
		if err := copyLogGeneration(path, previous); err != nil {
			return nil, "", fmt.Errorf("copy daemon log generation: %w", err)
		}
		if err := os.Truncate(path, 0); err != nil {
			return nil, "", fmt.Errorf("truncate rotated daemon log: %w", err)
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

func copyLogGeneration(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = source.Close()
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := errors.Join(source.Close(), destination.Close())
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	return protectUserOnly(destinationPath)
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
	if err := copyLogGeneration(path, previous); err != nil {
		return err
	}
	// Copy then truncate instead of rename so rotation also works on Windows
	// while the process-level bootstrap stderr handle remains open.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_APPEND|os.O_WRONLY, 0o600)
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

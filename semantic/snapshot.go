package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	meaning "github.com/meaninggraph/cli/pkg/meaning"
	modelspec "github.com/modelspec-org/cli/pkg/modelspec"
)

// sourceSnapshot gives the independent parsers and range scanners identical
// bytes for each path. The indexer verifies these hashes against both the
// indexed file records and current files before publishing the projection.
type sourceSnapshot struct {
	root  string
	files map[string][]byte
	err   error
}

const maxSemanticSnapshotBytes int64 = 32 * 1024 * 1024

type snapshotTooLarge struct{ size int64 }

func (e snapshotTooLarge) Error() string {
	return fmt.Sprintf("semantic source is %d bytes, exceeding the read limit", e.size)
}

func newSourceSnapshot(root string) *sourceSnapshot {
	return &sourceSnapshot{root: root, files: make(map[string][]byte)}
}

func (s *sourceSnapshot) read(path string) ([]byte, error) {
	return s.readMax(path, maxSemanticSnapshotBytes)
}

func (s *sourceSnapshot) readMax(path string, max int64) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	if abs != s.root && !strings.HasPrefix(abs, s.root+string(filepath.Separator)) {
		return nil, fmt.Errorf("semantic source escapes project root: %s", path)
	}
	if data, ok := s.files[abs]; ok {
		if int64(len(data)) > max {
			return nil, snapshotTooLarge{size: int64(len(data))}
		}
		return data, nil
	}
	// Match the parser's regular-file and nonblocking guarantees. A path can
	// become a FIFO after Stat but before Open, and a file can grow after Stat.
	f, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("semantic source is not a regular file: %s", path)
	}
	if info.Size() > max {
		return nil, snapshotTooLarge{size: info.Size()}
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, snapshotTooLarge{size: int64(len(data))}
	}
	s.files[abs] = data
	return data, nil
}

func (s *sourceSnapshot) readRange(path string) []byte {
	data, err := s.read(path)
	if err != nil && s.err == nil {
		s.err = err
	}
	return data
}

func (s *sourceSnapshot) hashes() map[string]string {
	out := make(map[string]string, len(s.files))
	for abs, data := range s.files {
		rel, _ := filepath.Rel(s.root, abs)
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
	}
	return out
}

type snapshotModelFS struct {
	modelspec.OSFS
	snapshot *sourceSnapshot
}

func (f snapshotModelFS) Open(path string) (fs.File, error) {
	data, err := f.snapshot.readMax(path, modelspec.MaxInputBytes)
	if err != nil {
		var tooLarge snapshotTooLarge
		if errors.As(err, &tooLarge) {
			return nil, &modelspec.TooLargeError{File: path, Size: tooLarge.size}
		}
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &snapshotFile{Reader: bytes.NewReader(data), info: snapshotInfo{FileInfo: info, size: int64(len(data))}}, nil
}

type snapshotInfo struct {
	fs.FileInfo
	size int64
}

func (i snapshotInfo) Size() int64 { return i.size }

type snapshotFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *snapshotFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *snapshotFile) Close() error               { return nil }

type snapshotMeaningFS struct {
	meaning.OSFS
	snapshot *sourceSnapshot
}

func (f snapshotMeaningFS) ReadFile(path string) ([]byte, error) {
	return f.snapshot.read(path)
}

func (f snapshotMeaningFS) ReadFileMax(path string, max int64) ([]byte, error) {
	data, err := f.snapshot.readMax(path, max)
	if err != nil {
		var tooLarge snapshotTooLarge
		if errors.As(err, &tooLarge) {
			return nil, meaning.ErrTooLarge
		}
		return nil, err
	}
	return data, nil
}

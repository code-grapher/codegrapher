package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStateIsPrivateAndPublicStatusOmitsCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	state := diskState{
		Status: Status{Lifecycle: LifecycleStarting, ProjectPath: "/example"},
		Nonce:  "ownership-nonce",
		Token:  "control-token",
	}
	if err := writeState(dir, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := readState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Nonce != state.Nonce || loaded.Token != state.Token {
		t.Fatalf("credentials did not round trip: %+v", loaded)
	}
	encoded, err := json.Marshal(loaded.Status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), state.Nonce) || strings.Contains(string(encoded), state.Token) {
		t.Fatalf("public status leaked credentials: %s", encoded)
	}
	if runtime.GOOS != "windows" {
		assertMode(t, dir, 0o700)
		assertMode(t, filepath.Join(dir, stateFileName), 0o600)
	}
}

func TestDaemonLogRotatesOneBoundedGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, logFileName)
	if err := os.WriteFile(path, make([]byte, maxLogSize), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, openedPath, err := openDaemonLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if openedPath != path {
		t.Fatalf("opened path = %q, want %q", openedPath, path)
	}
	rotated, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Size() != maxLogSize {
		t.Fatalf("rotated size = %d, want %d", rotated.Size(), maxLogSize)
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if current.Size() != 0 {
		t.Fatalf("new log size = %d, want 0", current.Size())
	}
}

func TestDaemonLogRotatesWhileProcessIsRunning(t *testing.T) {
	dir := t.TempDir()
	writer, err := newRotatingLogWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(make([]byte, maxLogSize-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("boundary")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, logFileName) + ".1"); err != nil {
		t.Fatal("running writer did not rotate prior generation:", err)
	}
	current, err := os.Stat(filepath.Join(dir, logFileName))
	if err != nil {
		t.Fatal(err)
	}
	if current.Size() != int64(len("boundary")) {
		t.Fatalf("current log size = %d", current.Size())
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

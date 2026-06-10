package lock_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/specscore/codegrapher/lock"
)

// newTestLock creates a FileLock for lockPath with a fake clock and a
// configurable alive probe, so tests never depend on real time or PIDs.
func newTestLock(lockPath string, nowFn lock.NowFunc, alive lock.AliveFunc) *lock.FileLock {
	return lock.New(lockPath, lock.WithClock(nowFn), lock.WithAliveProbe(alive))
}

// alwaysDead returns false for every PID (simulates a dead process).
func alwaysDead(_ int) bool { return false }

// alwaysAlive returns true for every PID (simulates a live process).
func alwaysAlive(_ int) bool { return true }

func TestAcquireAndRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	fl := lock.New(path)
	if err := fl.Acquire(); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Lock file must exist and contain our PID.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(content), "%d", &pid); err != nil {
		t.Fatalf("parsing PID from lock file: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("lock file PID = %d, want %d", pid, os.Getpid())
	}

	fl.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock file should be gone after Release, stat err = %v", err)
	}
}

func TestDoubleAcquireSameProcessFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	// Both locks share the same PID (ours) and use alwaysAlive so the second
	// acquire sees the file as held by a live process.
	fl1 := newTestLock(path, time.Now, alwaysAlive)
	fl2 := newTestLock(path, time.Now, alwaysAlive)

	if err := fl1.Acquire(); err != nil {
		t.Fatalf("fl1.Acquire: %v", err)
	}
	t.Cleanup(fl1.Release)

	err := fl2.Acquire()
	if err == nil {
		fl2.Release()
		t.Fatal("fl2.Acquire should have failed, got nil")
	}
	if !errors.Is(err, lock.ErrLockUnavailable) {
		t.Errorf("error = %v, want ErrLockUnavailable", err)
	}
}

func TestStaleByDeadPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	// Write a lock file with an arbitrary PID that we mark as dead.
	if err := os.WriteFile(path, []byte("99999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fresh mtime (within stale timeout), but PID is dead — stale.
	fl := newTestLock(path, time.Now, alwaysDead)
	if err := fl.Acquire(); err != nil {
		t.Fatalf("Acquire with dead PID should succeed, got: %v", err)
	}
	fl.Release()
}

func TestStaleByTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	// Write a lock file with a PID that is "alive", but the clock says the
	// mtime is 3 minutes ago — stale by timeout.
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldClock := func() time.Time {
		info, _ := os.Stat(path)
		return info.ModTime().Add(3 * time.Minute) // 3 min after mtime → beyond StaleTimeout
	}

	fl := newTestLock(path, oldClock, alwaysAlive)
	if err := fl.Acquire(); err != nil {
		t.Fatalf("Acquire with timed-out lock should succeed, got: %v", err)
	}
	fl.Release()
}

func TestActiveLockBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	// Write a lock file held by an "alive" process with a fresh mtime.
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Clock returns now — mtime just written, so age is near 0.
	fl := newTestLock(path, time.Now, alwaysAlive)
	err := fl.Acquire()
	if err == nil {
		fl.Release()
		t.Fatal("Acquire should fail for an active lock, got nil")
	}
	if !errors.Is(err, lock.ErrLockUnavailable) {
		t.Errorf("error = %v, want ErrLockUnavailable", err)
	}
}

func TestWithLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	fl := lock.New(path)
	var sawFile bool
	err := fl.WithLock(func() error {
		_, statErr := os.Stat(path)
		sawFile = statErr == nil
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	if !sawFile {
		t.Error("lock file should exist inside WithLock callback")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("lock file should be gone after WithLock returns")
	}
}

func TestWithLockReleasesOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	fl := lock.New(path)
	err := fl.WithLock(func() error {
		return errors.New("test error")
	})
	if err == nil || err.Error() != "test error" {
		t.Errorf("WithLock should propagate fn error, got: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("lock file should be gone after WithLock fn returns error")
	}
}

func TestReleaseIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	fl := lock.New(path)
	if err := fl.Acquire(); err != nil {
		t.Fatal(err)
	}
	fl.Release()
	fl.Release() // must not panic or error
}

func TestReleaseDoesNotUnlinkForeignPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	// Write a lock file with a different PID.
	if err := os.WriteFile(path, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use WithPID to make the lock think it owns PID 99999 — different from 1.
	fl := lock.New(path, lock.WithPID(99999), lock.WithAliveProbe(alwaysDead))
	fl.Release() // should be no-op since !held

	// File with PID 1 should still exist.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("Release without Acquire should not remove the lock file")
	}
}

// TestRaceAcquire verifies that concurrent Acquire calls do not both succeed.
// Uses real PIDs (os.Getpid()) and a real alive probe so the alive-check logic
// doesn't interfere — if a goroutine loses the O_EXCL race it must get EEXIST
// and return ErrLockUnavailable regardless of alive status.
func TestRaceAcquire(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "race.lock")

	const N = 20
	type result struct {
		fl  *lock.FileLock
		err error
	}
	results := make(chan result, N)
	for range N {
		go func() {
			fl := lock.New(path) // default alive probe (real signal 0)
			err := fl.Acquire()
			results <- result{fl, err}
		}()
	}

	// Collect ALL results before releasing any lock, so no winner can slip
	// through a gap opened by an early Release.
	allResults := make([]result, N)
	for i := range N {
		allResults[i] = <-results
	}

	var winners []*lock.FileLock
	for _, r := range allResults {
		if r.err == nil {
			winners = append(winners, r.fl)
		}
	}
	// Clean up any won locks.
	for _, fl := range winners {
		fl.Release()
	}
	// Exactly one goroutine should have won.
	if len(winners) != 1 {
		t.Errorf("expected exactly 1 winner, got %d", len(winners))
	}
}

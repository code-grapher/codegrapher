// Package lock implements a cross-process file lock using an exclusive PID
// file, ported from FileLock in src/utils.ts of
// github.com/colbymchenry/codegraph (MIT).
//
// Protocol: the lock file is created with O_CREATE|O_EXCL so only one process
// can win the race. On conflict the holder's PID and the file mtime are read:
// the lock is considered STALE (deleted and retaken) if the mtime is older
// than 2 minutes OR the PID is no longer alive (signal 0 probe). Otherwise
// [ErrLockUnavailable] is returned.
//
// Release verifies the stored PID matches our own before unlinking, so a
// late-running stale-cleanup by another process cannot accidentally release a
// lock we legitimately hold.
//
// All external dependencies (clock, own PID, alive-probe) are injectable for
// deterministic unit tests.
package lock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// StaleTimeout is how old a lock file's mtime must be before it is
// considered stale regardless of PID liveness, matching the original's
// STALE_TIMEOUT_MS = 2 minutes.
const StaleTimeout = 2 * time.Minute

// ErrLockUnavailable is returned when the lock is held by another live
// process that has not timed out. Callers (e.g. the file watcher) can use
// errors.Is to distinguish this from unexpected I/O errors.
var ErrLockUnavailable = errors.New("codegraph database is locked by another process")

// AliveFunc probes whether a process is alive. The production value sends
// signal 0 (os.FindProcess + p.Signal(syscall.Signal(0))).
type AliveFunc func(pid int) bool

// NowFunc returns the current wall-clock time. Injectable so tests can
// control time without sleeping.
type NowFunc func() time.Time

// FileLock is a cross-process exclusive lock backed by a PID file.
// The zero value is not usable; construct with [New].
type FileLock struct {
	path  string
	held  bool
	pid   int
	now   NowFunc
	alive AliveFunc
}

// Option configures a FileLock.
type Option func(*FileLock)

// WithClock overrides the clock used for stale-timeout checks. Useful in
// tests to advance time without sleeping.
func WithClock(fn NowFunc) Option {
	return func(fl *FileLock) { fl.now = fn }
}

// WithPID overrides the PID written to the lock file and used for ownership
// checks. Defaults to os.Getpid().
func WithPID(pid int) Option {
	return func(fl *FileLock) { fl.pid = pid }
}

// WithAliveProbe overrides the alive-probe function. Defaults to a signal-0
// probe via os.FindProcess.
func WithAliveProbe(fn AliveFunc) Option {
	return func(fl *FileLock) { fl.alive = fn }
}

// New returns a FileLock for the given path, configured by opts.
func New(path string, opts ...Option) *FileLock {
	fl := &FileLock{
		path:  path,
		pid:   os.Getpid(),
		now:   func() time.Time { return time.Now() },
		alive: defaultAlive,
	}
	for _, o := range opts {
		o(fl)
	}
	return fl
}

// Acquire attempts to take the lock. It returns [ErrLockUnavailable] (wrapped)
// when another live, non-timed-out process holds it, and other errors for
// unexpected I/O failures.
func (fl *FileLock) Acquire() error {
	// If a lock file already exists, decide whether it is stale.
	if _, err := os.Stat(fl.path); err == nil {
		if staleErr := fl.handleExistingLock(); staleErr != nil {
			return staleErr
		}
		// handleExistingLock either returned an error or removed the stale file.
	}

	// Create the lock file exclusively — only one goroutine/process wins.
	f, err := os.OpenFile(fl.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			// Race: another process grabbed the lock between our stat and open.
			return fmt.Errorf("%w: %s", ErrLockUnavailable, fl.path)
		}
		return fmt.Errorf("creating lock file %s: %w", fl.path, err)
	}
	_, werr := fmt.Fprintf(f, "%d", fl.pid)
	cerr := f.Close()
	if werr != nil {
		_ = os.Remove(fl.path)
		return fmt.Errorf("writing lock file %s: %w", fl.path, werr)
	}
	if cerr != nil {
		_ = os.Remove(fl.path)
		return fmt.Errorf("closing lock file %s: %w", fl.path, cerr)
	}
	fl.held = true
	return nil
}

// handleExistingLock reads an existing lock file, checks whether it is stale,
// and removes it if so. Returns a non-nil error (wrapping ErrLockUnavailable)
// if the lock is actively held.
func (fl *FileLock) handleExistingLock() error {
	content, readErr := os.ReadFile(fl.path)
	if readErr != nil && os.IsNotExist(readErr) {
		// Disappeared between the caller's Stat and our read — fine.
		return nil
	}

	info, err := os.Stat(fl.path)
	if err != nil {
		// Disappeared between ReadFile and Stat — nothing to do.
		return nil
	}

	pid, pidErr := strconv.Atoi(strings.TrimSpace(string(content)))
	if readErr != nil {
		pidErr = readErr // unreadable content == invalid content
	}
	lockAge := fl.now().Sub(info.ModTime())

	// Stale when older than the timeout (regardless of PID — upstream rule),
	// or when it names a VALID pid whose process is dead (crashed owner:
	// reclaim immediately, like upstream).
	//
	// DELIBERATE DIVERGENCE from upstream (see KNOWN-BUGS.md D-1): upstream
	// additionally reclaims locks with empty/unparsable content regardless of
	// age. That races against a concurrent acquirer between its O_EXCL create
	// and PID write — the file is momentarily empty — letting two processes
	// both "win" and corrupt the index. A young lock with invalid content is
	// therefore treated as held here: it is either mid-acquisition (resolves
	// in microseconds) or garbage that becomes reclaimable via the age rule
	// in at most 2 minutes. Observable behavior is otherwise identical.
	isStale := lockAge >= StaleTimeout || (pidErr == nil && !fl.alive(pid))
	if !isStale {
		if pidErr != nil {
			return fmt.Errorf("%w (lock file has no readable PID): if this is stale, run 'codegraph unlock' or delete %s",
				ErrLockUnavailable, fl.path)
		}
		return fmt.Errorf("%w (PID %d): if this is stale, run 'codegraph unlock' or delete %s",
			ErrLockUnavailable, pid, fl.path)
	}

	// Stale: remove and allow the caller to retake.
	if err := os.Remove(fl.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale lock file %s: %w", fl.path, err)
	}
	return nil
}

// Release releases the lock. It verifies that the stored PID equals our own
// before unlinking, so a concurrent stale-cleanup cannot disrupt us. Safe to
// call when the lock is not held (no-op).
func (fl *FileLock) Release() {
	if !fl.held {
		return
	}
	fl.held = false

	content, err := os.ReadFile(fl.path)
	if err != nil {
		// Lock file already gone — fine.
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || pid != fl.pid {
		// Not ours (shouldn't happen, but guard anyway).
		return
	}
	_ = os.Remove(fl.path)
}

// WithLock acquires the lock, calls fn, then releases. The lock is released
// even if fn panics.
func (fl *FileLock) WithLock(fn func() error) error {
	if err := fl.Acquire(); err != nil {
		return err
	}
	defer fl.Release()
	return fn()
}

// IsHeld reports whether this instance currently holds the lock.
func (fl *FileLock) IsHeld() bool { return fl.held }

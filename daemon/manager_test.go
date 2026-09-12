package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestStatusDistinguishesDeadStateFromUnreachableLiveOwner(t *testing.T) {
	dir := t.TempDir()
	state := diskState{
		Status: Status{
			Lifecycle:   LifecycleReady,
			ProjectPath: "/repo",
			Endpoint:    "http://127.0.0.1:1",
		},
		Nonce: "nonce",
		Token: "token",
	}
	if err := writeState(dir, state); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{StateDir: dir, Executable: "unused"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleFailed || status.Health.Live {
		t.Fatalf("dead persisted owner status = %+v", status)
	}

	lifetime := flock.New(dir + "/" + lifetimeLockName)
	locked, err := lifetime.TryLock()
	if err != nil || !locked {
		t.Fatalf("acquire test lifetime lock: locked=%t err=%v", locked, err)
	}
	defer func() { _ = lifetime.Unlock() }()
	if _, err := manager.Status(ctx); err == nil {
		t.Fatal("unreachable live owner was reported reclaimable")
	}
}

func TestMissingOrStoppedStateNeverOverridesHeldLifetimeOwner(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{StateDir: dir, Executable: "unused"}
	lifetime := flock.New(filepath.Join(dir, lifetimeLockName))
	locked, err := lifetime.TryLock()
	if err != nil || !locked {
		t.Fatalf("acquire lifetime lock: %v", err)
	}
	defer func() { _ = lifetime.Unlock() }()
	if _, err := manager.Status(context.Background()); err == nil {
		t.Fatal("missing state hid held lifetime owner")
	}
	if _, err := manager.Stop(context.Background()); err == nil {
		t.Fatal("missing state allowed unauthenticated stop")
	}
	if err := writeState(dir, diskState{Status: Status{Lifecycle: LifecycleStopped}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Status(context.Background()); err == nil {
		t.Fatal("stopped state hid held lifetime owner")
	}
}

func TestWaitStoppedReturnsFailureAndHonorsDeadline(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{StateDir: dir, Executable: "unused"}
	failed := diskState{Status: Status{Lifecycle: LifecycleFailed, Health: Health{LastError: "close failed"}}, Nonce: "n"}
	if err := writeState(dir, failed); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.waitStopped(context.Background(), "n"); err == nil {
		t.Fatal("failed stop was reported successful")
	}
	failed.Lifecycle = LifecycleStopping
	if err := writeState(dir, failed); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := manager.waitStopped(ctx, "n"); err == nil {
		t.Fatal("stop wait ignored its deadline")
	}
}

func TestRestartPreflightPreservesExistingState(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{StateDir: dir, Executable: "unused"}
	original := diskState{Status: Status{Lifecycle: LifecycleReady, ProjectPath: "/existing"}, Nonce: "n", Token: "t"}
	if err := writeState(dir, original); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	if _, err := manager.Restart(context.Background(), missing); err == nil {
		t.Fatal("restart accepted missing target")
	}
	current, err := readState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if current.Nonce != original.Nonce || current.Lifecycle != original.Lifecycle {
		t.Fatalf("failed restart mutated owner: %+v", current)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("restart preflight created target: %v", err)
	}
}

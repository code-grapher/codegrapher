package daemon

import (
	"context"
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

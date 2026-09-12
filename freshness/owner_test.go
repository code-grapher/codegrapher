package freshness_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/specscore/codegrapher/freshness"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/watch"
)

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current
func TestStartDrainsEventAcceptedDuringStartupReconciliation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Before() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _, err := indexer.Init(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	startupEntered := make(chan struct{})
	releaseStartup := make(chan struct{})
	type startOutcome struct {
		owner *freshness.Owner
		err   error
	}
	outcomeCh := make(chan startOutcome, 1)
	go func() {
		owner, _, startErr := freshness.Start(context.Background(), dir, freshness.Options{
			Watch: watch.Options{DebounceMs: 10, InertForTests: true},
			ReconcileStartup: func(*indexer.Indexer) (watch.SyncResult, error) {
				close(startupEntered)
				<-releaseStartup
				return watch.SyncResult{}, nil
			},
		})
		outcomeCh <- startOutcome{owner: owner, err: startErr}
	}()
	select {
	case <-startupEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("startup reconciliation did not begin")
	}
	if err := os.WriteFile(path, []byte("package sample\n\nfunc After() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !watch.EmitEventForTests(dir, "main.go") {
		t.Fatal("watcher was not established before startup reconciliation")
	}
	close(releaseStartup)
	var outcome startOutcome
	select {
	case outcome = <-outcomeCh:
	case <-time.After(3 * time.Second):
		t.Fatal("freshness start returned before startup generation drained")
	}
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	t.Cleanup(func() { _ = outcome.owner.Close() })
	status := outcome.owner.Status()
	if !status.WatchReady || !status.IndexCurrent || status.AcceptedGeneration != status.CompletedGeneration {
		t.Fatalf("status after startup drain = %+v", status)
	}
	check, err := indexer.Open(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	found := false
	for _, graphStore := range check.Stores() {
		nodes, getErr := graphStore.GetNodesByName("After")
		if getErr != nil {
			t.Fatal(getErr)
		}
		found = found || len(nodes) > 0
	}
	if !found {
		t.Fatal("startup barrier returned before event-driven index update")
	}
}

func TestStartRejectsUninitializedRepository(t *testing.T) {
	_, _, err := freshness.Start(context.Background(), t.TempDir(), freshness.Options{})
	if err == nil {
		t.Fatal("Start succeeded for uninitialized repository")
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:daemon-health-reports-liveness-watch-readiness-and-index-currency-separately
func TestStatusStaysStaleUntilLatestAcceptedGenerationSucceeds(t *testing.T) {
	dir := initializedRepository(t)
	var calls atomic.Int32
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	thirdStarted := make(chan struct{})
	releaseThird := make(chan struct{})
	owner, _, err := freshness.Start(context.Background(), dir, freshness.Options{
		Watch: watch.Options{DebounceMs: 5, InertForTests: true},
		ReconcileStartup: func(*indexer.Indexer) (watch.SyncResult, error) {
			return watch.SyncResult{}, nil
		},
		ReconcilePaths: func(*indexer.Indexer, []string) (watch.SyncResult, error) {
			switch calls.Add(1) {
			case 1:
				return watch.SyncResult{}, errors.New("transient reconcile failure")
			case 2:
				close(secondStarted)
				<-releaseSecond
				return watch.SyncResult{FilesChanged: 1}, nil
			case 3:
				close(thirdStarted)
				<-releaseThird
				return watch.SyncResult{FilesChanged: 1}, nil
			default:
				return watch.SyncResult{}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })

	if !watch.EmitEventForTests(dir, "main.go") {
		t.Fatal("synthetic event was not delivered")
	}
	waitFor(t, 2*time.Second, func() bool {
		return owner.Status().LastError == "transient reconcile failure"
	})
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not start")
	}
	if !watch.EmitEventForTests(dir, "main.go") {
		t.Fatal("event during retry was not delivered")
	}
	close(releaseSecond)
	select {
	case <-thirdStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up generation did not start")
	}
	status := owner.Status()
	if status.IndexCurrent || status.AcceptedGeneration == status.CompletedGeneration {
		t.Fatalf("older successful retry incorrectly reported current: %+v", status)
	}
	close(releaseThird)
	waitFor(t, 2*time.Second, func() bool { return owner.Status().IndexCurrent })
}

func initializedRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _, err := indexer.Init(dir, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

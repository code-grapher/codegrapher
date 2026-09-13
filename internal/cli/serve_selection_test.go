package cli

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestCombinedServeCancelsAndJoinsWatchWhenMCPCompletes(t *testing.T) {
	owner := &fakeFreshnessSession{waitDone: make(chan struct{})}
	server := fakeMCPServer{serve: func(context.Context, io.Reader, io.Writer) error { return nil }}
	if err := runCombinedServe(context.Background(), owner, server, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !owner.closed.Load() {
		t.Fatal("combined serve did not close freshness owner")
	}
	select {
	case <-owner.waitDone:
	default:
		t.Fatal("combined serve returned before watcher wait observed cancellation")
	}
}

func TestRunServeGroupAllowsNoParticipants(t *testing.T) {
	if err := runServeGroup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestRunServeGroupJoinsEveryParticipantAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- runServeGroup(ctx, []func(context.Context) error{
			func(runCtx context.Context) error { <-runCtx.Done(); return nil },
			func(runCtx context.Context) error { <-runCtx.Done(); <-release; return nil },
		})
	}()
	cancel()
	select {
	case err := <-returned:
		t.Fatalf("serve group returned before every participant joined: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve group did not return after every participant joined")
	}
}

func TestRunServeGroupCancelsAndJoinsSiblingAfterFirstFailure(t *testing.T) {
	want := errors.New("participant failed")
	siblingJoined := make(chan struct{})
	err := runServeGroup(context.Background(), []func(context.Context) error{
		func(context.Context) error { return want },
		func(runCtx context.Context) error {
			<-runCtx.Done()
			close(siblingJoined)
			return nil
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("serve error = %v, want %v", err, want)
	}
	select {
	case <-siblingJoined:
	default:
		t.Fatal("serve group returned before failed participant's sibling joined")
	}
}

func TestServeCapabilitySelection(t *testing.T) {
	if status := servedFreshness(nil); status.IndexCurrent || status.WatchReady {
		t.Fatalf("API-only serve claimed unobserved freshness: %+v", status)
	}
	if got := selectServeCapabilities(false, false, false, false, false); got != (serveCapabilities{mcp: true, watch: true, api: true}) {
		t.Fatalf("bare serve = %+v", got)
	}
	if got := selectServeCapabilities(false, false, false, false, true); got != (serveCapabilities{mcp: true, api: true}) {
		t.Fatalf("bare --no-watch = %+v", got)
	}
	for mask := 1; mask < 8; mask++ {
		want := serveCapabilities{mcp: mask&1 != 0, watch: mask&2 != 0, api: mask&4 != 0}
		got := selectServeCapabilities(true, want.mcp, want.watch, want.api, false)
		if got != want {
			t.Fatalf("explicit mask %03b = %+v, want %+v", mask, got, want)
		}
	}
	if got := selectServeCapabilities(true, true, true, true, true); got != (serveCapabilities{mcp: true, api: true}) {
		t.Fatalf("deprecated no-watch override = %+v", got)
	}
}

func TestServeRegistersSecureAPITransportFlagsOnce(t *testing.T) {
	cmd := newServeCmd()
	for _, name := range []string{"api-tls-cert", "api-tls-key", "api-public-authority", "api-external-tls"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("serve flag --%s is missing", name)
		}
	}
}

type fakeFreshnessSession struct {
	closed   atomic.Bool
	waitDone chan struct{}
}

func (f *fakeFreshnessSession) Wait(ctx context.Context) error {
	<-ctx.Done()
	close(f.waitDone)
	return nil
}

func (f *fakeFreshnessSession) Close() error {
	f.closed.Store(true)
	return nil
}

type fakeMCPServer struct {
	serve func(context.Context, io.Reader, io.Writer) error
}

func (f fakeMCPServer) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	return f.serve(ctx, input, output)
}

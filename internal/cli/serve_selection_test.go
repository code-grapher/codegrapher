package cli

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
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

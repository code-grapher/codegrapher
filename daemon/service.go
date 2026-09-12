package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/specscore/codegrapher/browserapi"
	"github.com/specscore/codegrapher/freshness"
	"github.com/specscore/codegrapher/watch"
)

// RunFromEnvironment runs the internal daemon child. It is intentionally
// environment-authenticated and should only be called by the hidden CLI verb.
func RunFromEnvironment(ctx context.Context, projectPath string) error {
	stateDir := os.Getenv(envStateDir)
	nonce := os.Getenv(envNonce)
	token := os.Getenv(envToken)
	browserToken := os.Getenv(envBrowserToken)
	if stateDir == "" || nonce == "" || token == "" || browserToken == "" {
		return errors.New("daemon child credentials are missing")
	}
	return Run(ctx, projectPath, stateDir, nonce, token, browserToken)
}

// Run owns the lifetime lock, control endpoint, and shared freshness owner.
func Run(parent context.Context, projectPath, stateDir, nonce, token, browserToken string) error {
	if err := ensureStateDir(stateDir); err != nil {
		return err
	}
	logWriter, err := newRotatingLogWriter(stateDir)
	if err != nil {
		return err
	}
	defer func() { _ = logWriter.Close() }()
	log.SetOutput(logWriter)
	projectPath, err = canonicalProjectPath(projectPath)
	if err != nil {
		return err
	}
	lifetime := flock.New(filepath.Join(stateDir, lifetimeLockName))
	locked, err := lifetime.TryLock()
	if err != nil {
		return fmt.Errorf("acquire daemon lifetime lock: %w", err)
	}
	if !locked {
		return errors.New("another CodeGrapher daemon owns the lifetime lock")
	}
	defer func() { _ = lifetime.Unlock() }()

	state, err := readState(stateDir)
	if err != nil {
		return err
	}
	if !secretEqual(state.Nonce, nonce) || !secretEqual(state.Token, token) || !secretEqual(state.BrowserToken, browserToken) || state.ProjectPath != projectPath || state.Lifecycle != LifecycleStarting {
		return errors.New("daemon startup ownership changed before child initialization")
	}

	runCtx, cancel := context.WithCancel(parent)
	defer cancel()
	runtime := &serviceRuntime{
		stateDir: stateDir,
		state:    state,
		nonce:    nonce,
		token:    token,
		stopCh:   make(chan struct{}),
		cancel:   cancel,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return runtime.fail(fmt.Errorf("bind loopback control endpoint: %w", err))
	}
	defer func() { _ = listener.Close() }()
	runtime.mu.Lock()
	runtime.state.PID = os.Getpid()
	runtime.state.Endpoint = "http://" + listener.Addr().String()
	runtime.state.Health.Live = true
	err = runtime.persistLocked()
	runtime.mu.Unlock()
	if err != nil {
		return err
	}

	server := &http.Server{
		Handler:           runtime.controlHandler(),
		ReadHeaderTimeout: 2 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErr <- serveErr
		}
	}()

	log.Printf("codegrapher daemon starting pid=%d project=%s endpoint=%s", os.Getpid(), projectPath, listener.Addr())
	owner, _, err := freshness.Start(runCtx, projectPath, freshness.Options{
		Watch: watch.Options{OnObservation: func(observation watch.Observation) {
			logObservation(observation)
			switch observation.Kind {
			case watch.ObservationOperationStarted, watch.ObservationOperationCompleted,
				watch.ObservationOperationFailed, watch.ObservationOperationRetry,
				watch.ObservationWatcherError:
				runtime.refreshOwnerState()
			}
		}},
	})
	if err != nil {
		shutdownServer(server)
		select {
		case <-runtime.stopCh:
			if stopErr := runtime.stopped(); stopErr != nil {
				return stopErr
			}
			return nil
		default:
			return runtime.fail(err)
		}
	}
	runtime.setOwner(owner)
	browserListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = owner.Close()
		shutdownServer(server)
		return runtime.fail(fmt.Errorf("bind browser API: %w", err))
	}
	defer func() { _ = browserListener.Close() }()
	browserServer, err := browserapi.New(owner.Indexer(), browserapi.Config{
		Token:          browserToken,
		AllowedOrigins: splitOrigins(os.Getenv(envBrowserOrigins)),
		Freshness:      owner.Status,
	})
	if err != nil {
		_ = owner.Close()
		shutdownServer(server)
		return runtime.fail(err)
	}
	runtime.mu.Lock()
	runtime.state.BrowserEndpoint = "http://" + browserListener.Addr().String() + browserapi.BasePath
	err = runtime.persistLocked()
	runtime.mu.Unlock()
	if err != nil {
		_ = owner.Close()
		shutdownServer(server)
		return err
	}
	browserErr := make(chan error, 1)
	go func() { browserErr <- browserServer.Serve(runCtx, browserListener) }()
	if err := runtime.ready(); err != nil {
		_ = owner.Close()
		shutdownServer(server)
		cancel()
		<-browserErr
		return err
	}
	log.Printf("codegrapher daemon ready pid=%d project=%s", os.Getpid(), projectPath)

	watchErr := make(chan error, 1)
	go func() { watchErr <- owner.Wait(runCtx) }()
	var terminalErr error
	browserFinished := false
	select {
	case <-runtime.stopCh:
	case <-parent.Done():
	case terminalErr = <-watchErr:
	case terminalErr = <-serverErr:
	case terminalErr = <-browserErr:
		browserFinished = true
	}
	cancel()
	if terminalErr != nil {
		_ = runtime.fail(terminalErr)
	} else {
		_ = runtime.stopping()
	}
	shutdownServer(server)
	if !browserFinished {
		select {
		case browserCloseErr := <-browserErr:
			if terminalErr == nil {
				terminalErr = browserCloseErr
			}
		case <-time.After(3 * time.Second):
			if terminalErr == nil {
				terminalErr = errors.New("browser API did not stop")
			}
		}
	}
	closeErr := owner.Close()
	if terminalErr == nil {
		terminalErr = closeErr
	}
	if terminalErr != nil {
		_ = runtime.fail(terminalErr)
		return terminalErr
	}
	if err := runtime.stopped(); err != nil {
		return err
	}
	log.Printf("codegrapher daemon stopped pid=%d project=%s", os.Getpid(), projectPath)
	return nil
}

func splitOrigins(raw string) []string {
	var origins []string
	for origin := range strings.SplitSeq(raw, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

type serviceRuntime struct {
	mu       sync.Mutex
	stateDir string
	state    diskState
	nonce    string
	token    string
	owner    *freshness.Owner
	stopOnce sync.Once
	stopCh   chan struct{}
	cancel   context.CancelFunc
}

func (r *serviceRuntime) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /control/v1/status", func(writer http.ResponseWriter, request *http.Request) {
		if !r.authenticate(request) {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(r.snapshot())
	})
	mux.HandleFunc("POST /control/v1/stop", func(writer http.ResponseWriter, request *http.Request) {
		if !r.authenticate(request) {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.requestStop()
		writer.WriteHeader(http.StatusAccepted)
	})
	return mux
}

func (r *serviceRuntime) authenticate(request *http.Request) bool {
	provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	return secretEqual(provided, r.token) && secretEqual(request.Header.Get("X-CodeGrapher-Nonce"), r.nonce)
}

func secretEqual(left, right string) bool {
	if len(left) != len(right) || len(left) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (r *serviceRuntime) requestStop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.cancel()
	})
}

func (r *serviceRuntime) setOwner(owner *freshness.Owner) {
	r.mu.Lock()
	r.owner = owner
	r.mu.Unlock()
}

func (r *serviceRuntime) snapshot() Status {
	r.mu.Lock()
	state := r.state.Status
	owner := r.owner
	r.mu.Unlock()
	if owner == nil {
		return state
	}
	current := owner.Status()
	state.Health = healthFromFreshness(current)
	state.Health.Live = true
	if state.Lifecycle == LifecycleReady || state.Lifecycle == LifecycleDegraded {
		if current.WatchReady && current.IndexCurrent {
			state.Lifecycle = LifecycleReady
		} else {
			state.Lifecycle = LifecycleDegraded
		}
	}
	return state
}

func (r *serviceRuntime) ready() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Lifecycle = LifecycleReady
	r.state.ReadyAt = time.Now().UTC()
	if r.owner != nil {
		r.state.Health = healthFromFreshness(r.owner.Status())
	}
	r.state.Health.Live = true
	return r.persistLocked()
}

func (r *serviceRuntime) refreshOwnerState() {
	r.mu.Lock()
	owner := r.owner
	r.mu.Unlock()
	if owner == nil {
		return
	}
	current := owner.Status()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Lifecycle != LifecycleReady && r.state.Lifecycle != LifecycleDegraded {
		return
	}
	r.state.Health = healthFromFreshness(current)
	r.state.Health.Live = true
	if current.WatchReady && current.IndexCurrent {
		r.state.Lifecycle = LifecycleReady
	} else {
		r.state.Lifecycle = LifecycleDegraded
	}
	if err := r.persistLocked(); err != nil {
		log.Printf("persist daemon health: %v", err)
	}
}

func (r *serviceRuntime) stopping() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Lifecycle = LifecycleStopping
	r.state.Health.Live = true
	return r.persistLocked()
}

func (r *serviceRuntime) stopped() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Lifecycle = LifecycleStopped
	r.state.StoppedAt = time.Now().UTC()
	r.state.PID = 0
	r.state.Endpoint = ""
	r.state.BrowserEndpoint = ""
	r.state.Health = Health{}
	r.state.Token = ""
	r.state.BrowserToken = ""
	return r.persistLocked()
}

func (r *serviceRuntime) fail(err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Lifecycle = LifecycleFailed
	r.state.Health.Live = false
	r.state.Health.WatchReady = false
	r.state.Health.IndexCurrent = false
	r.state.Health.LastError = err.Error()
	if persistErr := r.persistLocked(); persistErr != nil {
		return errors.Join(err, persistErr)
	}
	return err
}

func (r *serviceRuntime) persistLocked() error {
	if !secretEqual(r.state.Nonce, r.nonce) {
		return errors.New("refusing daemon state write after ownership nonce changed")
	}
	return writeState(r.stateDir, r.state)
}

func healthFromFreshness(status freshness.Status) Health {
	return Health{
		WatchReady:            status.WatchReady,
		IndexCurrent:          status.IndexCurrent,
		PendingDirtyPaths:     status.PendingDirtyPaths,
		WholeTreeDirty:        status.WholeTreeDirty,
		AcceptedGeneration:    status.AcceptedGeneration,
		CompletedGeneration:   status.CompletedGeneration,
		LastSuccessfulUpdate:  status.LastSuccessfulUpdate,
		LastReconciliation:    status.LastReconciliation,
		LastOperationDuration: status.LastOperationDuration.String(),
		LastError:             status.LastError,
	}
}

func shutdownServer(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func logObservation(observation watch.Observation) {
	switch observation.Kind {
	case watch.ObservationOperationStarted:
		log.Printf("operation=%d started kind=%s events=%d dirty_paths=%d", observation.OperationID, observation.Operation, observation.EventsReceived, observation.DirtyPaths)
	case watch.ObservationOperationCompleted:
		log.Printf("operation=%d completed kind=%s duration=%s changed=%d nodes_updated=%d", observation.OperationID, observation.Operation, observation.Duration, observation.Result.FilesChanged, observation.Result.NodesUpdated)
	case watch.ObservationOperationFailed, watch.ObservationOperationRetry, watch.ObservationWatcherError:
		log.Printf("operation=%d kind=%s error=%v", observation.OperationID, observation.Kind, observation.Err)
	}
}

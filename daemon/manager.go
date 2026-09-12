package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/specscore/codegrapher/indexer"
)

const (
	envStateDir = "CODEGRAPH_DAEMON_DIR"
	envNonce    = "CODEGRAPH_DAEMON_NONCE"
	envToken    = "CODEGRAPH_DAEMON_TOKEN"
)

// Manager coordinates the one daemon owned by the current user.
type Manager struct {
	StateDir     string
	Executable   string
	HTTPClient   *http.Client
	StartTimeout time.Duration
}

// NewManager resolves default process and state locations.
func NewManager() (*Manager, error) {
	stateDir, err := defaultStateDir()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve codegrapher executable: %w", err)
	}
	return &Manager{
		StateDir:     stateDir,
		Executable:   executable,
		HTTPClient:   &http.Client{Timeout: 2 * time.Second},
		StartTimeout: 30 * time.Second,
	}, nil
}

func (m *Manager) defaults() error {
	if m.StateDir == "" {
		dir, err := defaultStateDir()
		if err != nil {
			return err
		}
		m.StateDir = dir
	}
	if m.Executable == "" {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve codegrapher executable: %w", err)
		}
		m.Executable = executable
	}
	if m.HTTPClient == nil {
		m.HTTPClient = &http.Client{Timeout: 2 * time.Second}
	}
	if m.StartTimeout <= 0 {
		m.StartTimeout = 30 * time.Second
	}
	return ensureStateDir(m.StateDir)
}

// Start launches the daemon and waits for watch coverage, startup sync, and
// the startup generation barrier. A live daemon for the same path is idempotent.
func (m *Manager) Start(ctx context.Context, projectPath string) (Status, error) {
	return m.start(ctx, projectPath, false)
}

func (m *Manager) start(ctx context.Context, projectPath string, lockHeld bool) (Status, error) {
	if err := m.defaults(); err != nil {
		return Status{}, err
	}
	projectPath, err := canonicalProjectPath(projectPath)
	if err != nil {
		return Status{}, err
	}
	if !indexer.IsInitialized(projectPath) {
		return Status{}, fmt.Errorf("CodeGraph not initialized in %s; run 'codegrapher init' there first", projectPath)
	}

	if !lockHeld {
		startLock := flock.New(filepath.Join(m.StateDir, startLockName))
		locked, lockErr := startLock.TryLockContext(ctx, 25*time.Millisecond)
		if lockErr != nil {
			return Status{}, fmt.Errorf("acquire daemon start lock: %w", lockErr)
		}
		if !locked {
			return Status{}, fmt.Errorf("daemon start coordination canceled: %w", ctx.Err())
		}
		defer func() { _ = startLock.Unlock() }()
	}
	existing, readErr := readState(m.StateDir)
	held, probeErr := lifetimeLockHeld(m.StateDir)
	if probeErr != nil {
		return Status{}, probeErr
	}
	if (isNoState(readErr) || (readErr == nil && existing.Lifecycle == LifecycleStopped)) && held {
		return Status{}, errors.New("daemon lifetime owner exists but its state is missing or stopped; state preserved")
	}
	if readErr == nil && existing.Lifecycle != LifecycleStopped {
		status, liveErr := m.liveStatus(ctx, existing)
		if liveErr == nil && status.Health.Live {
			if status.ProjectPath != projectPath {
				return Status{}, fmt.Errorf("daemon already owns %s; stop it before starting %s", status.ProjectPath, projectPath)
			}
			return status, nil
		}
		if held {
			return Status{}, fmt.Errorf("daemon owner for %s is live but its control endpoint is unreachable: %w", existing.ProjectPath, liveErr)
		}
	} else if readErr != nil && !isNoState(readErr) {
		return Status{}, readErr
	}

	nonce, err := randomSecret(16)
	if err != nil {
		return Status{}, err
	}
	token, err := randomSecret(32)
	if err != nil {
		return Status{}, err
	}
	logFile, logPath, err := openDaemonLog(m.StateDir)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = logFile.Close() }()
	startedAt := time.Now().UTC()
	initial := diskState{
		Status: Status{
			Lifecycle:   LifecycleStarting,
			ProjectPath: projectPath,
			StartedAt:   startedAt,
			LogPath:     logPath,
			Health:      Health{},
		},
		Nonce: nonce,
		Token: token,
	}
	if err := writeState(m.StateDir, initial); err != nil {
		return Status{}, err
	}

	child := exec.Command(m.Executable, "daemon", "_run", projectPath)
	child.Env = append(os.Environ(), envStateDir+"="+m.StateDir, envNonce+"="+nonce, envToken+"="+token)
	child.Stdin = nil
	// Bootstrap errors and panics still reach the durable log. Once Run starts,
	// structured daemon logging uses the size-aware rotating writer.
	child.Stdout = logFile
	child.Stderr = logFile
	configureDetached(child)
	if err := child.Start(); err != nil {
		initial.Lifecycle = LifecycleFailed
		initial.Health.LastError = err.Error()
		_ = writeStateIfNonce(m.StateDir, nonce, initial)
		return Status{}, fmt.Errorf("launch daemon: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, m.StartTimeout)
	defer cancel()
	status, err := m.waitReady(waitCtx, nonce)
	if err != nil {
		_ = child.Process.Kill()
		initial.PID = child.Process.Pid
		initial.Lifecycle = LifecycleFailed
		initial.Health.LastError = err.Error()
		_ = writeStateIfNonce(m.StateDir, nonce, initial)
		_ = child.Wait()
		return Status{}, err
	}
	if err := child.Process.Release(); err != nil {
		return Status{}, fmt.Errorf("release daemon process handle: %w", err)
	}
	return status, nil
}

// Status authenticates the persisted endpoint before reporting a live owner.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if err := m.defaults(); err != nil {
		return Status{}, err
	}
	state, err := readState(m.StateDir)
	if isNoState(err) {
		held, probeErr := lifetimeLockHeld(m.StateDir)
		if probeErr != nil {
			return Status{}, probeErr
		}
		if held {
			return Status{}, errors.New("daemon lifetime owner exists but its state is missing")
		}
		return stoppedStatus(), nil
	}
	if err != nil {
		return Status{}, err
	}
	if state.Lifecycle == LifecycleStopped {
		held, probeErr := lifetimeLockHeld(m.StateDir)
		if probeErr != nil {
			return Status{}, probeErr
		}
		if held {
			return Status{}, errors.New("daemon is still releasing its lifetime ownership")
		}
		return state.Status, nil
	}
	status, liveErr := m.liveStatus(ctx, state)
	if liveErr == nil {
		return status, nil
	}
	held, err := lifetimeLockHeld(m.StateDir)
	if err != nil {
		return Status{}, err
	}
	if held {
		return Status{}, fmt.Errorf("daemon for %s is live but unreachable: %w", state.ProjectPath, liveErr)
	}
	state.Lifecycle = LifecycleFailed
	state.Health.Live = false
	state.Health.WatchReady = false
	state.Health.IndexCurrent = false
	state.Health.LastError = "daemon process is not running"
	return state.Status, nil
}

// Stop requests authenticated graceful shutdown. It never signals a PID.
func (m *Manager) Stop(ctx context.Context) (Status, error) {
	return m.stop(ctx, false)
}

func (m *Manager) stop(ctx context.Context, lockHeld bool) (Status, error) {
	if err := m.defaults(); err != nil {
		return Status{}, err
	}
	if !lockHeld {
		startLock := flock.New(filepath.Join(m.StateDir, startLockName))
		locked, lockErr := startLock.TryLockContext(ctx, 25*time.Millisecond)
		if lockErr != nil {
			return Status{}, fmt.Errorf("acquire daemon stop lock: %w", lockErr)
		}
		if !locked {
			return Status{}, fmt.Errorf("daemon stop coordination canceled: %w", ctx.Err())
		}
		defer func() { _ = startLock.Unlock() }()
	}
	state, err := readState(m.StateDir)
	if isNoState(err) {
		held, probeErr := lifetimeLockHeld(m.StateDir)
		if probeErr != nil {
			return Status{}, probeErr
		}
		if held {
			return Status{}, errors.New("daemon lifetime owner exists but its state is missing; cannot authenticate stop")
		}
		return stoppedStatus(), nil
	}
	if err != nil {
		return Status{}, err
	}
	if state.Lifecycle == LifecycleStopped {
		held, probeErr := lifetimeLockHeld(m.StateDir)
		if probeErr != nil {
			return Status{}, probeErr
		}
		if held {
			return Status{}, errors.New("daemon is still releasing its lifetime ownership")
		}
		return state.Status, nil
	}
	if err := m.controlRequest(ctx, state, http.MethodPost, "/control/v1/stop", nil); err != nil {
		held, probeErr := lifetimeLockHeld(m.StateDir)
		if probeErr != nil {
			return Status{}, probeErr
		}
		if held {
			return Status{}, fmt.Errorf("daemon for %s is live but unreachable; state preserved: %w", state.ProjectPath, err)
		}
		state.Lifecycle = LifecycleStopped
		state.StoppedAt = time.Now().UTC()
		state.Health = Health{}
		state.Endpoint = ""
		state.PID = 0
		if err := writeState(m.StateDir, state); err != nil {
			return Status{}, err
		}
		return state.Status, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return m.waitStopped(waitCtx, state.Nonce)
}

// Restart stops the current owner and starts projectPath. An empty path reuses
// the current daemon's project.
func (m *Manager) Restart(ctx context.Context, projectPath string) (Status, error) {
	if projectPath == "" {
		current, err := m.Status(ctx)
		if err != nil {
			return Status{}, err
		}
		projectPath = current.ProjectPath
		if projectPath == "" {
			return Status{}, errors.New("no daemon project to restart; provide a path")
		}
	}
	canonical, err := canonicalProjectPath(projectPath)
	if err != nil {
		return Status{}, err
	}
	if !indexer.IsInitialized(canonical) {
		return Status{}, fmt.Errorf("CodeGraph not initialized in %s; run 'codegrapher init' there first", canonical)
	}
	if err := m.defaults(); err != nil {
		return Status{}, err
	}
	startLock := flock.New(filepath.Join(m.StateDir, startLockName))
	locked, err := startLock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !locked {
		return Status{}, fmt.Errorf("acquire daemon restart lock: %w", err)
	}
	defer func() { _ = startLock.Unlock() }()
	if _, err := m.stop(ctx, true); err != nil {
		return Status{}, err
	}
	return m.start(ctx, canonical, true)
}

func (m *Manager) waitReady(ctx context.Context, nonce string) (Status, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := readState(m.StateDir)
		if err == nil && state.Nonce == nonce {
			if state.Lifecycle == LifecycleFailed {
				return Status{}, fmt.Errorf("daemon startup failed: %s", state.Health.LastError)
			}
			if state.Endpoint != "" {
				status, statusErr := m.liveStatus(ctx, state)
				if statusErr == nil && status.Lifecycle == LifecycleReady && status.Health.WatchReady && status.Health.IndexCurrent {
					return status, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return Status{}, fmt.Errorf("daemon did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (m *Manager) waitStopped(ctx context.Context, nonce string) (Status, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := readState(m.StateDir)
		if err == nil && state.Nonce == nonce && state.Lifecycle == LifecycleFailed {
			return state.Status, fmt.Errorf("daemon stopped with failure: %s", state.Health.LastError)
		}
		if err == nil && state.Nonce == nonce && state.Lifecycle == LifecycleStopped {
			held, probeErr := lifetimeLockHeld(m.StateDir)
			if probeErr != nil {
				return Status{}, probeErr
			}
			if !held {
				return state.Status, nil
			}
		}
		select {
		case <-ctx.Done():
			return Status{}, fmt.Errorf("daemon did not stop: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (m *Manager) liveStatus(ctx context.Context, state diskState) (Status, error) {
	var status Status
	if state.Endpoint == "" {
		return status, errors.New("control endpoint is not published")
	}
	if err := m.controlRequest(ctx, state, http.MethodGet, "/control/v1/status", &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (m *Manager) controlRequest(ctx context.Context, state diskState, method, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(state.Endpoint, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+state.Token)
	req.Header.Set("X-CodeGrapher-Nonce", state.Nonce)
	response, err := m.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("control endpoint returned %s", response.Status)
	}
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			return fmt.Errorf("decode control response: %w", err)
		}
	}
	return nil
}

func lifetimeLockHeld(dir string) (bool, error) {
	lock := flock.New(filepath.Join(dir, lifetimeLockName))
	acquired, err := lock.TryLock()
	if err != nil {
		return false, fmt.Errorf("probe daemon lifetime lock: %w", err)
	}
	if acquired {
		if err := lock.Unlock(); err != nil {
			return false, fmt.Errorf("release daemon lifetime probe: %w", err)
		}
		return false, nil
	}
	return true, nil
}

func canonicalProjectPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if os.IsNotExist(err) {
		return "", fmt.Errorf("project path does not exist: %s", abs)
	}
	return abs, nil
}

func writeStateIfNonce(dir, nonce string, state diskState) error {
	current, err := readState(dir)
	if err != nil {
		return err
	}
	if current.Nonce != nonce {
		return errors.New("daemon state ownership changed")
	}
	return writeState(dir, state)
}

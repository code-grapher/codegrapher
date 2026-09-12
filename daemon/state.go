// Package daemon owns CodeGrapher's single-user background freshness process.
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateSchemaVersion = 1
	controlAPIVersion  = 1
	stateFileName      = "state.json"
	startLockName      = "start.lock"
	lifetimeLockName   = "lifetime.lock"
	logFileName        = "daemon.log"
	maxLogSize         = 5 << 20
)

// Lifecycle is the durable daemon state machine.
type Lifecycle string

const (
	LifecycleStarting Lifecycle = "starting"
	LifecycleReady    Lifecycle = "ready"
	LifecycleDegraded Lifecycle = "degraded"
	LifecycleStopping Lifecycle = "stopping"
	LifecycleStopped  Lifecycle = "stopped"
	LifecycleFailed   Lifecycle = "failed"
)

// Health separates process liveness, native watch coverage, and graph currency.
type Health struct {
	Live                  bool      `json:"live"`
	WatchReady            bool      `json:"watchReady"`
	IndexCurrent          bool      `json:"indexCurrent"`
	PendingDirtyPaths     int       `json:"pendingDirtyPaths"`
	WholeTreeDirty        bool      `json:"wholeTreeDirty"`
	AcceptedGeneration    uint64    `json:"acceptedGeneration"`
	CompletedGeneration   uint64    `json:"completedGeneration"`
	LastSuccessfulUpdate  time.Time `json:"lastSuccessfulUpdate,omitempty"`
	LastReconciliation    time.Time `json:"lastReconciliation,omitempty"`
	LastOperationDuration string    `json:"lastOperationDuration,omitempty"`
	LastError             string    `json:"lastError,omitempty"`
}

// Status is safe to print or encode: lifecycle credentials are never exposed.
type Status struct {
	SchemaVersion     int       `json:"schemaVersion"`
	ControlAPIVersion int       `json:"controlApiVersion"`
	Lifecycle         Lifecycle `json:"lifecycle"`
	PID               int       `json:"pid,omitempty"`
	ProjectPath       string    `json:"projectPath,omitempty"`
	Endpoint          string    `json:"endpoint,omitempty"`
	StartedAt         time.Time `json:"startedAt,omitempty"`
	ReadyAt           time.Time `json:"readyAt,omitempty"`
	StoppedAt         time.Time `json:"stoppedAt,omitempty"`
	LogPath           string    `json:"logPath,omitempty"`
	Health            Health    `json:"health"`
}

type diskState struct {
	Status
	Nonce string `json:"nonce,omitempty"`
	Token string `json:"token,omitempty"`
}

func defaultStateDir() (string, error) {
	if override := os.Getenv("CODEGRAPH_DAEMON_DIR"); override != "" {
		return filepath.Abs(override)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, "codegrapher", "daemon"), nil
}

func ensureStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create daemon state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("protect daemon state directory: %w", err)
	}
	if err := protectUserOnly(dir); err != nil {
		return fmt.Errorf("protect daemon state directory ownership: %w", err)
	}
	return nil
}

func readState(dir string) (diskState, error) {
	data, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		return diskState{}, err
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		return diskState{}, fmt.Errorf("decode daemon state: %w", err)
	}
	return state, nil
}

func writeState(dir string, state diskState) error {
	if err := ensureStateDir(dir); err != nil {
		return err
	}
	state.SchemaVersion = stateSchemaVersion
	state.ControlAPIVersion = controlAPIVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode daemon state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create daemon state transaction: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect daemon state transaction: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write daemon state transaction: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync daemon state transaction: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close daemon state transaction: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, stateFileName)); err != nil {
		return fmt.Errorf("commit daemon state transaction: %w", err)
	}
	if err := protectUserOnly(filepath.Join(dir, stateFileName)); err != nil {
		return fmt.Errorf("protect committed daemon state: %w", err)
	}
	return nil
}

func randomSecret(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate daemon credential: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func stoppedStatus() Status {
	return Status{
		SchemaVersion:     stateSchemaVersion,
		ControlAPIVersion: controlAPIVersion,
		Lifecycle:         LifecycleStopped,
		Health:            Health{},
	}
}

func isNoState(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

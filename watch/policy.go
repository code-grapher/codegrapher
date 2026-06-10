// Package watch provides file-system watching with debounced sync callbacks.
// Ported from src/sync/watcher.ts and src/sync/watch-policy.ts of
// github.com/colbymchenry/codegraph (MIT).
package watch

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// wslCache caches the WSL detection result so /proc/version is read at most once.
var wslCache struct {
	once  sync.Once
	value bool
}

// wslMntRe matches WSL Windows-drive mounts like /mnt/c or /mnt/d/project.
// Only single-letter drive letters are matched; /mnt/wsl/... is excluded.
var wslMntRe = regexp.MustCompile(`(?i)^/mnt/[a-z](/|$)`)

// DetectWSL reports whether the current process is running under WSL. The
// result is cached after the first call.
func DetectWSL() bool {
	wslCache.once.Do(func() {
		wslCache.value = detectWSLOnce()
	})
	return wslCache.value
}

func detectWSLOnce() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

// ResetWSLCacheForTests resets the cached WSL detection so tests can control
// the outcome deterministically. Never call outside tests.
func ResetWSLCacheForTests() {
	wslCache.once = sync.Once{}
	wslCache.value = false
}

// WatchProbe holds injectable inputs for [WatchDisabledReason] so tests can
// control the decision without touching real env vars or /proc/version.
type WatchProbe struct {
	// Env overrides os.Environ lookups. nil means use os.Getenv.
	Env map[string]string
	// IsWSL overrides the WSL detection when non-nil.
	IsWSL *bool
}

func (p WatchProbe) getenv(key string) string {
	if p.Env != nil {
		return p.Env[key]
	}
	return os.Getenv(key)
}

func (p WatchProbe) isWSL() bool {
	if p.IsWSL != nil {
		return *p.IsWSL
	}
	return DetectWSL()
}

// WatchDisabledReason returns a human-readable reason why file watching
// should be skipped for projectRoot, or "" when watching should proceed.
//
// Precedence (first match wins):
//  1. CODEGRAPH_NO_WATCH=1    → off (explicit opt-out always wins)
//  2. CODEGRAPH_FORCE_WATCH=1 → on  (overrides auto-detection)
//  3. WSL2 + /mnt/* drive     → off (recursive fs.watch is too slow; #199)
func WatchDisabledReason(projectRoot string, probe WatchProbe) string {
	if probe.getenv("CODEGRAPH_NO_WATCH") == "1" {
		return "CODEGRAPH_NO_WATCH=1 is set"
	}
	if probe.getenv("CODEGRAPH_FORCE_WATCH") == "1" {
		return ""
	}
	if probe.isWSL() && wslMntRe.MatchString(projectRoot) {
		return "project is on a WSL2 /mnt/ drive, where recursive fs.watch is too slow to be reliable"
	}
	return ""
}

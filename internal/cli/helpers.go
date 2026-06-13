package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/specscore/codegrapher/indexer"
)

// resolveArg returns the first arg as an absolute path, or cwd if no args.
// Also walks up parent directories to find the nearest initialized CodeGraph
// project, mirroring resolveProjectPath in the original codegraph.ts.
// splitCSV splits a comma-separated value into trimmed, non-empty fields.
// An empty or whitespace-only input yields nil (meaning "all scopes").
func splitCSV(csv string) []string {
	var out []string
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func resolveArg(args []string, idx int) string {
	var raw string
	if idx < len(args) && args[idx] != "" {
		raw = args[idx]
	} else {
		raw, _ = os.Getwd()
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return raw
	}
	return findNearestOrReturn(abs)
}

// findNearestOrReturn walks up from startPath looking for an initialized
// CodeGraph project. Returns startPath if none is found.
func findNearestOrReturn(startPath string) string {
	if indexer.IsInitialized(startPath) {
		return startPath
	}
	found := indexer.FindNearestCodeGraphRoot(startPath)
	if found != "" {
		return found
	}
	return startPath
}

// verboseProgress returns an OnProgress callback that logs timestamped lines.
func verboseProgress() func(indexer.IndexProgress) {
	startTime := time.Now()
	lastPhase := ""
	lastPct := -1
	return func(p indexer.IndexProgress) {
		elapsed := time.Since(startTime).Seconds()
		phase := string(p.Phase)
		if phase != lastPhase {
			lastPhase = phase
			lastPct = -1
			fmt.Printf("[%.1fs] Phase: %s\n", elapsed, phase)
		}
		if p.Total > 0 {
			pct := 0
			if p.Total > 0 {
				pct = (p.Current * 100) / p.Total
			}
			if pct >= lastPct+5 || p.Current == p.Total {
				lastPct = pct
				file := ""
				if p.CurrentFile != "" {
					file = " — " + p.CurrentFile
				}
				fmt.Printf("[%.1fs]   %d/%d (%d%%)%s\n", elapsed, p.Current, p.Total, pct, file)
			}
		} else if p.Current > 0 && p.Current%1000 == 0 {
			fmt.Printf("[%.1fs]   %s files found\n", elapsed, formatNumber(p.Current))
		}
	}
}

// progressLabel converts an IndexProgress into a short spinner label.
func progressLabel(p indexer.IndexProgress) string {
	switch p.Phase {
	case indexer.PhaseScanning:
		if p.Current > 0 {
			return fmt.Sprintf("Scanning… %s files", formatNumber(p.Current))
		}
		return "Scanning…"
	case indexer.PhaseParsing:
		if p.Total > 0 {
			pct := (p.Current * 100) / p.Total
			return fmt.Sprintf("Parsing… %d/%d (%d%%)", p.Current, p.Total, pct)
		}
		return "Parsing…"
	case indexer.PhaseStoring:
		return "Storing…"
	case indexer.PhaseResolving:
		if p.Total > 0 {
			return fmt.Sprintf("Resolving… %d/%d", p.Current, p.Total)
		}
		return "Resolving…"
	}
	return string(p.Phase) + "…"
}

// ── spinner ──────────────────────────────────────────────────────────────────

type spinner struct {
	frames []string
	i      int
	label  string
	done   chan struct{}
}

func newSpinner() *spinner {
	return &spinner{
		frames: []string{"⠋", "⠙", "⠸", "⠴", "⠦", "⠇"},
		done:   make(chan struct{}),
	}
}

func (s *spinner) start(label string) {
	s.label = label
	go func() {
		tick := time.NewTicker(80 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-s.done:
				fmt.Fprintf(os.Stderr, "\r\x1b[K") // clear line
				return
			case <-tick.C:
				frame := s.frames[s.i%len(s.frames)]
				s.i++
				fmt.Fprintf(os.Stderr, "\r\x1b[2m%s\x1b[0m %s", frame, s.label)
			}
		}
	}()
}

func (s *spinner) update(label string) {
	s.label = label
}

func (s *spinner) stop() {
	close(s.done)
}

// globToRegex converts a simple glob pattern (*, **, ?) to a regexp string.
func globToRegex(pattern string) string {
	var b strings.Builder
	b.WriteRune('^')
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			b.WriteRune('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteRune('$')
	return b.String()
}

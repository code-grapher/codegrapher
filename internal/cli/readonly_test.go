package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

// captureOutput runs fn while stdout and stderr are redirected, returning both.
func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	done := make(chan [2]string)
	go func() {
		o, _ := io.ReadAll(outR)
		e, _ := io.ReadAll(errR)
		done <- [2]string{string(o), string(e)}
	}()
	func() {
		defer func() {
			os.Stdout, os.Stderr = oldOut, oldErr
			_ = outW.Close()
			_ = errW.Close()
		}()
		fn()
	}()
	res := <-done
	return res[0], res[1]
}

// indexedFixture copies go-small, indexes it, closes the indexer, and returns
// the project path.
func indexedFixture(t *testing.T) string {
	t.Helper()
	root, idx := initFixture(t)
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func scopeDBs(t *testing.T, root string) []string {
	t.Helper()
	dbs, err := filepath.Glob(filepath.Join(indexer.GetCodeGraphDir(root), "codegraph-*.db"))
	if err != nil || len(dbs) == 0 {
		t.Fatalf("no scope databases: %v %v", dbs, err)
	}
	return dbs
}

func rawExec(t *testing.T, dbPath string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

type fileState struct {
	data  []byte
	mtime int64
}

func snapshotDBs(t *testing.T, root string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	for _, db := range scopeDBs(t, root) {
		data, err := os.ReadFile(db)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(db)
		if err != nil {
			t.Fatal(err)
		}
		out[db] = fileState{data: data, mtime: fi.ModTime().UnixNano()}
	}
	return out
}

func assertDBsUnchanged(t *testing.T, root string, before map[string]fileState) {
	t.Helper()
	for db, was := range before {
		now, err := os.ReadFile(db)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(was.data, now) {
			t.Errorf("%s: bytes changed", filepath.Base(db))
		}
		fi, _ := os.Stat(db)
		if fi.ModTime().UnixNano() != was.mtime {
			t.Errorf("%s: mtime changed", filepath.Base(db))
		}
	}
	for _, side := range []string{"-wal", "-shm", "-journal"} {
		matches, _ := filepath.Glob(filepath.Join(indexer.GetCodeGraphDir(root), "*"+side))
		if len(matches) != 0 {
			t.Errorf("unexpected side files %v", matches)
		}
	}
}

func runCmd(t *testing.T, cmd *cobra.Command, args ...string) (stdout string, err error) {
	t.Helper()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	o, _ := captureOutput(t, func() { err = cmd.Execute() })
	return o + buf.String(), err
}

func decodeStatus(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, out)
	}
	return m
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	return ExitCodeFor(err)
}

// ── exit codes ───────────────────────────────────────────────────────────────

func TestExitCodeAndReport(t *testing.T) {
	needs := &store.NeedsUpgradeError{Path: "x.db", Reason: "legacy WAL journal mode"}
	if got := ExitCodeFor(fmt.Errorf("open: %w", needs)); got != ExitNeedsUpgrade {
		t.Errorf("needs-upgrade code = %d", got)
	}
	if got := ExitCodeFor(errors.New("boom")); got != 1 {
		t.Errorf("generic code = %d", got)
	}
	if got := ExitCodeFor(&ExitError{Code: 7}); got != 7 {
		t.Errorf("ExitError code = %d", got)
	}
	if (&ExitError{Code: 7}).Error() == "" {
		t.Error("ExitError message empty")
	}

	var buf bytes.Buffer
	if code := ReportAndExitCode(&ExitError{Code: 3}, &buf); code != 3 || buf.Len() != 0 {
		t.Errorf("ExitError: code %d, printed %q", code, buf.String())
	}
	if code := ReportAndExitCode(needs, &buf); code != ExitNeedsUpgrade || !strings.Contains(buf.String(), "codegrapher sync") || !strings.Contains(buf.String(), "--refresh") {
		t.Errorf("needs-upgrade: code %d, printed %q", code, buf.String())
	}
	buf.Reset()
	if code := ReportAndExitCode(errors.New("boom"), &buf); code != 1 || !strings.Contains(buf.String(), "boom") {
		t.Errorf("generic: code %d, printed %q", code, buf.String())
	}
}

func TestFailOpenExitsWithTypedCode(t *testing.T) {
	var got int
	old := exitProcess
	exitProcess = func(code int) { got = code }
	defer func() { exitProcess = old }()

	_, stderr := captureOutput(t, func() { failOpen(&store.NeedsUpgradeError{Path: "x", Reason: "r"}) })
	if got != ExitNeedsUpgrade || !strings.Contains(stderr, "Failed to open index") {
		t.Errorf("needs-upgrade: code %d stderr %q", got, stderr)
	}
	captureOutput(t, func() { failOpen(errors.New("boom")) })
	if got != 1 {
		t.Errorf("generic code = %d", got)
	}
}

// ── --refresh ────────────────────────────────────────────────────────────────

func TestEveryReadCommandHasRefreshFlag(t *testing.T) {
	for _, cmd := range []*cobra.Command{
		newStatusCmd(), newQueryCmd(), newCallersCmd(), newCalleesCmd(), newImpactCmd(),
		newPathCmd(), newFilesCmd(), newAffectedCmd(), newContextCmd(), newStacktraceCmd(),
		newTraceCmd(), newServeCmd(), newNodeCmd(), newCoverageTargetsCmd(),
	} {
		if cmd.Flags().Lookup("refresh") == nil {
			t.Errorf("%s lacks --refresh", cmd.Name())
		}
	}
	if newStatusCmd().Flags().Lookup("no-pending") == nil {
		t.Error("status lacks --no-pending")
	}
}

func TestOpenIndexForReadIsReadOnlyUnlessRefreshed(t *testing.T) {
	root := indexedFixture(t)
	changed := filepath.Join(root, "internal", "store", "store.go")
	f, err := os.OpenFile(changed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\nfunc AddedLater() {}\n")
	_ = f.Close()
	before := snapshotDBs(t, root)

	idx, err := openIndexForRead(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(idx.GetChangedFiles().Modified); n == 0 {
		t.Error("read-only open must not refresh the index")
	}
	_ = idx.Close()
	assertDBsUnchanged(t, root, before)

	idx, res, err := openIndexForReadResult(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if res.FilesModified == 0 {
		t.Errorf("refresh result = %+v", res)
	}
	if ch := idx.GetChangedFiles(); len(ch.Added)+len(ch.Modified)+len(ch.Removed) != 0 {
		t.Errorf("pending after refresh = %+v", ch)
	}
	if !idx.Registry().ReadOnly() {
		t.Error("the answering handle must be read-only")
	}
}

func TestRefreshFailuresAreReported(t *testing.T) {
	if _, err := refreshIndex(t.TempDir()); err == nil || !strings.Contains(err.Error(), "refresh: open index") {
		t.Errorf("uninitialized refresh err = %v", err)
	}

	root := indexedFixture(t)
	f, err := os.OpenFile(filepath.Join(root, "internal", "store", "store.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\nfunc AddedLater() {}\n")
	_ = f.Close()
	// A live writer's lock makes the refresh fail rather than read stale data.
	lockPath := filepath.Join(indexer.GetCodeGraphDir(root), "codegraph.lock")
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getppid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshIndex(root); err == nil || !strings.Contains(err.Error(), "refresh:") {
		t.Errorf("locked refresh err = %v", err)
	}
	if _, err := openIndexForRead(root, true); err == nil {
		t.Error("openIndexForRead must surface a refresh failure")
	}
}

func TestReadCommandsNeedUpgradeError(t *testing.T) {
	root := indexedFixture(t)
	for _, db := range scopeDBs(t, root) {
		rawExec(t, db, "PRAGMA journal_mode=WAL")
	}
	before := snapshotDBs(t, root)
	_, err := openIndexForRead(root, false)
	if !errors.Is(err, store.ErrNeedsUpgrade) {
		t.Fatalf("err = %v", err)
	}
	if exitCode(err) != ExitNeedsUpgrade {
		t.Errorf("exit code = %d", exitCode(err))
	}
	for _, name := range []string{"codegraph-"} {
		_ = name
	}
	// The error names the commands that perform the upgrade.
	if !strings.Contains(err.Error(), "codegrapher sync") || !strings.Contains(err.Error(), "--refresh") {
		t.Errorf("message = %q", err.Error())
	}
	// A RunE-style command surfaces it with the typed code.
	_, err = runCmd(t, newCoverageTargetsCmd(), "--root", root, "--json")
	if exitCode(err) != ExitNeedsUpgrade {
		t.Errorf("coverage targets err = %v", err)
	}
	// Reads left the WAL files exactly as they were.
	for db, was := range before {
		now, _ := os.ReadFile(db)
		if !bytes.Equal(was.data, now) {
			t.Errorf("%s changed", filepath.Base(db))
		}
	}

	// --refresh is the upgrade path: it converts, then the answer is served.
	if _, err := runCmd(t, newCoverageTargetsCmd(), "--root", root, "--json", "--refresh"); err != nil {
		t.Fatalf("--refresh: %v", err)
	}
	if _, err := openIndexForRead(root, false); err != nil {
		t.Errorf("after --refresh: %v", err)
	}
}

// ── status ───────────────────────────────────────────────────────────────────

func TestStatusIsReadOnlyOnFreshIndex(t *testing.T) {
	root := indexedFixture(t)
	before := snapshotDBs(t, root)

	out, err := runCmd(t, newStatusCmd(), "--json", "--path", root)
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	m := decodeStatus(t, out)
	if m["pendingChanges"] == nil {
		t.Error("pendingChanges should be an object by default")
	}
	if m["journalMode"] != "delete" {
		t.Errorf("journalMode = %v", m["journalMode"])
	}
	assertDBsUnchanged(t, root, before)

	out, err = runCmd(t, newStatusCmd(), "--json", "--no-pending", "--path", root)
	if err != nil {
		t.Fatal(err)
	}
	m = decodeStatus(t, out)
	if v, ok := m["pendingChanges"]; !ok || v != nil {
		t.Errorf("--no-pending pendingChanges = %v (present %v), want null", v, ok)
	}
	assertDBsUnchanged(t, root, before)

	// Text output, with and without the scan.
	for _, args := range [][]string{{"--path", root}, {"--no-pending", "--path", root}} {
		out, err := runCmd(t, newStatusCmd(), args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Journal:   delete") {
			t.Errorf("text status lacks journal: %s", out)
		}
	}
	assertDBsUnchanged(t, root, before)
}

func TestStatusPendingScanSkippedByNoPending(t *testing.T) {
	root := indexedFixture(t)
	f, err := os.OpenFile(filepath.Join(root, "internal", "store", "store.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\nfunc AddedLater() {}\n")
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "main.go")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	out, err := runCmd(t, newStatusCmd(), "--path", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Pending Changes:") || !strings.Contains(out, "Modified:") || !strings.Contains(out, "Added:") {
		t.Errorf("text status lacks pending changes: %s", out)
	}
	out, err = runCmd(t, newStatusCmd(), "--path", root, "--no-pending")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Pending Changes:") || !strings.Contains(out, "not checked") {
		t.Errorf("--no-pending output: %s", out)
	}
}

func TestStatusStaleSchemaPrintsWhatItCanAndExitsDistinctly(t *testing.T) {
	root := indexedFixture(t)
	for _, db := range scopeDBs(t, root) {
		rawExec(t, db, "DELETE FROM schema_versions WHERE version >= 9")
	}
	before := snapshotDBs(t, root)

	out, err := runCmd(t, newStatusCmd(), "--json", "--path", root)
	if exitCode(err) != ExitNeedsUpgrade {
		t.Fatalf("exit = %d (%v)", exitCode(err), err)
	}
	m := decodeStatus(t, out)
	if m["upgradeNeeded"] == nil || !strings.Contains(m["upgradeNeeded"].(string), "codegrapher sync") {
		t.Errorf("upgradeNeeded = %v", m["upgradeNeeded"])
	}
	if m["pendingChanges"] != nil {
		t.Errorf("stale status must not scan: pendingChanges = %v", m["pendingChanges"])
	}
	if m["nodeCount"].(float64) == 0 {
		t.Error("stale status should still report what it can (node count)")
	}
	assertDBsUnchanged(t, root, before)

	out, err = runCmd(t, newStatusCmd(), "--path", root)
	if exitCode(err) != ExitNeedsUpgrade {
		t.Fatalf("text exit = %d (%v)", exitCode(err), err)
	}
	if !strings.Contains(out, "index needs upgrade") || !strings.Contains(out, "Files:") {
		t.Errorf("text output: %s", out)
	}
	assertDBsUnchanged(t, root, before)
}

func TestStatusStaleSchemaWithUnreadableTablesStillReportsNotice(t *testing.T) {
	root := indexedFixture(t)
	for _, db := range scopeDBs(t, root) {
		rawExec(t, db, "DELETE FROM schema_versions WHERE version >= 9", "DROP TABLE project_metadata", "ALTER TABLE files RENAME TO files_gone")
	}
	out, err := runCmd(t, newStatusCmd(), "--json", "--path", root)
	if exitCode(err) != ExitNeedsUpgrade {
		t.Fatalf("exit = %d (%v)\n%s", exitCode(err), err, out)
	}
	if m := decodeStatus(t, out); m["upgradeNeeded"] == nil || m["initialized"] != true {
		t.Errorf("status = %v", m)
	}
}

func TestStatusReadFailureOnCurrentIndexIsAnError(t *testing.T) {
	root := indexedFixture(t)
	for _, db := range scopeDBs(t, root) {
		rawExec(t, db, "ALTER TABLE files RENAME TO files_gone")
	}
	_, err := runCmd(t, newStatusCmd(), "--json", "--path", root)
	if exitCode(err) != 1 {
		t.Errorf("exit = %d (%v)", exitCode(err), err)
	}
}

func TestStatusLegacyWALIndexAndRefreshUpgrade(t *testing.T) {
	root := indexedFixture(t)
	dbs := scopeDBs(t, root)
	for _, db := range dbs {
		rawExec(t, db, "PRAGMA journal_mode=WAL")
	}
	before := snapshotDBs(t, root)

	out, err := runCmd(t, newStatusCmd(), "--json", "--path", root)
	if exitCode(err) != ExitNeedsUpgrade {
		t.Fatalf("exit = %d (%v)", exitCode(err), err)
	}
	m := decodeStatus(t, out)
	if !strings.Contains(m["upgradeNeeded"].(string), "WAL") {
		t.Errorf("upgradeNeeded = %v", m["upgradeNeeded"])
	}
	assertDBsUnchanged(t, root, before)

	out, err = runCmd(t, newStatusCmd(), "--json", "--refresh", "--path", root)
	if err != nil {
		t.Fatalf("status --refresh: %v\n%s", err, out)
	}
	if m := decodeStatus(t, out); m["journalMode"] != "delete" || m["upgradeNeeded"] != nil {
		t.Errorf("after --refresh: %v", m)
	}
}

func TestStatusFailures(t *testing.T) {
	root := indexedFixture(t)
	old := refreshIndex
	refreshIndex = func(string) (indexer.SyncResult, error) { return indexer.SyncResult{}, errors.New("sync boom") }
	_, err := runCmd(t, newStatusCmd(), "--refresh", "--path", root)
	refreshIndex = old
	if exitCode(err) != 1 {
		t.Errorf("refresh failure exit = %d (%v)", exitCode(err), err)
	}

	for _, db := range scopeDBs(t, root) {
		if err := os.WriteFile(db, []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err = runCmd(t, newStatusCmd(), "--path", root)
	if exitCode(err) != 1 {
		t.Errorf("corrupt index exit = %d (%v)", exitCode(err), err)
	}
}

func TestStatusWarnsWhenDataDirIsNotIgnored(t *testing.T) {
	root := indexedFixture(t)
	gitRun(t, root, "init", "-q")
	// initFixture's CreateDirectory ran before git init, so nothing ignores it.
	_ = os.Remove(filepath.Join(indexer.GetCodeGraphDir(root), ".gitignore"))
	before := snapshotDBs(t, root)

	out, err := runCmd(t, newStatusCmd(), "--json", "--path", root)
	if err != nil {
		t.Fatal(err)
	}
	warnings, _ := decodeStatus(t, out)["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "not ignored by Git") {
		t.Errorf("warnings = %v", warnings)
	}
	// status only warns: nothing repaired.
	if _, err := os.Stat(filepath.Join(indexer.GetCodeGraphDir(root), ".gitignore")); err == nil {
		t.Error("status repaired .gitignore; it must only warn")
	}
	text, err := runCmd(t, newStatusCmd(), "--path", root)
	if err != nil || !strings.Contains(text, "not ignored by Git") {
		t.Errorf("text status: %v %s", err, text)
	}
	assertDBsUnchanged(t, root, before)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSyncAndInitRepairIgnoreAndWarnWhenTracked(t *testing.T) {
	root := indexedFixture(t)
	gitRun(t, root, "init", "-q")
	_ = os.Remove(filepath.Join(indexer.GetCodeGraphDir(root), ".gitignore"))

	out, err := runCmd(t, newSyncCmd(), root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not ignored") {
		t.Errorf("sync should have repaired silently: %s", out)
	}
	if _, err := os.Stat(filepath.Join(indexer.GetCodeGraphDir(root), ".gitignore")); err != nil {
		t.Errorf("sync did not repair .codegraph/.gitignore: %v", err)
	}

	// A tracked data file is only ever warned about.
	gitRun(t, root, "add", "-f", ".codegraph")
	out, err = runCmd(t, newSyncCmd(), root)
	if err != nil || !strings.Contains(out, "tracked by Git") {
		t.Errorf("sync output: %v %s", err, out)
	}
	if out, err = runCmd(t, newSyncCmd(), "--quiet", root); err != nil || strings.Contains(out, "tracked by Git") {
		t.Errorf("quiet sync printed warnings: %v %s", err, out)
	}

	// init (fresh project) and sync --init print the warnings from a fresh repair.
	fresh := t.TempDir()
	if err := copyDir(filepath.Join("..", "..", "testdata", "fixtures", "go-small"), fresh); err != nil {
		t.Fatal(err)
	}
	gitRun(t, fresh, "init", "-q")
	if out, err := runCmd(t, newInitCmd(), fresh); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	fresh2 := t.TempDir()
	if err := copyDir(filepath.Join("..", "..", "testdata", "fixtures", "go-small"), fresh2); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(t, newSyncCmd(), "--init", fresh2); err != nil {
		t.Fatalf("sync --init: %v\n%s", err, out)
	}
}

// ── node / coverage targets no longer refresh implicitly ─────────────────────

func TestNodeDoesNotRefreshImplicitlyAndLabelsStale(t *testing.T) {
	root := indexedFixture(t)
	storeGo := filepath.Join(root, "internal", "store", "store.go")
	data, err := os.ReadFile(storeGo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storeGo, append(data, []byte("\n// edited after indexing\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotDBs(t, root)

	out, err := runCmd(t, newNodeCmd(), "Len", "--source=inline", "--format", "json", "--relations", "--path", root)
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var results []NodeResult
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 1 {
		t.Fatalf("results %v %v\n%s", results, err, out)
	}
	r := results[0]
	if !r.Freshness.Stale || r.Freshness.Refreshed || r.Source != "" || !strings.Contains(r.Hint, "--refresh") {
		t.Errorf("stale node result = %+v", r)
	}
	text, err := runCmd(t, newNodeCmd(), "Len", "--source", "--path", root)
	if err != nil || !strings.Contains(text, "stale") {
		t.Errorf("text: %v %s", err, text)
	}
	assertDBsUnchanged(t, root, before)

	// With --refresh the same call re-indexes first and returns current source.
	out, err = runCmd(t, newNodeCmd(), "Len", "--source=inline", "--format", "json", "--refresh", "--path", root)
	if err != nil {
		t.Fatalf("node --refresh: %v\n%s", err, out)
	}
	results = nil
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Freshness.Stale || !r.Freshness.Refreshed || r.Source == "" {
		t.Errorf("refreshed node result = %+v", r)
	}
	// Without --source nothing is checked or refreshed.
	if f := nodeFreshnessFor(false, indexer.SyncResult{FilesModified: 1}); f.Refreshed {
		t.Errorf("freshness without refresh = %+v", f)
	}
}

// ── trace is read-only ───────────────────────────────────────────────────────

func TestSplitTraceStores(t *testing.T) {
	root := indexedFixture(t)
	idx, err := indexer.OpenReadOnly(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	stores := idx.Registry().Stores()

	projection, sources, err := splitTraceStores(stores)
	if err != nil || projection == nil || len(sources) == 0 {
		t.Fatalf("split = %v %d %v", projection, len(sources), err)
	}
	for _, s := range sources {
		if s == projection {
			t.Error("projection listed among sources")
		}
	}

	without := map[scope.Scope]*store.Store{}
	for sc, s := range stores {
		if sc.Language != model.Language("trace") {
			without[sc] = s
		}
	}
	if _, _, err := splitTraceStores(without); err == nil || !strings.Contains(err.Error(), "--refresh") {
		t.Errorf("missing projection err = %v", err)
	}
}

func TestTraceCommandReadsProjectionWithoutWriting(t *testing.T) {
	root := indexedFixture(t)
	before := snapshotDBs(t, root)
	out, err := runCmd(t, newTraceCmd(), "no-such-feature", "--root", root, "--json")
	if err != nil && !strings.Contains(err.Error(), "no-such-feature") && !strings.Contains(err.Error(), "trace") {
		t.Fatalf("trace: %v\n%s", err, out)
	}
	assertDBsUnchanged(t, root, before)

	// Stale/WAL index: typed error, not a silent rebuild.
	for _, db := range scopeDBs(t, root) {
		rawExec(t, db, "PRAGMA journal_mode=WAL")
	}
	_, err = runCmd(t, newTraceCmd(), "x", "--root", root)
	if exitCode(err) != ExitNeedsUpgrade {
		t.Errorf("trace on WAL index: %v", err)
	}
}

// ── review fixes ─────────────────────────────────────────────────────────────

func TestTraceRefreshBuildsMissingProjection(t *testing.T) {
	root := indexedFixture(t)
	dir := indexer.GetCodeGraphDir(root)
	traceDBs, _ := filepath.Glob(filepath.Join(dir, "codegraph-trace-*.db"))
	if len(traceDBs) != 1 {
		t.Fatalf("trace dbs = %v", traceDBs)
	}
	if err := os.Remove(traceDBs[0]); err != nil {
		t.Fatal(err)
	}

	_, err := runCmd(t, newTraceCmd(), "x", "--root", root, "--json")
	if err == nil || !strings.Contains(err.Error(), "--refresh") {
		t.Fatalf("without --refresh: %v", err)
	}
	if _, statErr := os.Stat(traceDBs[0]); statErr == nil {
		t.Fatal("a read-only trace must not create the projection")
	}

	// Nothing changed on disk, yet --refresh must still build the projection.
	_, err = runCmd(t, newTraceCmd(), "x", "--root", root, "--json", "--refresh")
	if err != nil && strings.Contains(err.Error(), "not built") {
		t.Fatalf("--refresh left the projection unbuilt: %v", err)
	}
	if _, statErr := os.Stat(traceDBs[0]); statErr != nil {
		t.Fatalf("--refresh did not create the trace db: %v", statErr)
	}
	// Idempotent: a second refresh with a complete projection is a no-op.
	if _, err := refreshIndex(root); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureTraceProjectionFailsWhenIndexLocked(t *testing.T) {
	root := indexedFixture(t)
	dir := indexer.GetCodeGraphDir(root)
	traceDBs, _ := filepath.Glob(filepath.Join(dir, "codegraph-trace-*.db"))
	_ = os.Remove(traceDBs[0])
	if err := os.WriteFile(filepath.Join(dir, "codegraph.lock"), []byte(strconv.Itoa(os.Getppid())), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := indexer.Open(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if _, err := idx.RefreshForRead(indexer.Options{}); err == nil || !strings.Contains(err.Error(), "trace projection") {
		t.Errorf("err = %v", err)
	}
}

func TestStaleSourceIsWithheldNotFatalInEveryMode(t *testing.T) {
	root := indexedFixture(t)
	cache := filepath.Join(root, "internal", "store", "cache.go")
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, append(data, []byte("\n// edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotDBs(t, root)

	// node: default footer, explicit inline and JSON all exit 0 and name --refresh.
	for _, args := range [][]string{{"--source"}, {"--source=footer"}, {"--source=inline"}, {"--source=inline", "--format", "json"}} {
		out, err := runCmd(t, newNodeCmd(), append([]string{"Warm"}, append(args, "--path", root)...)...)
		if err != nil {
			t.Fatalf("node %v: %v\n%s", args, err, out)
		}
		if !strings.Contains(out, "--refresh") || !strings.Contains(out, "stale") {
			t.Errorf("node %v output lacks stale/--refresh: %s", args, out)
		}
		if strings.Contains(out, "func (c *Cache) Warm") {
			t.Errorf("node %v leaked stale source", args)
		}
	}

	// path: source withheld, exit 0, stale reported in text and JSON.
	for _, args := range [][]string{{"--source"}, {"--source=inline"}, {"--source=inline", "--format", "json"}} {
		out, err := runCmd(t, newPathCmd(), append([]string{"Warm", "Set"}, append(args, "--path", root)...)...)
		if err != nil {
			t.Fatalf("path %v: %v\n%s", args, err, out)
		}
		if !strings.Contains(out, "stale") || !strings.Contains(out, "--refresh") {
			t.Errorf("path %v output: %s", args, out)
		}
	}

	// stacktrace: frames kept, source withheld, result marked stale.
	idx, err := indexer.OpenReadOnly(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	frame := "example.com/go-small/internal/store.(*Cache).Warm(...)\n\t" + filepath.Join(root, "internal/store/cache.go") + ":24 +0x1"
	res, err := mapStacktrace(idx, nil, frame, true, NodeFreshness{})
	if err != nil || !res.Freshness.Stale || len(res.Frames) != 1 || res.Frames[0].Source != "" || !strings.Contains(res.Frames[0].Hint, "--refresh") {
		t.Fatalf("stacktrace result = %+v, %v", res, err)
	}
	var out bytes.Buffer
	if err := printStacktraceMarkdown(&out, res, true); err != nil || !strings.Contains(out.String(), "stale") {
		t.Errorf("stacktrace text: %v %s", err, out.String())
	}
	assertDBsUnchanged(t, root, before)

	// The shared reader names the flag for the callers that still fail (context).
	matches, err := findNodeMatches(idx.StoresFiltered(nil), "Warm")
	if err != nil || len(matches) == 0 {
		t.Fatal(err)
	}
	if _, err := readVerifiedIndexedNodeSource(root, matches[0]); !errors.Is(err, errStaleSource) || !strings.Contains(err.Error(), "--refresh") {
		t.Errorf("readVerified err = %v", err)
	}
	// An empty indexed range is also reported as stale with the flag.
	empty := matches[0]
	empty.node.StartLine, empty.node.EndLine = 100000, 100001
	empty.node.FilePath = "internal/store/store.go"
	if _, err := readVerifiedIndexedNodeSource(root, empty); err == nil || !strings.Contains(err.Error(), "--refresh") {
		t.Errorf("empty-range err = %v", err)
	}
}

func TestEmptyIndexFileIsCorruptNotUpgrade(t *testing.T) {
	root := indexedFixture(t)
	for _, db := range scopeDBs(t, root) {
		if err := os.Truncate(db, 0); err != nil {
			t.Fatal(err)
		}
	}
	_, err := openIndexForRead(root, false)
	if !errors.Is(err, store.ErrCorrupt) || errors.Is(err, store.ErrNeedsUpgrade) {
		t.Fatalf("read-only open err = %v", err)
	}
	// The read-write path (what sync and --refresh use) says the same, not "no such table".
	_, err = refreshIndex(root)
	if !errors.Is(err, store.ErrCorrupt) || !strings.Contains(err.Error(), "codegrapher init") {
		t.Errorf("refresh err = %v", err)
	}
}

func TestCorruptionAfterOpenGetsRebuildAdvice(t *testing.T) {
	_, stderr := captureOutput(t, func() { printError("Search failed: database disk image is malformed (11)") })
	if !strings.Contains(stderr, "codegrapher uninit") {
		t.Errorf("printError lacks advice: %q", stderr)
	}
	_, stderr = captureOutput(t, func() { printError("Search failed: " + store.RebuildAdvice + " malformed") })
	if strings.Count(stderr, "codegrapher uninit") != 1 {
		t.Errorf("advice duplicated: %q", stderr)
	}
	_, stderr = captureOutput(t, func() { printError("plain failure") })
	if strings.Contains(stderr, "uninit") {
		t.Errorf("unrelated error got advice: %q", stderr)
	}
	var buf bytes.Buffer
	code := ReportAndExitCode(errors.New("callers: file is not a database (26)"), &buf)
	if code != 1 || !strings.Contains(buf.String(), "codegrapher init") {
		t.Errorf("ReportAndExitCode: %d %q", code, buf.String())
	}
}

func TestServeRefreshWithWatchIsAnError(t *testing.T) {
	root := indexedFixture(t)
	for _, args := range [][]string{{"--refresh", "--path", root}, {"--watch", "--refresh", "--path", root}} {
		_, err := runCmd(t, newServeCmd(), args...)
		if err == nil || !strings.Contains(err.Error(), "--refresh has no effect") {
			t.Errorf("serve %v: err = %v", args, err)
		}
	}
}

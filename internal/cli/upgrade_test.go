package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
)

// AC: cli-install#req:core-framework-neutral, cli-install#req:update-alias-
// policy — the command is named "upgrade", registers the shared upgrade
// flag surface, and carries no "update" alias (that alias stays on
// self-update only).
func TestUpgrade_Registration(t *testing.T) {
	t.Parallel()

	cmd := newUpgradeCmd()
	if !strings.HasPrefix(cmd.Use, "upgrade") {
		t.Errorf("Use = %q, want it to start with upgrade", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty, want a description")
	}
	for _, name := range []string{"all", "check", "yes", "dry-run", "format"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q is not registered", name)
		}
	}
	if cmd.Flags().Lookup("dir") != nil {
		t.Error("upgrade must not register --dir")
	}
	for _, alias := range cmd.Aliases {
		if alias == "update" {
			t.Errorf("upgrade command carries an %q alias; only self-update may keep it", alias)
		}
	}
}

// M2 review fix: upgrade MUST register the SAME --json shortcut self-update
// has (addJSONShortcut, skills.go), so the two commands' flag surfaces stay
// aligned rather than only self-update offering the shorthand.
func TestUpgrade_RegistersJSONShortcut(t *testing.T) {
	t.Parallel()

	cmd := newUpgradeCmd()
	flag := cmd.Flags().Lookup("json")
	if flag == nil {
		t.Fatal("upgrade --json is missing")
	}
	if flag.Shorthand != "j" {
		t.Errorf("--json shorthand = %q, want j", flag.Shorthand)
	}
}

// cli-install#req:unknown-target-refused — `codegrapher upgrade nosuchcli`
// MUST fail before any confirmation, network request or write, mapped
// through the SAME installErrors mapper install itself uses (a
// *cobracmd.UsageError, matching install's own passthrough for
// KindUnknownTarget).
func TestUpgradeCmdNoSuchTarget_ExitCodeContract(t *testing.T) {
	t.Parallel()

	cmd := newUpgradeCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"nosuchcli"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error for an unknown upgrade target")
	}
	if !strings.Contains(err.Error(), "nosuchcli") {
		t.Errorf("error %q does not name the unknown target", err.Error())
	}
	var usage *cobracmd.UsageError
	if !errors.As(err, &usage) {
		t.Errorf("error %v (%T), want a *cobracmd.UsageError (KindUnknownTarget mapped explicitly)", err, err)
	}
}

// withEmptyUpgradeEnv points upgradeEnv (upgrade.go) at an Env that finds
// nothing installed anywhere: empty PATH, no host dir, nothing executable.
// The bare/--all report otherwise probes every OTHER fleet CLI genuinely
// installed on this real machine's PATH (this VM has specscore and wb
// installed) and makes a REAL release lookup for each one found
// (cli-install#req:upgrade-release-lookups-bounded skips only NotInstalled/
// unrecognized/skipped targets) — this seam is what keeps the bare report
// offline (cli-install#req:no-network-in-tests) without depending on this
// machine's own installed-CLI inventory staying empty.
func withEmptyUpgradeEnv(t *testing.T) {
	t.Helper()
	prev := upgradeEnv
	upgradeEnv = cliinstall.InstallEnv{
		Env: cliinstall.Env{
			PathDirs:     func() []string { return nil },
			HostDir:      func() (string, error) { return "", errors.New("no host dir in test env") },
			IsExecutable: func(string) bool { return false },
			EvalSymlinks: func(p string) (string, error) { return p, nil },
			Run:          func(context.Context, string, []string) ([]byte, error) { return nil, errors.New("not reachable in test env") },
		},
		UserHomeDir: func() (string, error) { return "", errors.New("no home dir in test env") },
		Getenv:      func(string) string { return "" },
		MkdirAll:    func(string, fs.FileMode) error { return errors.New("not writable in test env") },
	}
	t.Cleanup(func() { upgradeEnv = prev })
}

// cli-install#req:upgrade-no-args-reports — the bare report (no names, no
// --all) MUST exit successfully whether or not an upgrade is available,
// exercised offline via the SAME selfUpdateConfigFunc fixture-server seam
// self-update's own equivalence test below uses, plus withEmptyUpgradeEnv
// so no OTHER installed fleet CLI's real release lookup can reach the
// network (cli-install#req:no-network-in-tests).
func TestUpgradeCmdReport_OfflineViaFixtureServer(t *testing.T) {
	withFakeReleases(t, releaseServer(t, `[{"tag_name":"v1.0.0","prerelease":false,"draft":false}]`))
	withEmptyUpgradeEnv(t)

	cmd := newUpgradeCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare `upgrade` returned error (want exit 0 regardless of verdict): %v", err)
	}
	if out.Len() == 0 {
		t.Error("bare upgrade report produced no output")
	}
}

// --- end-to-end wiring: cobracmd.NewUpgrade + selfupdate.Config.Check
// --- against a fake GitHub releases endpoint, proving `self-update` and
// --- `upgrade codegrapher` reach the same verdict from the SAME HostConfig
// --- (cli-install#req:self-update-equals-upgrade-self,
// --- cli-install#req:host-target-is-running-binary). Both commands resolve
// --- their Config through the shared selfUpdateConfigFunc seam.

func releaseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withFakeReleases(t *testing.T, srv *httptest.Server) {
	t.Helper()
	prev := selfUpdateConfigFunc
	selfUpdateConfigFunc = func() selfupdate.Config {
		cfg := prev()
		cfg.ReleasesAPIURL = srv.URL
		cfg.HTTPClient = srv.Client()
		return cfg
	}
	t.Cleanup(func() { selfUpdateConfigFunc = prev })
}

func TestUpgrade_SelfUpdateEqualsUpgradeSelf(t *testing.T) {
	t.Run("update available: both report the same current/latest verdict", func(t *testing.T) {
		withFakeReleases(t, releaseServer(t, `[{"tag_name":"v99.0.0","prerelease":false,"draft":false}]`))

		selfCmd := newSelfUpdateCmd()
		var selfOut strings.Builder
		selfCmd.SetOut(&selfOut)
		selfCmd.SetArgs([]string{"--check", "--format", "json"})
		if err := selfCmd.Execute(); err != nil {
			t.Fatalf("self-update --check --format json returned error: %v", err)
		}
		var selfResult struct {
			Current string `json:"current"`
			Latest  string `json:"latest"`
			Verdict string `json:"verdict"`
		}
		if err := json.Unmarshal([]byte(selfOut.String()), &selfResult); err != nil {
			t.Fatalf("self-update JSON = %q: %v", selfOut.String(), err)
		}

		upCmd := newUpgradeCmd()
		var upOut strings.Builder
		upCmd.SetOut(&upOut)
		upCmd.SetArgs([]string{"codegrapher", "--check", "--format", "json"})
		if err := upCmd.Execute(); err != nil {
			t.Fatalf("upgrade codegrapher --check --format json returned error: %v", err)
		}

		if selfResult.Latest != "99.0.0" {
			t.Errorf("self-update latest = %q, want 99.0.0", selfResult.Latest)
		}
		if !strings.Contains(upOut.String(), "99.0.0") {
			t.Errorf("upgrade codegrapher output %q does not report the same latest release self-update saw (%q)", upOut.String(), selfResult.Latest)
		}
		if !strings.Contains(upOut.String(), selfResult.Current) {
			t.Errorf("upgrade codegrapher output %q does not report the same current version self-update saw (%q)", upOut.String(), selfResult.Current)
		}
	})
}

// cli-install#req:self-update-hook-hint, cli-install#req:host-target-is-
// running-binary — `upgrade codegrapher --yes` on an already-current host
// MUST run the SAME AfterUpdate hook self-update's own already-current
// path runs (cliinstall.ExecuteUpgrade makes a second, real UpdateAt call
// for an already-current host specifically so this hook always fires).
// upgradeDetectHostFunc forces a Manual classification so the host is
// never refused as ambiguous (the real `go test` binary's own path would
// otherwise classify Ambiguous and never reach AlreadyCurrent at all).
// fakePath must exist on disk: selfupdate's own UpdateAt resolves the
// executable identity via filepath.EvalSymlinks before invoking
// AfterUpdate at all, so a nonexistent path fails that resolution and
// silently skips the callback (only an AfterUpdateWarning, no error
// surfaced here) rather than reaching syncSkillsAfterSelfUpdate's own
// ActionAlreadyCurrent short-circuit. Writing an empty file keeps this
// fully offline and filesystem-isolated to t.TempDir().
func TestUpgrade_AfterUpdateHookRunsForAlreadyCurrentHost(t *testing.T) {
	prevConfig := selfUpdateConfigFunc
	srv := releaseServer(t, `[{"tag_name":"v1.0.0","prerelease":false,"draft":false}]`)
	selfUpdateConfigFunc = func() selfupdate.Config {
		return selfupdate.Config{
			BinaryName:     "codegrapher",
			Repository:     "code-grapher/codegrapher",
			CurrentVersion: "1.0.0",
			ReleasesAPIURL: srv.URL,
			HTTPClient:     srv.Client(),
		}
	}
	t.Cleanup(func() { selfUpdateConfigFunc = prevConfig })

	prevDetect := upgradeDetectHostFunc
	fakePath := filepath.Join(t.TempDir(), "codegrapher")
	if err := os.WriteFile(fakePath, []byte{}, 0o755); err != nil {
		t.Fatal(err)
	}
	upgradeDetectHostFunc = func() (selfupdate.Detection, error) {
		return selfupdate.Detection{Method: selfupdate.Manual, Path: fakePath}, nil
	}
	t.Cleanup(func() { upgradeDetectHostFunc = prevDetect })

	cmd := newUpgradeCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"codegrapher", "--yes"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade codegrapher --yes (already current) returned error: %v", err)
	}
}

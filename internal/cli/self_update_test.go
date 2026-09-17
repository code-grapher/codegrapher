package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/strongo/buildinfo"
	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/selfupdate"
)

// AC: cli-install#req:catalog-identity-single-source — review finding S2:
// newSelfUpdateConfig MUST resolve codegrapher's release identity from
// cliinstall.ByID("codegrapher").Config(...), the SAME compiled-in catalog
// entry install/upgrade resolve, not a hand-maintained duplicate that only
// happens to match today. TestNewSelfUpdateConfigMatchesPublishedReleaseAssets
// above already pins the individual field values; this proves they come
// from the catalog entry itself, so drift in catalog_codegrapher.go would
// be caught here too.
func TestNewSelfUpdateConfig_MatchesCatalogEntry(t *testing.T) {
	entry, ok := cliinstall.ByID(codegrapherCatalogID)
	if !ok {
		t.Fatalf("no catalog entry for %q", codegrapherCatalogID)
	}
	want := entry.Config(buildinfo.Get("codegrapher").Version)
	got := newSelfUpdateConfig()
	if got.Repository != want.Repository || got.BinaryName != want.BinaryName {
		t.Errorf("newSelfUpdateConfig() = %+v, want built from cliinstall.ByID(%q).Config(...): %+v", got, codegrapherCatalogID, want)
	}
	if len(got.Managers) != len(want.Managers) {
		t.Errorf("Managers = %d entries, want %d (the same catalog entry's managers)", len(got.Managers), len(want.Managers))
	}
	if len(got.SupportedPlatforms) != len(want.SupportedPlatforms) {
		t.Errorf("SupportedPlatforms = %d entries, want %d (the catalog entry's own platform matrix)", len(got.SupportedPlatforms), len(want.SupportedPlatforms))
	}
}

// cli-install#req:host-identity-from-catalog — a host id absent from the
// compiled catalog is a programming error caught by this package's own
// tests, never a runtime state a user can trigger. Before the S2 fix,
// newSelfUpdateConfig hand-built its Config and could never hit this path;
// now that it resolves through catalogEntryByID, a missing entry panics,
// matching specscore's and chatwright's own identical seam and panic.
func TestNewSelfUpdateConfig_PanicsWhenCatalogEntryMissing(t *testing.T) {
	prev := catalogEntryByID
	catalogEntryByID = func(string) (cliinstall.Entry, bool) { return cliinstall.Entry{}, false }
	t.Cleanup(func() { catalogEntryByID = prev })

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected newSelfUpdateConfig to panic when the catalog entry is missing")
		}
	}()
	newSelfUpdateConfig()
}

func TestNewSelfUpdateConfigMatchesPublishedReleaseAssets(t *testing.T) {
	cfg := newSelfUpdateConfig()
	if cfg.BinaryName != "codegrapher" || cfg.Repository != "code-grapher/codegrapher" {
		t.Errorf("identity = %q / %q", cfg.BinaryName, cfg.Repository)
	}
	if got := cfg.ChecksumsName("codegrapher", "0.1.2"); got != "checksums.txt" {
		t.Errorf("checksums name = %q, want checksums.txt", got)
	}
	if len(cfg.Managers) != 1 || cfg.Managers[0].UpgradeCommand != "brew update && brew upgrade --yes --cask -- codegrapher" {
		t.Errorf("managers = %+v", cfg.Managers)
	}
	manager := cfg.Managers[0]
	if !manager.CanExecuteUpgrade() {
		t.Fatal("Homebrew manager is redirect-only; self-update --yes must execute the managed upgrade")
	}
	wantSteps := []selfupdate.ManagedCommand{
		{Executable: "brew", Args: []string{"update"}},
		{Executable: "brew", Args: []string{"upgrade", "--yes", "--cask", "--", "codegrapher"}},
	}
	if !reflect.DeepEqual(manager.UpgradeSteps, wantSteps) {
		t.Errorf("Homebrew upgrade steps = %#v, want %#v", manager.UpgradeSteps, wantSteps)
	}
	if len(cfg.VersionProbeArgs) != 1 || cfg.VersionProbeArgs[0] != "--version" {
		t.Errorf("version probe = %v", cfg.VersionProbeArgs)
	}
}

func TestSelfUpdateRegistersCodeGrapherJSONShortcut(t *testing.T) {
	cmd := newSelfUpdateCmd()
	for _, name := range []string{"format", "json", "check", "yes", "version", "allow-downgrade", "dry-run"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("self-update flag --%s is missing", name)
		}
	}
	if flag := cmd.Flags().Lookup("json"); flag.Shorthand != "j" {
		t.Errorf("--json shorthand = %q, want j", flag.Shorthand)
	}
}

func TestSyncSkillsAfterSelfUpdateUsesVerifiedBinaryAndKeepsJSONClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX executable script")
	}
	binary := filepath.Join(t.TempDir(), "codegrapher")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'skills synced: %s %s\\n' \"$1\" \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := newSelfUpdateCmd()
	if err := cmd.Flags().Set("format", "json"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	update := selfupdate.AfterUpdate{
		Outcome:    selfupdate.Outcome{Action: selfupdate.ActionUpdated, Target: "0.1.0"},
		Executable: selfupdate.ExecutableIdentity{Path: binary},
	}
	if err := syncSkillsAfterSelfUpdate(cmd, context.Background(), update); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Errorf("JSON stdout = %q, want empty nested output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "skills synced: skills sync") {
		t.Errorf("stderr = %q, want nested skills output", stderr.String())
	}
}

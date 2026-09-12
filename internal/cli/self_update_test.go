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

	"github.com/strongo/cli-helpers/selfupdate"
)

func TestNewSelfUpdateConfigMatchesPublishedReleaseAssets(t *testing.T) {
	cfg := newSelfUpdateConfig()
	if cfg.BinaryName != "codegrapher" || cfg.Repository != "code-grapher/codegrapher" {
		t.Errorf("identity = %q / %q", cfg.BinaryName, cfg.Repository)
	}
	if got := cfg.ChecksumsName("codegrapher", "0.1.2"); got != "checksums.txt" {
		t.Errorf("checksums name = %q, want checksums.txt", got)
	}
	if len(cfg.Managers) != 1 || cfg.Managers[0].UpgradeCommand != codeGrapherHomebrewUpgradeCommand {
		t.Errorf("managers = %+v", cfg.Managers)
	}
	manager := cfg.Managers[0]
	if !manager.CanExecuteUpgrade() {
		t.Fatal("Homebrew manager is redirect-only; self-update --yes must execute the managed upgrade")
	}
	wantSteps := []selfupdate.ManagedCommand{
		{Executable: "brew", Args: []string{"update"}},
		{Executable: "brew", Args: []string{"upgrade", "--cask", "codegrapher"}},
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

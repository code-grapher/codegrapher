package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"github.com/strongo/buildinfo"
	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/selfupdate"
	selfupdatecmd "github.com/strongo/cli-helpers/selfupdate/cobracmd"
)

// codegrapherCatalogID is this binary's own id in
// github.com/strongo/cli-helpers/cliinstall -- the fleet-wide compiled-in
// registry of installable CLIs and their release identities
// (cli-install#req:host-identity-from-catalog).
const codegrapherCatalogID = "codegrapher"

// catalogEntryByID is a test seam over cliinstall.ByID so the defensive
// panic below (a host id absent from the compiled catalog, which never
// happens in production -- codegrapher's own catalog entry always exists)
// is exercisable, matching specscore's and chatwright's own identical seam.
var catalogEntryByID = cliinstall.ByID

// newSelfUpdateConfig resolves codegrapher's own selfupdate.Config from its
// compiled-in catalog entry (cliinstall/catalog_codegrapher.go in
// strongo/cli-helpers) rather than a hand-maintained duplicate
// (cli-install#req:catalog-identity-single-source: "A host's self-update
// SHOULD build its Config from its own entry so its self-update and every
// other host's install <that cli> resolve releases identically" --
// task-10's own "self-update Config from the catalog"). The catalog entry
// carries the SAME Managers (HomebrewCask), SupportedPlatforms,
// VersionProbeArgs and ChecksumsName this function used to hand-roll;
// UndeterminedVersions is left at the library's own {"dev"} default, which
// the catalog entry also leaves unset. newUpgradeCmd (upgrade.go) resolves
// its own HostConfig through the SAME selfUpdateConfigFunc seam below, so
// `codegrapher self-update` and `codegrapher upgrade codegrapher` always
// build from the identical Config.
func newSelfUpdateConfig() selfupdate.Config {
	entry, ok := catalogEntryByID(codegrapherCatalogID)
	if !ok {
		// A host id absent from the compiled catalog is a programming error
		// caught by this package's own tests, never a runtime state a user
		// can trigger (cli-install#req:host-identity-from-catalog).
		panic(fmt.Sprintf("cliinstall: no catalog entry for %q", codegrapherCatalogID))
	}
	return entry.Config(buildinfo.Get("codegrapher").Version)
}

// selfUpdateConfigFunc is a seam over newSelfUpdateConfig so tests can point
// a full command execution at an httptest.Server instead of the real GitHub
// API. newUpgradeCmd (upgrade.go) resolves its own HostConfig through this
// SAME seam, so `codegrapher self-update` and `codegrapher upgrade
// codegrapher` always build from the identical Config
// (cli-install#req:self-update-equals-upgrade-self,
// cli-install#req:host-target-is-running-binary), by construction rather
// than by two copies staying in sync.
var selfUpdateConfigFunc = newSelfUpdateConfig

func newSelfUpdateCmd() *cobra.Command {
	var command *cobra.Command
	command = selfupdatecmd.New(selfUpdateConfigFunc(), selfupdatecmd.CommandOptions{
		Short:      "Update the installed CodeGrapher binary to the latest release",
		Aliases:    []string{"update"},
		JSONFormat: true,
		AfterUpdate: func(ctx context.Context, update selfupdate.AfterUpdate) error {
			return syncSkillsAfterSelfUpdate(command, ctx, update)
		},
	})
	addJSONShortcut(command)
	return command
}

// syncSkillsAfterSelfUpdate re-execs the verified installed binary because an
// in-memory process continues to carry the pre-update embedded bundle.
func syncSkillsAfterSelfUpdate(cmd *cobra.Command, parent context.Context, update selfupdate.AfterUpdate) error {
	if update.Outcome.Action == selfupdate.ActionAlreadyCurrent {
		return nil
	}
	if update.Outcome.PostSwapWarning != nil {
		return fmt.Errorf("skip skills sync because the installed CodeGrapher binary was not verified: %w", update.Outcome.PostSwapWarning)
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, update.Executable.Path, "skills", "sync") //nolint:gosec // supplied by the verified self-update provider.
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		return fmt.Errorf("skills sync failed (%v); run `codegrapher skills sync` to install CodeGrapher Agent Skills manually: %s", err, stderr.String())
	}
	if format, _ := cmd.Flags().GetString("format"); format == "json" {
		_, _ = cmd.ErrOrStderr().Write(stdout.Bytes())
		return nil
	}
	_, _ = cmd.OutOrStdout().Write(stdout.Bytes())
	return nil
}

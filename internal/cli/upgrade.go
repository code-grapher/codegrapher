package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/strongo/cli-helpers/cliinstall"
	upgradecmd "github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
)

// newUpgradeCmd returns the "upgrade" command, built from
// github.com/strongo/cli-helpers/cliinstall/cobracmd against codegrapher's
// own catalog id (cli-install#req:host-identity-from-catalog). `codegrapher
// upgrade` (no arguments) reports every installed catalog CLI plus
// codegrapher itself -- current version, latest stable release, and verdict
// -- without changing anything; `codegrapher upgrade --all`/`codegrapher
// upgrade <name>...` upgrade what the report showed. codegrapher is always
// upgraded last, classified and versioned from its OWN self-update Config
// (never a PATH probe of its own binary): HostConfig resolves through the
// SAME selfUpdateConfigFunc seam newSelfUpdateCmd uses, so `codegrapher
// self-update` and `codegrapher upgrade codegrapher` reach the exact same
// library call (cli-install#req:self-update-equals-upgrade-self,
// cli-install#req:host-target-is-running-binary). HostAfterUpdate is the
// SAME syncSkillsAfterSelfUpdate function self-update calls, bound to
// upgrade's own command so it reads upgrade's own --format flag rather than
// self-update's (cli-install#req:self-update-hook-hint -- codegrapher is one
// of the two catalog entries with SelfUpdateHooks set).
func newUpgradeCmd() *cobra.Command {
	var command *cobra.Command
	command = upgradecmd.NewUpgrade(upgradecmd.UpgradeCommandOptions{
		Short:      "Upgrade installed fleet CLIs, including this one",
		HostID:     "codegrapher",
		Errors:     installErrors{},
		HostConfig: selfUpdateConfigFunc(),
		HostAfterUpdate: func(ctx context.Context, update selfupdate.AfterUpdate) error {
			return syncSkillsAfterSelfUpdate(command, ctx, update)
		},
		DetectHost: upgradeDetectHostFunc,
		Env:        upgradeEnv,
	})
	return command
}

// upgradeDetectHostFunc is a seam over cobracmd.UpgradeCommandOptions.
// DetectHost: nil in production, which the library documents as meaning
// "use opts.HostConfig.DetectSelf" -- exactly what self-update itself calls
// (cli-install#req:host-target-is-running-binary). Tests override this so
// they never depend on the real `go test` temp binary's own ambiguous
// classification, letting an already-current host exercise the SAME
// AfterUpdate hook path a real Manual or executable-managed upgrade would
// (cliinstall's ExecuteUpgrade always makes a second, real UpdateAt call
// for an already-current host so its hook still runs).
var upgradeDetectHostFunc func() (selfupdate.Detection, error)

// upgradeEnv is a seam over cobracmd.UpgradeCommandOptions.Env: its zero
// value (PathDirs == nil) is what cobracmd's own resolveEnv treats as "use
// the real cliinstall.DefaultInstallEnv()" in production. Tests override
// this to a fully offline Env so the bare report never probes -- and never
// makes a real release lookup for -- any OTHER fleet CLI genuinely
// installed on this machine's real PATH (cli-install#req:no-network-in-
// tests).
var upgradeEnv cliinstall.InstallEnv

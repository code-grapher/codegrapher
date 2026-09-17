package cli

import (
	"github.com/spf13/cobra"

	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
)

// newInstallCmd returns the "install" command, built from
// github.com/strongo/cli-helpers/cliinstall/cobracmd against codegrapher's
// own catalog id (cli-install#req:host-identity-from-catalog). `codegrapher
// install` lists the fleet CLIs relevant to codegrapher (specscore, wb,
// cover100) with their live status, and `codegrapher install <name>...`
// installs them the same way codegrapher itself was installed. cobracmd.New
// panics if "codegrapher" is absent from the compiled catalog — a
// programming error TestInstall_Registration below catches, never a
// runtime state a user sees.
func newInstallCmd() *cobra.Command {
	return cobracmd.New(cobracmd.CommandOptions{
		Short:  "List and install fleet CLIs relevant to codegrapher",
		Errors: installErrors{},
		HostID: codegrapherCatalogID,
	})
}

// installErrors implements cobracmd.ErrorMapper for codegrapher's own
// install command (cli-install#req:host-owned-exit-codes). codegrapher's
// main.go treats every non-nil root.Execute() error identically — print it
// to stderr and exit 1 — so, unlike a host with a dedicated usage or
// invalid-state exit code, nothing here changes the process exit code; the
// mapper exists so selfupdate.KindUnknownTarget is handled by an EXPLICIT
// branch, never falls through a self-update default branch, and is tagged
// as a usage mistake rather than left in self-update's own vocabulary,
// exactly as that REQ requires.
//
// selfupdate.KindOf resolves through cliinstall.Plan's own
// *selfupdate.Failure for an unknown name (Plan fails the whole batch
// before any other target is probed, so KindUnknownTarget never shares a
// *cliinstall.BatchFailure with an unrelated kind today) and through a
// *cliinstall.BatchFailure's first entry via its Unwrap() []error, so one
// check covers every shape opts.Errors.Failure can receive.
type installErrors struct{}

// Failure maps err into codegrapher's own error convention.
//
// cliinstall/cobracmd v0.21.0's mapFailure short-circuits a nil err before
// ever calling opts.Errors.Failure (fixed since v0.20.0, where this comment
// used to document the bug: runInstall called mapFailure(opts,
// plan.Failure()) and mapFailure(opts, result.Failure()) unconditionally,
// and both return nil for a fully successful batch, so
// installErrors.Failure(nil) ran on every successful `codegrapher install`
// and `codegrapher install <name> --dry-run`). The guard below stays only
// because it is trivially free and keeps this method nil-safe for any
// direct caller, including TestInstallErrorsFailure_NilReturnsNil.
func (installErrors) Failure(err error) error {
	if err == nil {
		return nil
	}
	switch selfupdate.KindOf(err) {
	case selfupdate.KindUnknownTarget:
		return &cobracmd.UsageError{Err: err}
	case selfupdate.KindNoInstallDir, selfupdate.KindDestinationExists:
		// Explicit, even though the outcome is identical to the default
		// branch below (codegrapher has only one non-success exit code):
		// cli-install#req:host-owned-exit-codes requires every host to map
		// the three new failure kinds through an EXPLICIT branch, never a
		// self-update default branch.
		return err
	default:
		// Every other kind — a *cobracmd.UsageError already produced for an
		// invalid --format or --all combined with names, and every kind
		// selfupdate itself carries — passes through unchanged, exactly
		// like self_update.go's own error passthrough (newSelfUpdateCmd's
		// Errors is nil, so selfupdate/cobracmd already returns those
		// errors unmodified).
		return err
	}
}

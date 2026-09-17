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
		HostID: "codegrapher",
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
// A nil err IS a real, reachable call on the ordinary success and dry-run
// path, not just a defensive guard: cliinstall/cobracmd v0.20.0's
// runInstall calls mapFailure(opts, plan.Failure()) and mapFailure(opts,
// result.Failure()) unconditionally, and both return nil for a fully
// successful batch, so installErrors.Failure(nil) runs on every successful
// `codegrapher install` and `codegrapher install <name> --dry-run`.
// Feedback for cli-helpers (known bug, not yet fixed at v0.20.0):
// mapFailure itself should short-circuit nil before calling
// opts.Errors.Failure, matching what ErrorMapper.Failure's own doc comment
// already promises ("maps a non-nil command error").
func (installErrors) Failure(err error) error {
	if err == nil {
		return nil
	}
	if selfupdate.KindOf(err) == selfupdate.KindUnknownTarget {
		return &cobracmd.UsageError{Err: err}
	}
	// Every other kind — a *cobracmd.UsageError already produced for an
	// invalid --format or --all combined with names, KindNoInstallDir,
	// KindDestinationExists, and every kind selfupdate itself carries —
	// passes through unchanged, exactly like self_update.go's own error
	// passthrough (newSelfUpdateCmd sets no Errors, so
	// selfupdate/cobracmd already returns those errors unmodified).
	return err
}

package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
)

func TestInstall_Registration(t *testing.T) {
	t.Parallel()

	cmd := newInstallCmd()
	if !strings.HasPrefix(cmd.Use, "install") {
		t.Errorf("Use = %q, want it to start with install", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty, want a description")
	}
	for _, name := range []string{"all", "yes", "dry-run", "dir", "format"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q is not registered", name)
		}
	}
	if f := cmd.Flags().Lookup("yes"); f.Shorthand != "y" {
		t.Errorf("--yes shorthand = %q, want y", f.Shorthand)
	}
	// cli-install#req:update-alias-policy — "upgrade MUST NOT get an update
	// alias", and by the same policy install never gains one either: only
	// self-update keeps the update alias it already ships.
	for _, alias := range cmd.Aliases {
		if alias == "update" {
			t.Errorf("install command carries an %q alias; only self-update may keep it", alias)
		}
	}
}

// TestInstallErrorsFailure_NilReturnsNil proves the defensive nil guard
// stays nil-safe: cliinstall/cobracmd v0.21.0's mapFailure now short-
// circuits nil before ever calling opts.Errors.Failure (fixed since the
// v0.20.0 bug install.go's doc comment used to document, where runInstall
// called mapFailure(opts, plan.Failure()) and mapFailure(opts,
// result.Failure()) unconditionally, and both return nil for a fully
// successful batch), so this guard is now purely defensive against a direct
// caller rather than a reachable production path.
func TestInstallErrorsFailure_NilReturnsNil(t *testing.T) {
	t.Parallel()

	if got := (installErrors{}).Failure(nil); got != nil {
		t.Errorf("Failure(nil) = %v, want nil", got)
	}
}

// TestInstallErrorsFailure_UnknownTargetBecomesUsageError proves
// selfupdate.KindUnknownTarget is mapped through an EXPLICIT branch to a
// *cobracmd.UsageError, never left to a self-update default branch
// (cli-install#req:host-owned-exit-codes), for both the shape
// cliinstall.Plan actually returns for an unknown name (a bare
// *selfupdate.Failure) and a *cliinstall.BatchFailure wrapping one.
func TestInstallErrorsFailure_UnknownTargetBecomesUsageError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{
			name: "bare failure (cliinstall.Plan's own shape for an unknown name)",
			err: &selfupdate.Failure{
				Kind: selfupdate.KindUnknownTarget,
				Err:  errors.New("nosuchcli: not a known install target; valid ids: cover100, specscore, wb"),
			},
		},
		{
			name: "batch failure wrapping one unknown-target failure",
			err: &cliinstall.BatchFailure{Failures: []*selfupdate.Failure{
				{Kind: selfupdate.KindUnknownTarget, Err: errors.New("nosuchcli: not a known install target")},
			}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := (installErrors{}).Failure(c.err)
			if got == nil {
				t.Fatal("Failure(...) = nil, want a non-nil error")
			}
			var usage *cobracmd.UsageError
			if !errors.As(got, &usage) {
				t.Errorf("Failure(%v) = %v (%T), want a *cobracmd.UsageError", c.err, got, got)
			}
			if !strings.Contains(got.Error(), "nosuchcli") {
				t.Errorf("Failure(...) = %q, want it to name the unknown target", got.Error())
			}
		})
	}
}

// TestInstallErrorsFailure_PassesThroughOtherKinds proves every failure
// that is NOT KindUnknownTarget — the two other cli-install-only kinds
// (mapped through their OWN explicit case, per cli-install#req:host-owned-
// exit-codes, even though the outcome is identical to the default branch),
// a self-update-shared kind, an already-usage error, and a plain error —
// passes through unchanged, matching self_update.go's own passthrough
// (newSelfUpdateCmd sets no Errors).
func TestInstallErrorsFailure_PassesThroughOtherKinds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"no install dir", &selfupdate.Failure{Kind: selfupdate.KindNoInstallDir, Err: errors.New("no per-user bin directory on PATH")}},
		{"destination exists", &selfupdate.Failure{Kind: selfupdate.KindDestinationExists, Err: errors.New("destination already exists")}},
		{"checksum (self-update-shared kind)", &selfupdate.Failure{Kind: selfupdate.KindChecksum, Err: errors.New("checksum mismatch")}},
		{"already a usage error", &cobracmd.UsageError{Err: errors.New(`invalid --format "yaml": expected text or json`)}},
		{"plain error", errors.New("network unavailable")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := (installErrors{}).Failure(c.err)
			if got != c.err {
				t.Errorf("Failure(%v) = %v, want the exact same error returned unchanged", c.err, got)
			}
		})
	}
}

// TestInstallCmdNoSuchTarget_ExitCodeContract runs the real command built
// exactly as root.go wires it (real, un-injected catalog and env) against an
// unknown target name. cliinstall.Plan validates every name against the
// compiled-in catalog BEFORE probing anything
// (cli-install#req:unknown-target-refused: "MUST fail before any
// confirmation, network request or write"), so this is inherently offline —
// no network/env seam is needed to keep it safe for CI.
func TestInstallCmdNoSuchTarget_ExitCodeContract(t *testing.T) {
	t.Parallel()

	cmd := newInstallCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"nosuchcli"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error for an unknown install target")
	}
	if !strings.Contains(err.Error(), "nosuchcli") {
		t.Errorf("error %q does not name the unknown target", err.Error())
	}
	var usage *cobracmd.UsageError
	if !errors.As(err, &usage) {
		t.Errorf("error %v (%T), want a *cobracmd.UsageError (KindUnknownTarget mapped explicitly)", err, err)
	}
}

func TestInstallCmdInvalidFormat_IsUsageError(t *testing.T) {
	t.Parallel()

	cmd := newInstallCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"--format", "yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error for --format yaml")
	}
	if !strings.Contains(err.Error(), "--format") {
		t.Errorf("error %q does not mention --format", err.Error())
	}
	var usage *cobracmd.UsageError
	if !errors.As(err, &usage) {
		t.Errorf("error %v (%T), want a *cobracmd.UsageError", err, err)
	}
}

// TestInstallCmdList_IsOfflineAndSucceeds proves the bare listing form
// (cli-install#req:list-offline-read-only) runs against the real catalog and
// environment without error — it only probes local PATH entries, never the
// network — so it exercises newInstallCmd's wiring end to end once more,
// beyond the unit-level ErrorMapper tests above.
func TestInstallCmdList_IsOfflineAndSucceeds(t *testing.T) {
	t.Parallel()

	cmd := newInstallCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("codegrapher install (bare listing) failed: %v", err)
	}
	if out.Len() == 0 {
		t.Error("bare listing produced no output")
	}
}

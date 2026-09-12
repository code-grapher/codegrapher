package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"github.com/strongo/buildinfo"
	"github.com/strongo/cli-helpers/selfupdate"
	selfupdatecmd "github.com/strongo/cli-helpers/selfupdate/cobracmd"
)

func newSelfUpdateConfig() selfupdate.Config {
	build := buildinfo.Get("codegrapher")
	return selfupdate.Config{
		BinaryName:           "codegrapher",
		Repository:           "code-grapher/codegrapher",
		CurrentVersion:       build.Version,
		UndeterminedVersions: []string{"dev"},
		Managers:             []selfupdate.Manager{selfupdate.HomebrewCask("codegrapher")},
		SupportedPlatforms:   []selfupdate.Platform{{GOOS: "darwin", GOARCH: "amd64"}, {GOOS: "darwin", GOARCH: "arm64"}, {GOOS: "linux", GOARCH: "amd64"}, {GOOS: "linux", GOARCH: "arm64"}, {GOOS: "windows", GOARCH: "amd64"}},
		VersionProbeArgs:     []string{"--version"},
		ChecksumsName:        func(string, string) string { return "checksums.txt" },
	}
}

func newSelfUpdateCmd() *cobra.Command {
	var command *cobra.Command
	command = selfupdatecmd.New(newSelfUpdateConfig(), selfupdatecmd.CommandOptions{
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

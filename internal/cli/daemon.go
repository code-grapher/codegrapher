package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/specscore/codegrapher/daemon"
	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "daemon",
		Short: "Keep one local repository index current in the background",
	}
	command.AddCommand(
		newDaemonStartCmd(),
		newDaemonStopCmd(),
		newDaemonRestartCmd(),
		newDaemonStatusCmd(),
		newDaemonRunCmd(),
	)
	return command
}

func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start [path]",
		Short: "Start the background index owner and wait until it is current",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args)
			if mismatch := indexer.DetectWorktreeIndexMismatch(watchStartPath(args), projectPath); mismatch != nil {
				return fmt.Errorf("cannot start a daemon for a different git worktree's index:\n%s", indexer.WorktreeMismatchWarning(*mismatch))
			}
			manager, err := daemon.NewManager()
			if err != nil {
				return err
			}
			status, err := manager.Start(cmd.Context(), projectPath)
			if err != nil {
				return err
			}
			writeDaemonStarted(cmd, status)
			return nil
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background index owner gracefully",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := daemon.NewManager()
			if err != nil {
				return err
			}
			status, err := manager.Stop(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "CodeGrapher daemon stopped%s\n", projectSuffix(status.ProjectPath))
			return err
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart [path]",
		Short: "Gracefully replace the background index owner",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := ""
			if len(args) > 0 {
				projectPath = resolveArg(args)
			}
			manager, err := daemon.NewManager()
			if err != nil {
				return err
			}
			status, err := manager.Restart(cmd.Context(), projectPath)
			if err != nil {
				return err
			}
			writeDaemonStarted(cmd, status)
			return nil
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	var format string
	var jsonOut bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Show background owner lifecycle and freshness health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := daemon.NewManager()
			if err != nil {
				return err
			}
			status, err := manager.Status(cmd.Context())
			if err != nil {
				return err
			}
			if wantsJSON(format, jsonOut) {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(status)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Lifecycle: %s\nPID: %d\nProject: %s\nLive: %t\nWatch ready: %t\nIndex current: %t\nPending paths: %d\nLast error: %s\nLog: %s\n",
				status.Lifecycle, status.PID, status.ProjectPath, status.Health.Live,
				status.Health.WatchReady, status.Health.IndexCurrent,
				status.Health.PendingDirtyPaths, status.Health.LastError, status.LogPath)
			return err
		},
	}
	addJSONOutputFlags(command, &format, &jsonOut)
	return command
}

func newDaemonRunCmd() *cobra.Command {
	command := &cobra.Command{
		Use:    "_run <path>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stopSignals()
			return daemon.RunFromEnvironment(ctx, args[0])
		},
	}
	return command
}

func writeDaemonStarted(cmd *cobra.Command, status daemon.Status) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CodeGrapher daemon ready\nPID: %d\nProject: %s\nLog: %s\nOwner: current user\nStop: codegrapher daemon stop\n",
		status.PID, status.ProjectPath, status.LogPath)
}

func projectSuffix(projectPath string) string {
	if projectPath == "" {
		return ""
	}
	return " for " + projectPath
}

package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestMachineOutputCommandsAcceptFormatJSON(t *testing.T) {
	for _, cmd := range []commandSpec{
		{name: "status", cmd: newStatusCmd()},
		{name: "query", cmd: newQueryCmd()},
		{name: "callers", cmd: newCallersCmd()},
		{name: "callees", cmd: newCalleesCmd()},
		{name: "impact", cmd: newImpactCmd()},
		{name: "affected", cmd: newAffectedCmd()},
		{name: "files", cmd: newFilesCmd()},
	} {
		t.Run(cmd.name, func(t *testing.T) {
			if flag := cmd.cmd.Flags().Lookup("format"); flag == nil {
				t.Fatal("--format flag is missing")
			}
			if flag := cmd.cmd.Flags().Lookup("json"); flag == nil || flag.Shorthand != "j" {
				t.Fatal("--json compatibility shorthand is missing")
			}
		})
	}
}

func TestWantsJSONAcceptsCanonicalFormatAndCompatibilityAlias(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
		json   bool
		want   bool
	}{
		{name: "canonical format", format: "json", want: true},
		{name: "compatibility alias", format: "text", json: true, want: true},
		{name: "human output", format: "text", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsJSON(tc.format, tc.json); got != tc.want {
				t.Fatalf("wantsJSON(%q, %t) = %t, want %t", tc.format, tc.json, got, tc.want)
			}
		})
	}
}

type commandSpec struct {
	name string
	cmd  *cobra.Command
}

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/skillsync"
)

func TestNewSkillsSyncConfigBindsEmbeddedCodeGrapherPlugin(t *testing.T) {
	cfg, err := newSkillsSyncConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLI != codeGrapherSkillsCLI {
		t.Errorf("CLI = %+v, want %+v", cfg.CLI, codeGrapherSkillsCLI)
	}
	if len(cfg.Bundles) != 1 {
		t.Fatalf("bundles = %d, want one", len(cfg.Bundles))
	}
	bundle := cfg.Bundles[0]
	if bundle.Plugin != codeGrapherSkillsPlugin {
		t.Errorf("plugin = %+v, want %+v", bundle.Plugin, codeGrapherSkillsPlugin)
	}
	if bundle.Source.Repository != "github.com/code-grapher/codegrapher" || bundle.Source.Path != "agentplugin/skills" {
		t.Errorf("source = %+v", bundle.Source)
	}
	if bundle.Source.Digest == "" {
		t.Error("embedded bundle digest is empty")
	}
}

func TestSkillsSyncUsesJSONShortcutAndIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	first := newSkillsCmd()
	first.SetArgs([]string{"sync", "--dir", dir, "--json"})
	var output bytes.Buffer
	first.SetOut(&output)
	if err := first.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Changes []skillsync.Change `json:"changes"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, output.String())
	}
	if len(payload.Changes) == 0 {
		t.Fatal("first sync reported no changes")
	}
	if _, err := os.Stat(filepath.Join(dir, "codegrapher", "SKILL.md")); err != nil {
		t.Fatalf("installed skill missing: %v", err)
	}

	second := newSkillsCmd()
	second.SetArgs([]string{"sync", "--dir", dir, "--format", "json"})
	output.Reset()
	second.SetOut(&output)
	if err := second.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("--format=json output is not valid JSON: %v\n%s", err, output.String())
	}
	for _, change := range payload.Changes {
		if change.Action != skillsync.Unchanged {
			t.Errorf("second sync action = %q, want unchanged", change.Action)
		}
	}
}

func TestSkillsSyncExposesExplicitNewerCompatibleMode(t *testing.T) {
	cmd := newSkillsCmd()
	sync, _, err := cmd.Find([]string{"sync"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"format", "json", "newer-compatible"} {
		if sync.Flags().Lookup(name) == nil {
			t.Errorf("sync flag --%s is missing", name)
		}
	}
}

// TestTestContextSkillIsSyncedWithValidFrontmatter covers the codegrapher
// context command's own Agent Skill (agentplugin/skills/codegrapher-test-
// context): it must sync into a target directory alongside the original
// codegrapher skill (proving skills.go's listing code does not assume a
// single skill), and its frontmatter must be a valid, trigger-shaped skill
// description.
func TestTestContextSkillIsSyncedWithValidFrontmatter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	cmd := newSkillsCmd()
	cmd.SetArgs([]string{"sync", "--dir", dir, "--json"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Changes []skillsync.Change `json:"changes"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, output.String())
	}
	synced := map[string]bool{}
	for _, c := range payload.Changes {
		synced[c.Name] = true
	}
	if !synced["codegrapher-test-context"] {
		t.Fatalf("codegrapher-test-context not reported as synced alongside codegrapher: %+v", payload.Changes)
	}

	installedPath := filepath.Join(dir, "codegrapher-test-context", "SKILL.md")
	data, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("installed skill missing: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("SKILL.md does not start with a frontmatter block:\n%s", content)
	}
	end := strings.Index(content[4:], "---")
	if end < 0 {
		t.Fatalf("SKILL.md frontmatter block is not closed:\n%s", content)
	}
	frontmatter := content[4 : 4+end]
	if !strings.Contains(frontmatter, "name: codegrapher-test-context") {
		t.Fatalf("frontmatter missing name: codegrapher-test-context:\n%s", frontmatter)
	}
	descIdx := strings.Index(frontmatter, "description:")
	if descIdx < 0 {
		t.Fatalf("frontmatter missing description:\n%s", frontmatter)
	}
	descLine := strings.TrimSpace(frontmatter[descIdx+len("description:"):])
	if before, _, ok := strings.Cut(descLine, "\n"); ok {
		descLine = strings.TrimSpace(before)
	}
	if !strings.HasPrefix(descLine, "Use when") {
		t.Fatalf("description is not trigger-shaped (must start with %q): %q", "Use when", descLine)
	}
	if strings.Count(descLine, ".") > 1 {
		t.Fatalf("description must be one sentence: %q", descLine)
	}
}

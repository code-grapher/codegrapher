package cli

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/selfupdate"
	"gopkg.in/yaml.v3"
)

// goreleaserConfig decodes only the .goreleaser.yaml fields this consumer
// test needs to compare against the compiled-in catalog entry
// (cli-install#req:catalog-identity-single-source): archive/checksums
// naming, the release repository, the Homebrew cask, and the supported
// platform matrix. It is deliberately not a full GoReleaser schema.
type goreleaserConfig struct {
	ProjectName string `yaml:"project_name"`
	Builds      []struct {
		GOOS   []string `yaml:"goos"`
		GOARCH []string `yaml:"goarch"`
		Ignore []struct {
			GOOS   string `yaml:"goos"`
			GOARCH string `yaml:"goarch"`
		} `yaml:"ignore"`
	} `yaml:"builds"`
	Archives []struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"checksum"`
	Release struct {
		GitHub struct {
			Owner string `yaml:"owner"`
			Name  string `yaml:"name"`
		} `yaml:"github"`
	} `yaml:"release"`
	HomebrewCasks []struct {
		Name       string `yaml:"name"`
		Repository struct {
			Owner string `yaml:"owner"`
			Name  string `yaml:"name"`
		} `yaml:"repository"`
	} `yaml:"homebrew_casks"`
}

// AC: cli-install#ac:catalog-matrix-is-valid — a consumer-side offline test
// asserting that codegrapher's own .goreleaser.yaml archive name template,
// checksum name template, platforms, release repository and Homebrew cask
// match its cli-helpers catalog entry, so drift fails codegrapher's own CI
// rather than surfacing as a 404 in someone else's `install codegrapher`
// (cli-install#req:catalog-identity-single-source).
func TestCodegrapherCatalogEntry_MatchesGoReleaserConfig(t *testing.T) {
	t.Parallel()

	entry, ok := cliinstall.ByID("codegrapher")
	if !ok {
		t.Fatal(`no catalog entry for "codegrapher"`)
	}

	raw, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var gr goreleaserConfig
	if err := yaml.Unmarshal(raw, &gr); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}

	if gr.ProjectName != entry.ID {
		t.Errorf("goreleaser project_name = %q, want catalog id %q", gr.ProjectName, entry.ID)
	}
	if got, want := gr.Release.GitHub.Owner+"/"+gr.Release.GitHub.Name, entry.Repository; got != want {
		t.Errorf("goreleaser release.github = %q, want catalog Repository %q", got, want)
	}
	if entry.TagPrefix != "" {
		t.Errorf("catalog TagPrefix = %q, want empty: codegrapher publishes one product per repository", entry.TagPrefix)
	}

	// checksum: the catalog entry MUST override the library's default
	// "<binary>_<version>_checksums.txt" naming with the flat "checksums.txt"
	// codegrapher's own release actually publishes.
	if gr.Checksum.NameTemplate != "checksums.txt" {
		t.Fatalf(".goreleaser.yaml checksum.name_template = %q, want %q", gr.Checksum.NameTemplate, "checksums.txt")
	}
	if entry.ChecksumsName == nil {
		t.Fatal("catalog entry.ChecksumsName is nil; want an override matching the flat checksums.txt goreleaser publishes")
	}
	if got := entry.ChecksumsName(entry.ID, "1.2.3"); got != gr.Checksum.NameTemplate {
		t.Errorf("entry.ChecksumsName(...) = %q, want goreleaser's checksum.name_template %q", got, gr.Checksum.NameTemplate)
	}

	// archive naming: the catalog entry leaves AssetName nil, which means
	// "use the library's own GoReleaser-shaped default"
	// (<binary>_<version>_<os>_<arch>.tar.gz/.zip) — verify .goreleaser.yaml's
	// own template is exactly that shape, so the two can never silently
	// diverge.
	if len(gr.Archives) == 0 {
		t.Fatal(".goreleaser.yaml declares no archives")
	}
	const wantArchiveTemplate = "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
	if gr.Archives[0].NameTemplate != wantArchiveTemplate {
		t.Errorf(".goreleaser.yaml archives[0].name_template = %q, want %q (the library's default asset-naming shape)",
			gr.Archives[0].NameTemplate, wantArchiveTemplate)
	}
	if entry.AssetName != nil {
		t.Error("catalog entry.AssetName is overridden; want nil (the shared GoReleaser default matches .goreleaser.yaml)")
	}

	// platform matrix: builds[].goos x goarch, minus builds[].ignore, must
	// equal entry.SupportedPlatforms exactly (order-independent).
	if len(gr.Builds) == 0 {
		t.Fatal(".goreleaser.yaml declares no builds")
	}
	build := gr.Builds[0]
	ignored := make(map[[2]string]bool, len(build.Ignore))
	for _, ig := range build.Ignore {
		ignored[[2]string{ig.GOOS, ig.GOARCH}] = true
	}
	var goreleaserPlatforms []selfupdate.Platform
	for _, goos := range build.GOOS {
		for _, goarch := range build.GOARCH {
			if ignored[[2]string{goos, goarch}] {
				continue
			}
			goreleaserPlatforms = append(goreleaserPlatforms, selfupdate.Platform{GOOS: goos, GOARCH: goarch})
		}
	}
	if got, want := sortedPlatforms(goreleaserPlatforms), sortedPlatforms(entry.SupportedPlatforms); !platformsEqual(got, want) {
		t.Errorf("goreleaser platform matrix = %v, want catalog entry.SupportedPlatforms %v", got, want)
	}

	// Homebrew cask: codegrapher publishes to its own org tap
	// (code-grapher/tap/codegrapher), matching entry.CaskToken exactly. The
	// `brew tap`/`brew install --cask` shorthand drops a tap repository's
	// "homebrew-" prefix, so homebrew-tap becomes "tap" in the token.
	if len(gr.HomebrewCasks) == 0 {
		t.Fatal(".goreleaser.yaml declares no homebrew_casks")
	}
	cask := gr.HomebrewCasks[0]
	tapName := strings.TrimPrefix(cask.Repository.Name, "homebrew-")
	wantCaskToken := cask.Repository.Owner + "/" + tapName + "/" + cask.Name
	if entry.CaskToken != wantCaskToken {
		t.Errorf("catalog CaskToken = %q, want %q derived from .goreleaser.yaml's homebrew_casks[0]", entry.CaskToken, wantCaskToken)
	}
}

func sortedPlatforms(in []selfupdate.Platform) []selfupdate.Platform {
	out := append([]selfupdate.Platform(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].GOOS != out[j].GOOS {
			return out[i].GOOS < out[j].GOOS
		}
		return out[i].GOARCH < out[j].GOARCH
	})
	return out
}

func platformsEqual(a, b []selfupdate.Platform) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Package specdoc is a thin codegrapher-owned adapter over the specscore-cli
// artifact parsers (github.com/specscore/specscore-cli). It turns a SpecScore
// artifact file (Feature, Idea, or Plan) into a single normalized Doc that
// codegrapher's extractor consumes.
//
// This package copies no parsing logic: the body of each artifact is parsed by
// specscore-cli's exported parsers. The only thing read here directly is the
// leading YAML frontmatter `format:` value, used solely to classify the
// artifact kind before dispatching — that is classification, not body parsing.
package specdoc

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/specscore/specscore-cli/pkg/feature"
	"github.com/specscore/specscore-cli/pkg/idea"
	"github.com/specscore/specscore-cli/pkg/plan"
)

// Kind is the SpecScore artifact kind.
type Kind string

const (
	KindFeature Kind = "feature"
	KindIdea    Kind = "idea"
	KindPlan    Kind = "plan"
)

// Frontmatter `format:` values that identify each artifact kind.
const (
	formatFeature = "https://specscore.md/feature-specification"
	formatIdea    = "https://specscore.md/idea-specification"
	formatPlan    = "https://specscore.md/plan-specification"
)

// Doc is the normalized, kind-agnostic view of a SpecScore artifact that
// codegrapher's extractor consumes. Fields not reachable through specscore-cli's
// exported API for a given kind are left zero.
type Doc struct {
	Kind   Kind
	Slug   string
	Title  string
	Status string
	Grade  string // only populated for ideas (header field); empty otherwise

	// Items are the artifact's child structural elements — plan tasks, or
	// section headings for features/ideas. ID is a stable within-doc handle
	// (e.g. task number); Title is the human label.
	Items []Item

	// Refs are raw cross-references to other artifacts, as (Kind, Target)
	// pairs. Resolution into graph edges happens in a later layer.
	Refs []Ref
}

// Item is a child structural element of a Doc.
type Item struct {
	ID    string
	Title string
}

// Ref is one raw cross-reference. Kind is the relationship (e.g.
// "promotes_to", "supersedes", "depends_on", "source", "verifies"); Target is
// the referenced slug or AC id as written.
type Ref struct {
	Kind   string
	Target string
}

// Parse reads a SpecScore artifact file, classifies it by its frontmatter
// `format:` value, dispatches to the matching specscore-cli parser, and maps
// the result into a normalized Doc.
func Parse(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(path, data)
}

// ParseContent is Parse for callers that already hold the artifact bytes (the
// indexer extract path). logicalPath is the artifact's project-relative path —
// used only to derive the artifact slug (idea/plan slug = filename stem; feature
// slug = README.md's parent directory) and for error messages, NOT to read the
// file. Because the underlying specscore-cli parsers read by path, the content
// is spilled to a temp file that mirrors the tail of logicalPath so slug
// derivation stays correct regardless of the indexer's working directory.
func ParseContent(logicalPath string, content []byte) (*Doc, error) {
	if frontmatterFormat(content) == "" {
		return nil, fmt.Errorf("specdoc: %s: unrecognized or missing format", logicalPath)
	}
	dir, err := os.MkdirTemp("", "codegrapher-specdoc-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	rel := filepath.FromSlash(logicalPath)
	tmp := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return nil, err
	}
	d, err := parse(tmp, content)
	if err != nil {
		return nil, err
	}
	// Re-derive path-based slugs (feature and plan) from the logical path. Both
	// use slugFromPath, which on the temp mirror already matches the logical
	// path's tail; re-deriving against logicalPath is belt-and-suspenders so the
	// slug never depends on the temp directory layout.
	if d.Kind == KindFeature || d.Kind == KindPlan {
		d.Slug = slugFromPath(logicalPath)
	}
	return d, nil
}

// parse dispatches an artifact (already-read bytes + a real readable path) to the
// matching specscore-cli parser.
func parse(path string, data []byte) (*Doc, error) {
	switch frontmatterFormat(data) {
	case formatFeature:
		return parseFeature(path)
	case formatIdea:
		return parseIdea(path)
	case formatPlan:
		return parsePlan(path)
	default:
		return nil, fmt.Errorf("specdoc: %s: unrecognized or missing format", path)
	}
}

func parseIdea(path string) (*Doc, error) {
	i, err := idea.Parse(path)
	if err != nil {
		return nil, err
	}
	d := &Doc{
		Kind:   KindIdea,
		Slug:   i.Slug,
		Title:  i.TitleName,
		Status: i.Status(),
		Grade:  strings.TrimSpace(i.FieldByName["Grade"].Value),
	}
	if d.Title == "" {
		d.Title = i.Title
	}
	for _, t := range i.PromotesTo() {
		d.Refs = append(d.Refs, Ref{Kind: "promotes_to", Target: t})
	}
	for _, t := range i.Supersedes() {
		d.Refs = append(d.Refs, Ref{Kind: "supersedes", Target: t})
	}
	for _, t := range i.RelatedIdeas() {
		d.Refs = append(d.Refs, Ref{Kind: "related", Target: t})
	}
	for _, s := range i.Sections {
		d.Items = append(d.Items, Item{ID: s.Title, Title: s.Title})
	}
	return d, nil
}

func parsePlan(path string) (*Doc, error) {
	p, err := plan.Parse(path)
	if err != nil {
		return nil, err
	}
	d := &Doc{
		Kind: KindPlan,
		// pkg/plan.Slug is the file stem, which is "README" for directory-style
		// plans (spec/plans/<slug>/README.md). Derive from the path instead so
		// they get their directory slug; flat plans are unaffected
		// (slugFromPath returns the file stem there).
		Slug:   slugFromPath(path),
		Title:  p.Title,
		Status: p.Status,
	}
	if p.SourceIdea != "" {
		d.Refs = append(d.Refs, Ref{Kind: "source", Target: p.SourceIdea})
	}
	if p.SourceFeature != "" {
		d.Refs = append(d.Refs, Ref{Kind: "source_feature", Target: p.SourceFeature})
	}
	for _, t := range p.Tasks {
		d.Items = append(d.Items, Item{ID: strconv.Itoa(t.Number), Title: t.Name})
		for _, dep := range t.DependsOn {
			d.Refs = append(d.Refs, Ref{Kind: "depends_on", Target: strconv.Itoa(dep)})
		}
		for _, ac := range t.Verifies {
			d.Refs = append(d.Refs, Ref{Kind: "verifies", Target: ac})
		}
	}
	return d, nil
}

func parseFeature(path string) (*Doc, error) {
	status, err := feature.ParseFeatureStatus(path)
	if err != nil {
		return nil, err
	}
	title, err := feature.ParseFeatureTitle(path)
	if err != nil {
		return nil, err
	}
	deps, err := feature.ParseDependencies(path)
	if err != nil {
		return nil, err
	}
	sections, err := feature.ParseSections(path)
	if err != nil {
		return nil, err
	}
	d := &Doc{
		Kind:   KindFeature,
		Slug:   slugFromPath(path),
		Title:  title,
		Status: status,
	}
	for _, dep := range deps {
		d.Refs = append(d.Refs, Ref{Kind: "depends_on", Target: dep})
	}
	for _, s := range sections {
		d.Items = append(d.Items, Item{ID: s.Title, Title: s.Title})
	}
	return d, nil
}

// slugFromPath derives a feature slug from its README path: the name of the
// directory containing README.md (spec/features/<slug>/README.md), or the file
// stem otherwise.
func slugFromPath(path string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(p, "/")
	if n := len(parts); n >= 2 && strings.EqualFold(parts[n-1], "README.md") {
		return parts[n-2]
	}
	base := parts[len(parts)-1]
	return strings.TrimSuffix(base, ".md")
}

// frontmatterFormat extracts the leading YAML frontmatter `format:` scalar.
// Returns "" when there is no complete leading `---`-fenced block or no
// `format:` key. This reads only the classification key, not the artifact body.
func frontmatterFormat(data []byte) string {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if line == "---" {
			return ""
		}
		if after, ok := strings.CutPrefix(line, "format:"); ok {
			v := strings.TrimSpace(after)
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

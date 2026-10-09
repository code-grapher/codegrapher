// Package semantic builds a deterministic, derived graph of MeaningGraph and
// ModelSpec declarations from the released parsers. Files remain authoritative.
package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	meaning "github.com/meaninggraph/cli/pkg/meaning"
	modelspec "github.com/modelspec-org/cli/pkg/modelspec"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

type Graph struct {
	Nodes        []model.Node
	Edges        []model.Edge
	SourceHashes map[string]string
}

type builder struct {
	root        string
	address     string
	nodes       map[string]model.Node
	edges       []model.Edge
	models      map[string][]string
	modelPaths  map[string]string
	moduleNames map[string]string
	concepts    map[string][]string
	snapshot    *sourceSnapshot
}

// writtenModelKind is the one place that maps a concept kind read by the
// ModelSpec library to the node kind CodeGrapher writes. The library names a
// record type "record" whichever spelling the file uses; CodeGrapher still
// writes it as model_entity, so the index does not change when a model file
// moves to the current spelling. Writing model_record later is a one-line edit
// here together with an extraction-version bump.
var writtenModelKind = map[modelspec.Kind]model.NodeKind{
	modelspec.KindRecord:    model.KindModelEntity,
	modelspec.KindComponent: model.KindModelComponent,
	modelspec.KindEnum:      model.KindModelEnum,
}

// writtenMemberKind is the memberKind metadata written for the members of each
// concept kind (a record's members were properties, a component's are fields).
var writtenMemberKind = map[modelspec.Kind]string{
	modelspec.KindRecord:    "property",
	modelspec.KindComponent: "field",
	modelspec.KindEnum:      "field",
}

// The library reads a member's reference to a record as the attribute "record"
// in either spelling. CodeGrapher keeps writing it under the name it has always
// carried, as member metadata and as the attribute of the references edge.
const (
	readRecordRefAttr    = "record"
	writtenRecordRefAttr = "entity"
)

func stableID(kind model.NodeKind, scope, name string) string {
	sum := sha256.Sum256([]byte(string(kind) + "\x00" + scope + "\x00" + name))
	return string(kind) + ":" + hex.EncodeToString(sum[:16])
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(path)
}

func repoAddress(root string) string {
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return parseRepoAddress(strings.TrimSpace(string(out)))
}

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var hostPart = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

func parseRepoAddress(raw string) string {
	var host, path string
	if strings.HasPrefix(raw, "git@") {
		value := strings.TrimPrefix(raw, "git@")
		h, p, ok := strings.Cut(value, ":")
		if !ok {
			return ""
		}
		host, path = h, p
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		if u.Scheme != "https" && u.Scheme != "ssh" {
			return ""
		}
		if u.Scheme == "ssh" && (u.User == nil || u.User.Username() != "git") {
			return ""
		}
		if u.Scheme == "https" && u.User != nil {
			return ""
		}
		if u.Host == "" || u.Host != u.Hostname() || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		host, path = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	}
	if !hostPart.MatchString(host) || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return ""
	}
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return ""
	}
	for _, p := range parts {
		if !repoPart.MatchString(p) || p == "." || p == ".." {
			return ""
		}
	}
	return host + "/" + parts[0] + "/" + parts[1]
}

func Build(root string, files []string, sources []*store.Store) (Graph, error) {
	b := &builder{root: root, address: repoAddress(root), nodes: map[string]model.Node{}, models: map[string][]string{}, modelPaths: map[string]string{}, moduleNames: map[string]string{}, concepts: map[string][]string{}, snapshot: newSourceSnapshot(root)}
	if err := b.modelSpec(files); err != nil {
		return Graph{}, err
	}
	if err := b.meaning(files); err != nil {
		return Graph{}, err
	}
	if err := b.annotations(files, sources); err != nil {
		return Graph{}, err
	}
	if b.snapshot.err != nil {
		return Graph{}, b.snapshot.err
	}
	out := Graph{Edges: b.edges, SourceHashes: b.snapshot.hashes()}
	for _, n := range b.nodes {
		out.Nodes = append(out.Nodes, n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })
	sort.Slice(out.Edges, func(i, j int) bool {
		a, c := out.Edges[i], out.Edges[j]
		if a.Source != c.Source {
			return a.Source < c.Source
		}
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		if a.Target != c.Target {
			return a.Target < c.Target
		}
		return a.Line < c.Line
	})
	return out, nil
}

func (b *builder) put(n model.Node) { b.nodes[n.ID] = n }
func (b *builder) edge(src, dst string, kind model.EdgeKind, line int, meta map[string]any) {
	if src == "" || dst == "" {
		return
	}
	b.edges = append(b.edges, model.Edge{Source: src, Target: dst, Kind: kind, Line: line, Metadata: meta, Provenance: "semantic"})
}
func (b *builder) diagnostic(id, code, msg string, line int) {
	n := b.nodes[id]
	if n.ID == "" {
		return
	}
	if n.Metadata == nil {
		n.Metadata = map[string]any{}
	}
	ds, _ := n.Metadata["diagnostics"].([]map[string]any)
	ds = append(ds, map[string]any{"code": code, "message": msg, "line": line})
	n.Metadata["diagnostics"] = ds
	b.nodes[id] = n
}
func (b *builder) modelCandidates(name, scope string) []string {
	var out []string
	for _, id := range b.models[name] {
		n := b.nodes[id]
		if n.Metadata["semanticScope"] == scope {
			out = append(out, id)
		}
	}
	return out
}
func repr(path string, form string, line, end int) map[string]any {
	return map[string]any{"filePath": path, "form": form, "startLine": line, "endLine": end}
}
func nodeValue(n *modelspec.Node) any {
	if n == nil {
		return nil
	}
	switch n.Type {
	case modelspec.NodeString, modelspec.NodeNumber:
		return n.Str
	case modelspec.NodeBool:
		return n.Bool
	case modelspec.NodeArray:
		v := make([]any, 0, len(n.Items))
		for _, x := range n.Items {
			v = append(v, nodeValue(x))
		}
		return v
	case modelspec.NodeObject:
		v := map[string]any{}
		for _, x := range n.Fields {
			v[x.Key] = nodeValue(x.Value)
		}
		return v
	default:
		return nil
	}
}
func attrs(a []modelspec.Attr) map[string]any {
	out := map[string]any{}
	for _, x := range a {
		out[x.Name] = nodeValue(x.Value)
	}
	return out
}

// memberAttrs is attrs for a member: the record reference is written under its
// established name (writtenRecordRefAttr), whichever spelling the file uses.
func memberAttrs(a []modelspec.Attr) map[string]any {
	out := attrs(a)
	if v, ok := out[readRecordRefAttr]; ok {
		delete(out, readRecordRefAttr)
		out[writtenRecordRefAttr] = v
	}
	return out
}

// deprecationNotice returns the library's message with the file named the way
// the index names it: relative to the repository root, not the checkout path.
func deprecationNotice(f modelspec.Finding, path string) string {
	return strings.Replace(f.Message, fmt.Sprintf("%q", f.File), fmt.Sprintf("%q", path), 1)
}
func (b *builder) lineCount(path string) int {
	data := b.snapshot.readRange(path)
	if data == nil {
		return 1
	}
	return strings.Count(string(data), "\n") + 1
}

// blockEnd returns the actual closing line of a ModelSpec HCL/JSON object.
// It scans from the parser's declaration line, ignoring braces in quoted
// strings, comments and HCL heredocs. A missing close stays at the declaration line;
// the parser diagnostic remains authoritative for malformed input.
func (b *builder) blockEnd(path string, start int) int {
	data := b.snapshot.readRange(path)
	if data == nil {
		return start
	}
	lines := strings.Split(string(data), "\n")
	depth := 0
	seen := false
	quoted := false
	escaped := false
	blockComment := false
	heredoc := ""
	for i := max(0, start-1); i < len(lines); i++ {
		line := lines[i]
		if heredoc != "" {
			if strings.TrimSpace(line) == heredoc {
				heredoc = ""
			}
			continue
		}
		for j := 0; j < len(line); j++ {
			ch := line[j]
			if blockComment {
				if ch == '*' && j+1 < len(line) && line[j+1] == '/' {
					blockComment = false
					j++
				}
				continue
			}
			if escaped {
				escaped = false
				continue
			}
			if quoted {
				switch ch {
				case '\\':
					escaped = true
				case '"':
					quoted = false
				}
				continue
			}
			if ch == '"' {
				quoted = true
				continue
			}
			if ch == '#' || (ch == '/' && j+1 < len(line) && line[j+1] == '/') {
				break
			}
			if ch == '/' && j+1 < len(line) && line[j+1] == '*' {
				blockComment = true
				j++
				continue
			}
			if ch == '<' && j+1 < len(line) && line[j+1] == '<' {
				k := j + 2
				if k < len(line) && line[k] == '-' {
					k++
				}
				startToken := k
				for k < len(line) && ((line[k] >= 'A' && line[k] <= 'Z') || (line[k] >= 'a' && line[k] <= 'z') || (line[k] >= '0' && line[k] <= '9') || line[k] == '_') {
					k++
				}
				if k > startToken {
					heredoc = line[startToken:k]
					j = k - 1
					continue
				}
			}
			if ch == '{' {
				depth++
				seen = true
			}
			if ch == '}' {
				depth--
				if seen && depth == 0 {
					return i + 1
				}
			}
		}
	}
	return start
}
func (b *builder) yamlBlockEnd(path string, start int) int {
	data := b.snapshot.readRange(path)
	if data == nil {
		return start
	}
	lines := strings.Split(string(data), "\n")
	if start < 1 || start > len(lines) {
		return start
	}
	indent := len(lines[start-1]) - len(strings.TrimLeft(lines[start-1], " "))
	end := start
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " "))
		if n <= indent {
			return end
		}
		end = i + 1
	}
	return end
}

func (b *builder) modelSpec(files []string) error {
	var paths []modelspec.Source
	for _, p := range files {
		if !modelspec.IsModelFile(p) {
			continue
		}
		abs := filepath.Join(b.root, filepath.FromSlash(p))
		paths = append(paths, modelspec.Source{Path: abs, Abs: abs})
	}
	if len(paths) == 0 {
		return nil
	}
	parsed, findings, err := modelspec.Load(snapshotModelFS{snapshot: b.snapshot}, paths, nil)
	if err != nil {
		return err
	}
	findings = append(findings, modelspec.Check(parsed, modelspec.Options{})...)
	groupNames := map[string]string{}
	for _, m := range parsed {
		if !m.Twin {
			groupNames[m.Group] = m.Name
		}
	}
	nameCounts := map[string]int{}
	for _, n := range groupNames {
		nameCounts[n]++
	}
	groupScope := map[string]string{}
	for group, name := range groupNames {
		scope := "model:" + b.address + "/" + name
		if b.address == "" {
			scope = "model:" + name
		}
		if nameCounts[name] > 1 {
			scope += "/" + rel(b.root, group)
		}
		groupScope[group] = scope
	}
	moduleID := map[string]string{}
	for _, m := range parsed {
		group := m.Group
		if m.Twin && m.TwinOf != nil {
			group = m.TwinOf.Group
		}
		if m.Twin && m.TwinOf == nil {
			// A layout twin belongs to the HCL group in its own models
			// directory, even when another module elsewhere has the same name.
			if _, ok := groupScope[filepath.Dir(m.File)]; ok {
				group = filepath.Dir(m.File)
			}
		}
		scope := groupScope[group]
		if scope == "" {
			scope = "model:" + b.address + "/" + m.Name
		}
		id := moduleID[scope]
		path := rel(b.root, m.File)
		b.modelPaths[path] = scope
		b.moduleNames[scope] = m.Name
		if id == "" {
			id = stableID(model.KindModelModule, scope, m.Name)
			moduleID[scope] = id
			meta := map[string]any{"semanticScope": scope, "semanticId": m.Name, "representations": []map[string]any{}}
			if m.Module != nil {
				meta["moduleId"] = m.Module.ID
				meta["moduleVersion"] = m.Module.Version
			}
			b.put(model.Node{ID: id, Kind: model.KindModelModule, Name: m.Name, QualifiedName: scope, FilePath: path, Language: model.LangModelSpec, StartLine: max(1, m.ModuleLine), EndLine: b.lineCount(m.File), Metadata: meta})
		}
		mn := b.nodes[id]
		reps := mn.Metadata["representations"].([]map[string]any)
		reps = append(reps, repr(path, string(m.Form), 1, b.lineCount(m.File)))
		mn.Metadata["representations"] = reps
		if !m.Twin || len(reps) == 1 {
			mn.FilePath = path
		}
		b.put(mn)
		if m.Twin {
			for _, c := range m.Concepts {
				kind := writtenModelKind[c.Kind]
				cid := stableID(kind, scope, c.Name)
				if n, ok := b.nodes[cid]; ok {
					reps := n.Metadata["representations"].([]map[string]any)
					reps = append(reps, repr(path, string(m.Form), c.Line, b.blockEnd(m.File, c.Line)))
					n.Metadata["representations"] = reps
					b.put(n)
				}
				for _, mem := range c.Members {
					mid := stableID(model.KindModelMember, scope, c.Name+"."+mem.Name)
					if n, ok := b.nodes[mid]; ok {
						reps := n.Metadata["representations"].([]map[string]any)
						reps = append(reps, repr(path, string(m.Form), mem.Line, b.blockEnd(m.File, mem.Line)))
						n.Metadata["representations"] = reps
						b.put(n)
					}
				}
			}
			continue
		}
		if m.Broken {
			continue
		}
		for _, c := range m.Concepts {
			kind := writtenModelKind[c.Kind]
			cid := stableID(kind, scope, c.Name)
			end := b.blockEnd(m.File, c.Line)
			meta := attrs(c.Attrs)
			meta["semanticScope"] = scope
			meta["semanticId"] = c.Name
			meta["module"] = m.Name
			meta["representations"] = []map[string]any{repr(path, string(m.Form), c.Line, end)}
			n := model.Node{ID: cid, Kind: kind, Name: c.Name, QualifiedName: m.Name + "." + c.Name, FilePath: path, Language: model.LangModelSpec, StartLine: c.Line, EndLine: max(c.Line, end), Metadata: meta}
			if prev, ok := b.nodes[cid]; ok {
				b.diagnostic(prev.ID, "duplicate-name", "duplicate ModelSpec declaration "+c.Name, c.Line)
				continue
			}
			b.put(n)
			b.edge(id, cid, model.EdgeContains, c.Line, nil)
			b.models[m.Name+"."+c.Name] = append(b.models[m.Name+"."+c.Name], cid)
			for _, mem := range c.Members {
				mid := stableID(model.KindModelMember, scope, c.Name+"."+mem.Name)
				mend := b.blockEnd(m.File, mem.Line)
				mmeta := memberAttrs(mem.Attrs)
				mmeta["semanticScope"] = scope
				mmeta["semanticId"] = c.Name + "." + mem.Name
				mmeta["module"] = m.Name
				mmeta["memberKind"] = writtenMemberKind[c.Kind]
				mmeta["representations"] = []map[string]any{repr(path, string(m.Form), mem.Line, mend)}
				if _, ok := b.nodes[mid]; ok {
					b.diagnostic(cid, "duplicate-member", "duplicate member "+mem.Name, mem.Line)
					continue
				}
				b.put(model.Node{ID: mid, Kind: model.KindModelMember, Name: mem.Name, QualifiedName: m.Name + "." + c.Name + "." + mem.Name, FilePath: path, Language: model.LangModelSpec, StartLine: mem.Line, EndLine: max(mem.Line, mend), Metadata: mmeta})
				b.edge(cid, mid, model.EdgeContains, mem.Line, nil)
				b.models[m.Name+"."+c.Name+"."+mem.Name] = append(b.models[m.Name+"."+c.Name+"."+mem.Name], mid)
			}
		}
	}
	for _, f := range findings {
		path := rel(b.root, f.File)
		if f.Rule == modelspec.RuleDeprecated {
			// One notice per file, kept on the module the file belongs to. A
			// warning is retained like every other finding and never fails a run.
			b.diagnostic(moduleID[b.modelPaths[path]], f.Rule, deprecationNotice(f, path), f.Line)
			continue
		}
		for _, n := range b.nodes {
			if n.FilePath == path && n.Kind == model.KindModelModule {
				b.diagnostic(n.ID, f.Rule, f.Message, f.Line)
				break
			}
		}
	}
	b.modelRelations()
	return nil
}
func (b *builder) modelRelations() {
	for _, n := range b.nodes {
		module, _ := n.Metadata["module"].(string)
		scope, _ := n.Metadata["semanticScope"].(string)
		if n.Kind == model.KindModelEntity {
			if uses, ok := n.Metadata["use"].([]any); ok {
				for _, x := range uses {
					if ref, ok := x.(string); ok {
						b.linkModelRef(n, module, scope, "use", ref, model.EdgeReferences)
					}
				}
			}
		}
		if n.Kind != model.KindModelMember {
			continue
		}
		for _, key := range []string{"entity", "component", "enum"} {
			ref, _ := n.Metadata[key].(string)
			if ref != "" {
				b.linkModelRef(n, module, scope, key, ref, model.EdgeReferences)
			}
		}
		if bind, ok := n.Metadata["bind"].(string); ok {
			parts := strings.Split(bind, ".")
			ref := bind
			if len(parts) == 2 {
				ref = module + "." + bind
			}
			candidates := b.modelCandidates(ref, scope)
			if len(candidates) == 1 {
				b.edge(n.ID, candidates[0], model.EdgeReferences, n.StartLine, map[string]any{"attribute": "bind"})
			} else {
				b.diagnostic(n.ID, "unresolved-reference", "unresolved or ambiguous bind "+bind, n.StartLine)
			}
		}
	}
}
func (b *builder) linkModelRef(n model.Node, module, scope, key, ref string, kind model.EdgeKind) {
	name := ref
	if !strings.Contains(ref, ".") {
		name = module + "." + ref
	}
	candidates := b.modelCandidates(name, scope)
	if len(candidates) == 0 && strings.Contains(ref, ".") && !strings.HasPrefix(ref, module+".") {
		candidates = b.models[name]
	}
	if len(candidates) == 1 {
		b.edge(n.ID, candidates[0], kind, n.StartLine, map[string]any{"attribute": key})
	} else {
		b.diagnostic(n.ID, "unresolved-reference", "unresolved or ambiguous "+key+" "+ref, n.StartLine)
	}
}

func (b *builder) meaning(files []string) error {
	groups := map[string][]string{}
	for _, p := range files {
		if strings.HasSuffix(p, meaning.FileSuffix) {
			dir := filepath.Dir(p)
			groups[dir] = append(groups[dir], filepath.Join(b.root, filepath.FromSlash(p)))
		}
	}
	dirs := make([]string, 0, len(groups))
	for d := range groups {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		paths := groups[dir]
		slices.Sort(paths)
		g, err := meaning.LoadFiles(snapshotMeaningFS{snapshot: b.snapshot}, paths)
		if err != nil {
			return err
		}
		scope := "meaning:" + b.address
		if dir != "." {
			scope += "/" + filepath.ToSlash(dir)
		}
		if b.address == "" {
			scope = "meaning:local/" + filepath.ToSlash(dir)
		}
		g.Address = b.address
		first := rel(b.root, paths[0])
		gid := stableID(model.KindMeaningGraph, scope, "graph")
		b.put(model.Node{ID: gid, Kind: model.KindMeaningGraph, Name: filepath.Base(dir), QualifiedName: scope, FilePath: first, Language: model.LangMeaningGraph, StartLine: 1, EndLine: b.lineCount(paths[0]), Metadata: map[string]any{"semanticScope": scope, "semanticId": "graph", "address": b.address, "representations": []map[string]any{repr(first, "yaml", 1, b.lineCount(paths[0]))}}})
		for _, f := range g.Files {
			path := rel(b.root, f.Path)
			if f.ParseErr != nil {
				b.diagnostic(gid, "parse-error", f.ParseErr.Message, 0)
				continue
			}
			for _, c := range f.Concepts {
				if c.ID == "" {
					b.diagnostic(gid, "missing-id", "meaning concept has no id", c.Line)
					continue
				}
				id := stableID(model.KindMeaningConcept, scope, c.ID)
				end := b.yamlBlockEnd(f.Path, c.Line)
				meta := map[string]any{"semanticScope": scope, "semanticId": c.ID, "conceptKind": c.Kind, "labels": c.Labels, "synonyms": c.Synonyms, "values": c.Values, "unit": c.Unit, "source": c.Source, "representations": []map[string]any{repr(path, "yaml", c.Line, end)}}
				if c.Measure != nil {
					meta["formula"] = c.Measure.Formula
					meta["aggregation"] = c.Measure.Aggregation
					meta["inputs"] = c.Measure.Inputs
					meta["dimensions"] = c.Measure.Dimensions
				}
				if _, ok := b.nodes[id]; ok {
					b.diagnostic(gid, "duplicate-concept", "duplicate concept "+c.ID, c.Line)
					continue
				}
				var words []string
				for _, v := range c.Labels {
					words = append(words, v)
				}
				for _, xs := range c.Synonyms {
					words = append(words, xs...)
				}
				slices.Sort(words)
				b.put(model.Node{ID: id, Kind: model.KindMeaningConcept, Name: c.ID, QualifiedName: scope + "/" + c.ID, FilePath: path, Language: model.LangMeaningGraph, StartLine: c.Line, EndLine: max(c.Line, end), Docstring: strings.Join(words, " "), Metadata: meta})
				b.edge(gid, id, model.EdgeContains, c.Line, nil)
				b.concepts[c.ID] = append(b.concepts[c.ID], id)
			}
		}
		// The released checker validates schemas, duplicates, graph boundaries,
		// binding roles and references without making network requests.
		blocked := map[string]map[int]bool{}
		invalidModels := map[string]bool{}
		for _, finding := range (meaning.Checker{}).Check(g) {
			b.diagnostic(gid, finding.Rule, finding.Message, finding.Line)
			if finding.Severity == meaning.Error {
				if finding.Rule == meaning.RuleModels || finding.Rule == meaning.RuleSchema {
					invalidModels[finding.File] = true
				}
				if finding.Rule == meaning.RuleBindingModel || finding.Rule == meaning.RuleBindingRole {
					if blocked[finding.File] == nil {
						blocked[finding.File] = map[int]bool{}
					}
					blocked[finding.File][finding.Line] = true
				}
			}
		}
		for _, f := range g.Files {
			for _, c := range f.Concepts {
				id := stableID(model.KindMeaningConcept, scope, c.ID)
				if b.nodes[id].ID == "" {
					continue
				}
				for _, r := range []struct {
					value string
					kind  model.EdgeKind
				}{
					{c.Of, model.EdgeReferences}, {c.Extends, model.EdgeExtends}, {c.ValuesOf, model.EdgeValuesOf}, {c.UnitsOf, model.EdgeUnitsOf},
				} {
					b.meaningRef(id, scope, r.value, r.kind, c.Line)
				}
				if c.Measure != nil {
					for _, r := range c.Measure.Inputs {
						b.meaningRef(id, scope, r, model.EdgeMeasureInput, c.Line)
					}
					for _, r := range c.Measure.Dimensions {
						b.meaningRef(id, scope, r, model.EdgeMeasureDimension, c.Line)
					}
				}
				for _, bind := range c.Bindings {
					if invalidModels[f.Path] || blocked[f.Path][bind.Line] {
						b.diagnostic(id, "invalid-binding", "binding rejected by MeaningGraph checker", bind.Line)
						continue
					}
					// ModelSpec's binding target is a declared model entity/property path.
					parsed, ok := meaning.ParseModelRef(bind.Model)
					if !ok || parsed.Repo != "" || parsed.Pin != "" {
						b.diagnostic(id, "unresolved-binding", "binding names an external, pinned, or invalid ModelSpec URI "+bind.Model, bind.Line)
						continue
					}
					declaredPath := f.Models[parsed.Module]
					if declaredPath == "" {
						b.diagnostic(id, "unresolved-binding", "model "+parsed.Module+" is not declared by this graph", bind.Line)
						continue
					}
					modelPath := rel(b.root, filepath.Clean(filepath.Join(filepath.Dir(f.Path), filepath.FromSlash(declaredPath))))
					modelScope := b.modelPaths[modelPath]
					if modelScope == "" {
						b.diagnostic(id, "unresolved-binding", "declared model source is unavailable: "+declaredPath, bind.Line)
						continue
					}
					ref := b.moduleNames[modelScope] + "." + parsed.Name
					if bind.Property != "" {
						ref += "." + bind.Property
					}
					candidates := b.modelCandidates(ref, modelScope)
					if len(candidates) != 1 {
						b.diagnostic(id, "unresolved-binding", "unresolved or ambiguous binding "+ref, bind.Line)
						continue
					}
					b.edge(id, candidates[0], model.EdgeBindsTo, bind.Line, map[string]any{"role": bind.Role, "match": bind.Match, "note": bind.Note})
				}
			}
		}
	}
	return nil
}

func (b *builder) meaningRef(source, scope, raw string, kind model.EdgeKind, line int) {
	if raw == "" {
		return
	}
	ref, ok := meaning.ParseConceptRef(raw)
	if !ok {
		b.diagnostic(source, "invalid-reference", "invalid concept reference "+raw, line)
		return
	}
	if ref.Repo != "" && (ref.Repo != b.address || ref.Pin != "") {
		b.diagnostic(source, "unresolved-pinned-reference", "external reference "+raw+" requires a verified exact revision", line)
		n := b.nodes[source]
		refs, _ := n.Metadata["unresolvedReferences"].([]map[string]any)
		refs = append(refs, map[string]any{"uri": raw, "repository": ref.Repo, "revision": ref.Pin, "conceptId": ref.ID})
		n.Metadata["unresolvedReferences"] = refs
		b.put(n)
		return
	}
	target := stableID(model.KindMeaningConcept, scope, ref.ID)
	if b.nodes[target].ID == "" {
		b.diagnostic(source, "unresolved-reference", "missing concept "+raw, line)
		return
	}
	b.edge(source, target, kind, line, nil)
}

func (b *builder) annotations(files []string, sources []*store.Store) error {
	symbols := map[string][]model.Node{}
	for _, st := range sources {
		nodes, err := st.AllNodes()
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if n.Kind == model.KindFile || n.Kind == model.KindSemanticCode || n.Language == model.LangMeaningGraph || n.Language == model.LangModelSpec {
				continue
			}
			symbols[n.FilePath] = append(symbols[n.FilePath], n)
		}
	}
	for _, ns := range symbols {
		sort.Slice(ns, func(i, j int) bool {
			if ns[i].StartLine != ns[j].StartLine {
				return ns[i].StartLine < ns[j].StartLine
			}
			return ns[i].ID < ns[j].ID
		})
	}
	for _, path := range files {
		ns := symbols[path]
		if len(ns) == 0 {
			continue
		}
		data, err := b.snapshot.read(filepath.Join(b.root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			lineNo := i + 1
			raw := strings.TrimSpace(line)
			var ref string
			var candidates []string
			switch {
			case strings.HasPrefix(raw, "// modelspec: implements "):
				ref = strings.TrimSpace(strings.TrimPrefix(raw, "// modelspec: implements "))
				candidates = b.models[ref]
			case strings.HasPrefix(raw, "// meaninggraph: references "):
				ref = strings.TrimSpace(strings.TrimPrefix(raw, "// meaninggraph: references "))
				candidates = b.concepts[ref]
			default:
				continue
			}
			var code model.Node
			for _, n := range ns {
				if n.StartLine <= lineNo && lineNo <= n.EndLine {
					if code.ID == "" || n.EndLine-n.StartLine < code.EndLine-code.StartLine {
						code = n
					}
				}
			}
			if code.ID == "" {
				for _, n := range ns {
					if n.StartLine > lineNo && n.StartLine-lineNo <= 3 {
						code = n
						break
					}
				}
			}
			if code.ID == "" {
				continue
			}
			if len(candidates) != 1 {
				// The annotation is retained on a bridge even when its target is absent.
				bridge := b.bridge(code)
				b.diagnostic(bridge, "unresolved-annotation", "unresolved or ambiguous semantic target "+ref, lineNo)
				continue
			}
			bridge := b.bridge(code)
			b.edge(candidates[0], bridge, model.EdgeMapsToCode, lineNo, map[string]any{"annotation": raw, "sourcePath": path, "sourceLine": lineNo})
			b.edges[len(b.edges)-1].Provenance = "explicit_annotation"
		}
	}
	return nil
}
func (b *builder) bridge(code model.Node) string {
	id := "semantic_code:" + code.ID
	if b.nodes[id].ID != "" {
		return id
	}
	b.put(model.Node{ID: id, Kind: model.KindSemanticCode, Name: code.Name, QualifiedName: code.QualifiedName, FilePath: code.FilePath, Language: code.Language, StartLine: code.StartLine, EndLine: code.EndLine, StartColumn: code.StartColumn, EndColumn: code.EndColumn, Metadata: map[string]any{"canonicalCodeId": code.ID, "semanticMirror": true}})
	return id
}

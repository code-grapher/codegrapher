// Package gomod parses go.mod files into a typed, line-annotated structure.
// It is the single go.mod parser shared by the scope, resolve, mcp, and
// extract packages, wrapping golang.org/x/mod/modfile.
package gomod

import "golang.org/x/mod/modfile"

// File is the parsed content of a go.mod.
type File struct {
	Module    string
	Go        string // e.g. "1.22.3"
	Toolchain string // e.g. "go1.26.4", or ""
	Requires  []Require
	Replaces  []Replace
	Excludes  []Exclude
	Retracts  []Retract
}

// Require is a single require directive entry.
type Require struct {
	Path     string
	Version  string
	Indirect bool
	Line     int // 1-indexed line in go.mod
}

// Replace is a single replace directive entry. NewVersion is "" for a
// filesystem-path replacement (e.g. => ../fork).
type Replace struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
	Line       int
}

// Exclude is a single exclude directive entry.
type Exclude struct {
	Path    string
	Version string
	Line    int
}

// Retract is a single retract directive entry. Low == High for a single
// version; both are set for a range.
type Retract struct {
	Low       string
	High      string
	Rationale string
	Line      int
}

// Parse parses go.mod content. name is used only in error messages.
func Parse(name string, data []byte) (*File, error) {
	mf, err := modfile.Parse(name, data, nil)
	if err != nil {
		return nil, err
	}
	f := &File{}
	if mf.Module != nil {
		f.Module = mf.Module.Mod.Path
	}
	if mf.Go != nil {
		f.Go = mf.Go.Version
	}
	if mf.Toolchain != nil {
		f.Toolchain = mf.Toolchain.Name
	}
	for _, r := range mf.Require {
		f.Requires = append(f.Requires, Require{
			Path: r.Mod.Path, Version: r.Mod.Version, Indirect: r.Indirect,
			Line: lineOf(r.Syntax),
		})
	}
	for _, r := range mf.Replace {
		f.Replaces = append(f.Replaces, Replace{
			OldPath: r.Old.Path, OldVersion: r.Old.Version,
			NewPath: r.New.Path, NewVersion: r.New.Version,
			Line: lineOf(r.Syntax),
		})
	}
	for _, e := range mf.Exclude {
		f.Excludes = append(f.Excludes, Exclude{
			Path: e.Mod.Path, Version: e.Mod.Version, Line: lineOf(e.Syntax),
		})
	}
	for _, r := range mf.Retract {
		f.Retracts = append(f.Retracts, Retract{
			Low: r.Low, High: r.High, Rationale: r.Rationale, Line: lineOf(r.Syntax),
		})
	}
	return f, nil
}

func lineOf(s *modfile.Line) int {
	if s == nil {
		return 0
	}
	return s.Start.Line
}

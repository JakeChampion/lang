package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/caps"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/manifest"
)

// runCapabilities implements `fern -capabilities FILE.fern`: load the
// entry exactly as a compile would (fern.toml packages, workspaces,
// vendored deps, literate entries all resolve through loadEntry),
// type-check it (so method calls are rewritten to their hoisted
// names), and print the per-package capability report computed by
// internal/caps. Report mode only — the report itself never enforces
// (grants are enforced on the compile/-check/-interp paths via
// enforceCapabilities); see docs/PACKAGE-CAPABILITIES-BRIEF.md (#5361).
func runCapabilities(srcPath string, w io.Writer) error {
	e, err := loadEntry(srcPath)
	if err != nil {
		return err
	}
	prog := e.prog
	if err := constfold.Fold(prog, nil); err != nil {
		return e.format(err)
	}
	if _, err := checker.Check(prog); err != nil {
		return e.format(err)
	}
	rows := caps.Analyze(prog, packageResolver(srcPath))
	_, err = io.WriteString(w, caps.Format(rows))
	return err
}

// enforceCapabilities is phase 2 of docs/PACKAGE-CAPABILITIES-BRIEF.md
// (#5361): after a successful type-check, run the same reachability
// walk the -capabilities report uses (caps.Analyze) and hold every
// dependency package to its manifest grant. A package whose declaring
// dependency entry carries a `capabilities` key erroring outside the
// grant is E070 (returned as diag.Errors, position-less — the chain
// crosses module boundaries, so the message carries the attribution);
// a package with no key gets a warn-and-allow line on warnw, once per
// package+capability; the root package (and manifest-less modules,
// which fold into it) is never enforced or warned. Wired into every
// path a program compiles or runs through: `fern -check`, `-interp`,
// and codegen (run) — stdin programs skip it (no manifest can govern
// them).
func enforceCapabilities(srcPath string, prog *ast.Program, warnw io.Writer) error {
	resolve := packageInfoResolver(srcPath)
	enforceable := false
	for _, fn := range prog.Funcs {
		if name, _, root := resolve(fn.SourceModule); name != "" && !root {
			enforceable = true
			break
		}
	}
	if !enforceable {
		// Every function is the root package's (or stdlib, which folds
		// into its caller) — nothing to enforce or warn about, so skip
		// the reachability walk entirely on the common single-package
		// compile.
		return nil
	}
	type meta struct {
		root bool
		dirs map[string]bool
	}
	metas := map[string]*meta{}
	rows := caps.Analyze(prog, func(module string) string {
		name, dir, root := resolve(module)
		if name == "" {
			return ""
		}
		m := metas[name]
		if m == nil {
			m = &meta{dirs: map[string]bool{}}
			metas[name] = m
		}
		if dir != "" {
			m.dirs[dir] = true
		}
		m.root = m.root || root
		return name
	})
	grants := map[string]caps.Grant{}
	for name, m := range metas {
		g := caps.Grant{Root: m.root}
		for dir := range m.dirs {
			if cs, ok := prog.CapGrants[dir]; ok {
				g.Governed = true
				g.Caps = append(g.Caps, cs...)
			}
		}
		grants[name] = g
	}
	errsV, warnsV := caps.Enforce(rows, grants)
	for _, v := range warnsV {
		fmt.Fprintf(warnw, "warning: %s\n", v.Message())
	}
	if len(errsV) == 0 {
		return nil
	}
	var errs diag.Errors
	for _, v := range errsV {
		errs = append(errs, &checker.Error{Msg: v.Message(), ErrCode: "E070"})
	}
	return errs
}

// packageResolver maps a FuncDecl.SourceModule onto its report
// package name — see packageInfoResolver for the resolution rules.
func packageResolver(entryPath string) func(string) string {
	resolve := packageInfoResolver(entryPath)
	return func(module string) string {
		name, _, _ := resolve(module)
		return name
	}
}

// packageInfoResolver maps a FuncDecl.SourceModule onto its package
// identity: the nearest governing fern.toml's [package] name and
// directory, plus whether the module belongs to the ROOT package —
// the entry's own manifest, a manifest-less module, or a synthesised
// function with no module stamp (all of which fold into the root and
// are exempt from enforcement). Stdlib modules resolve to ("", "",
// false): caps.Analyze folds them into whichever package calls them.
// The root package of a program with no fern.toml reports as "(root)".
func packageInfoResolver(entryPath string) func(module string) (name, dir string, root bool) {
	rootName, rootDir := "(root)", ""
	if abs, err := filepath.Abs(entryPath); err == nil {
		rootDir = filepath.Dir(abs)
		if man, err := manifest.FindForDir(rootDir); err == nil && man != nil {
			rootDir = man.Dir
			if man.Name != "" {
				rootName = man.Name
			}
		}
	}
	type pkgID struct {
		name, dir string
		root      bool
	}
	cache := map[string]pkgID{}
	return func(module string) (string, string, bool) {
		if module == "" {
			return rootName, rootDir, true
		}
		if strings.HasPrefix(module, "stdlib://") {
			return "", "", false
		}
		mdir := filepath.Dir(module)
		if id, ok := cache[mdir]; ok {
			return id.name, id.dir, id.root
		}
		id := pkgID{name: rootName, dir: rootDir, root: true}
		if man, err := manifest.FindForDir(mdir); err == nil && man != nil {
			id.dir = man.Dir
			id.root = man.Dir == rootDir
			if man.Name != "" {
				id.name = man.Name
			}
		}
		cache[mdir] = id
		return id.name, id.dir, id.root
	}
}

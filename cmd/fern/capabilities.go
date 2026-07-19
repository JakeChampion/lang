package main

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/caps"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/manifest"
)

// runCapabilities implements `fern -capabilities FILE.fern`: load the
// entry exactly as a compile would (fern.toml packages, workspaces,
// vendored deps, literate entries all resolve through loadEntry),
// type-check it (so method calls are rewritten to their hoisted
// names), and print the per-package capability report computed by
// internal/caps. Report mode only — Phase 1 of
// docs/PACKAGE-CAPABILITIES-BRIEF.md (#5361); nothing is enforced.
func runCapabilities(srcPath string, w io.Writer) error {
	e, err := loadEntry(srcPath)
	if err != nil {
		return err
	}
	prog := e.prog
	if err := constfold.Fold(prog); err != nil {
		return e.format(err)
	}
	if _, err := checker.Check(prog); err != nil {
		return e.format(err)
	}
	rows := caps.Analyze(prog, packageResolver(srcPath))
	_, err = io.WriteString(w, caps.Format(rows))
	return err
}

// packageResolver maps a FuncDecl.SourceModule onto its report
// package name: the nearest governing fern.toml's [package] name, the
// root package's name for manifest-less modules (and for synthesised
// functions with no module stamp), and "" for stdlib modules — which
// caps.Analyze folds into whichever package calls them. The root
// package of a program with no fern.toml reports as "(root)".
func packageResolver(entryPath string) func(string) string {
	rootName := "(root)"
	if abs, err := filepath.Abs(entryPath); err == nil {
		if man, err := manifest.FindForDir(filepath.Dir(abs)); err == nil && man != nil && man.Name != "" {
			rootName = man.Name
		}
	}
	cache := map[string]string{}
	return func(module string) string {
		if module == "" {
			return rootName
		}
		if strings.HasPrefix(module, "stdlib://") {
			return ""
		}
		dir := filepath.Dir(module)
		if name, ok := cache[dir]; ok {
			return name
		}
		name := rootName
		if man, err := manifest.FindForDir(dir); err == nil && man != nil && man.Name != "" {
			name = man.Name
		}
		cache[dir] = name
		return name
	}
}

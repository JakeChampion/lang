package gates

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/caps"
	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

// Capabilities is phase 2 of docs/PACKAGE-CAPABILITIES-BRIEF.md
// (#5361): after a successful type-check, run the same reachability
// walk the -capabilities report uses (caps.Analyze) and hold every
// dependency package to its manifest grant. A package whose declaring
// dependency entry carries a `capabilities` key erroring outside the
// grant is E070 (returned as diag.Errors, position-less — the chain
// crosses module boundaries, so the message carries the attribution);
// a package with no key gets a warn-and-allow warning, once per
// package+capability; the root package is never enforced or warned.
// Manifest-less modules fold into it unless an explicit grant boundary
// governs them. Wired into every
// path a program compiles or runs through: `fern -check`, `-interp`,
// and codegen (run) — stdin programs skip it (no manifest can govern
// them).
func Capabilities(entry string, prog *ast.Program) ([]Warning, error) {
	resolve := packageInfoResolver(entry, prog.CapGrants)
	enforceable := false
	for _, fn := range prog.Funcs {
		if name, _, root := resolve(fn.BodyModule()); name != "" && !root {
			enforceable = true
			break
		}
	}
	if !enforceable {
		// Every function is the root package's (or stdlib, which folds
		// into its caller) — nothing to enforce or warn about, so skip
		// the reachability walk entirely on the common single-package
		// compile.
		return nil, nil
	}
	rows, grants := CapabilityRows(entry, prog)
	errsV, warnsV := caps.Enforce(rows, grants)
	var warns []Warning
	for _, v := range warnsV {
		warns = append(warns, Warning{Msg: v.Message()})
	}
	if len(errsV) == 0 {
		return warns, nil
	}
	var errs diag.Errors
	for _, v := range errsV {
		errs = append(errs, &checker.Error{Msg: v.Message(), ErrCode: "E070"})
	}
	return warns, errs
}

// CapabilityRows uses resolved package directories as authority identities.
// Display names are never keys: two manifests may use the same name.
func CapabilityRows(entry string, prog *ast.Program) ([]caps.Row, map[string]caps.Grant) {
	resolve := packageInfoResolver(entry, prog.CapGrants)
	names := map[string]string{}
	grants := map[string]caps.Grant{}
	rows := caps.Analyze(prog, func(module string) string {
		name, dir, root := resolve(module)
		if name == "" {
			return ""
		}
		names[dir] = name
		cs, governed := prog.CapGrants[dir]
		grants[dir] = caps.Grant{Root: root, Governed: governed, Caps: cs}
		return dir
	})
	counts := map[string]int{}
	for _, row := range rows {
		counts[names[row.Package]]++
	}
	for i := range rows {
		name := names[rows[i].Package]
		if counts[name] > 1 {
			name += " (" + rows[i].Package + ")"
		}
		rows[i].Name = name
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, grants
}

// packageInfoResolver maps a FuncDecl.SourceModule onto its package
// identity: the nearest governing fern.toml's [package] name and
// directory, plus whether the module belongs to the ROOT package —
// the entry's own manifest, an ungoverned manifest-less module, or a synthesised
// function with no module stamp (all of which fold into the root and
// are exempt from enforcement). Stdlib modules resolve to ("", "",
// false): caps.Analyze folds them into whichever package calls them.
// The root package of a program with no fern.toml reports as "(root)".
func packageInfoResolver(entry string, grants map[string][]string) func(module string) (name, dir string, root bool) {
	rootName, rootDir := "(root)", ""
	if abs, err := filepath.Abs(entry); err == nil {
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
		// A declared grant remains a boundary even without a manifest in
		// the dependency directory. Prefer the closest applicable boundary.
		for boundary := range grants {
			within := mdir == boundary || strings.HasPrefix(mdir, boundary+string(filepath.Separator))
			ownerContains := mdir == id.dir || strings.HasPrefix(mdir, id.dir+string(filepath.Separator))
			if within && (!ownerContains || len(boundary) > len(id.dir)) {
				id = pkgID{name: filepath.Base(boundary), dir: boundary, root: boundary == rootDir}
			}
		}
		cache[mdir] = id
		return id.name, id.dir, id.root
	}
}

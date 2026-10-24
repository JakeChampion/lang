package modload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/pkg/mvs"
	"github.com/jakechampion/lang/internal/pkg/pkgcache"
	"github.com/jakechampion/lang/internal/stdlib"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/tools/literate"
)

func Load(entryPath string) (*ast.Program, map[string]string, error) {
	return loadCore(entryPath, nil)
}

func LoadWith(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, error) {
	prog, srcs, _, err := loadCoreLit(entryPath, overrides)
	return prog, srcs, err
}

type LiterateModule struct {
	DocPath string
	DocSrc  string
	LineMap []literate.Line
}

func LoadWithLiterate(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, map[string]*LiterateModule, error) {
	return loadCoreLit(entryPath, overrides)
}

func LoadSource(src string) (*ast.Program, map[string]string, error) {
	return loadCore(SourceEntry, map[string]string{SourceEntry: src})
}

const SourceEntry = "/__fern_source__/main.fern"

func loadCore(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, error) {
	prog, srcs, _, err := loadCoreLit(entryPath, overrides)
	return prog, srcs, err
}

func loadCoreLit(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, map[string]*LiterateModule, error) {
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, nil, nil, err
	}
	loaded := map[string]*module{}
	stack := map[string]bool{}
	srcs := map[string]string{}
	lit := map[string]*LiterateModule{}
	mans := map[string]*manifest.Manifest{}
	if err := loadRecursive(entryAbs, loaded, stack, srcs, overrides, lit, mans); err != nil {
		return nil, srcs, lit, err
	}
	resolveCyclicImports(loaded)
	prog, err := combine(loaded, entryAbs)
	if err != nil {
		return nil, srcs, lit, err
	}
	if prog.CapGrants, err = capGrants(mans, entryAbs); err != nil {
		return nil, srcs, lit, err
	}
	return prog, srcs, lit, nil
}

func LoadStdlibFlat(paths []string) (*ast.Program, error) {
	return LoadStdlibFlatSkipping(paths, nil)
}

func LoadStdlibFlatSkipping(paths []string, skipPaths map[string]bool) (*ast.Program, error) {
	loaded := map[string]*module{}
	stack := map[string]bool{}
	srcs := map[string]string{}
	for _, p := range paths {
		if !stdlib.IsStdlibPath(p) {
			return nil, fmt.Errorf("LoadStdlibFlat: %q is not a stdlib path (must start with `std/` or `core/`)", p)
		}
		canonical := resolveImportPath("", p)
		if err := loadRecursive(canonical, loaded, stack, srcs, nil, map[string]*LiterateModule{}, map[string]*manifest.Manifest{}); err != nil {
			return nil, err
		}
	}
	resolveCyclicImports(loaded)
	combined := &ast.Program{
		LoadedStdlibPaths: map[string]bool{},
	}
	for path := range loaded {
		combined.LoadedStdlibPaths[path] = true
	}
	var firstErr error
	for _, p := range sortedPaths(loaded) {
		errs := loaded[p].rewriteAllOpts("", true, skipPaths)
		if len(errs) > 0 && firstErr == nil {
			firstErr = errs[0]
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	for _, p := range sortedPaths(loaded) {
		mod := loaded[p]
		if skipPaths[mod.path] {
			continue
		}
		for _, fn := range mod.prog.Funcs {
			fn.SourceModule = ""
		}
		for _, cd := range mod.prog.Consts {
			cd.SourceModule = ""
		}
		combined.Funcs = append(combined.Funcs, mod.prog.Funcs...)
		combined.Structs = append(combined.Structs, mod.prog.Structs...)
		combined.Enums = append(combined.Enums, mod.prog.Enums...)
		combined.Unions = append(combined.Unions, mod.prog.Unions...)
		combined.Consts = append(combined.Consts, mod.prog.Consts...)
		combined.Traits = append(combined.Traits, mod.prog.Traits...)
		combined.Impls = append(combined.Impls, mod.prog.Impls...)
		combined.Resources = append(combined.Resources, mod.prog.Resources...)
		combined.Comments = append(combined.Comments, mod.prog.Comments...)
	}
	return combined, nil
}

type module struct {
	path             string
	name             string
	manglePrefix     string
	prog             *ast.Program
	imports          map[string]*module
	importPaths      map[string]string
	publicFuncs      map[string]bool
	publicMethods    map[string]bool
	publicPlainFuncs map[string]bool
	publicStructs    map[string]bool
	publicConsts     map[string]bool
	publicEnums      map[string]bool
	allEnums         map[string]bool
	publicVariants   map[string]string
	allVariants      map[string]string
	allConsts        map[string]bool
	allDecls         map[string]bool
	reexports        map[string]string
	reexportTypes    map[string]string
	pubUses          []pubUseEntry
	packageScoped    map[string]bool
}

func samePackage(a, b string) bool {
	aStd := strings.HasPrefix(a, stdlibPrefix)
	bStd := strings.HasPrefix(b, stdlibPrefix)
	if aStd || bStd {
		return aStd && bStd
	}
	return filepath.Dir(a) == filepath.Dir(b)
}

type pubUseEntry struct {
	childPath string
	names     []string
	pos       ast.Position
}

func loadRecursive(path string, loaded map[string]*module, stack map[string]bool, srcs map[string]string, overrides map[string]string, lit map[string]*LiterateModule, mans map[string]*manifest.Manifest) error {
	if _, done := loaded[path]; done {
		return nil
	}
	if stack[path] {
		if strings.HasPrefix(path, stdlibPrefix) {
			return nil
		}
		return errImportCycle
	}
	stack[path] = true
	defer delete(stack, path)

	src, litMod, err := readModuleSource(path, overrides)
	if err != nil {
		return err
	}
	srcs[path] = src
	if litMod != nil {
		lit[path] = litMod
	}
	prog, err := parser.Parse(src)
	if err != nil {
		return diag.WithFile(err, path)
	}

	dir := ""
	if !strings.HasPrefix(path, stdlibPrefix) {
		dir = filepath.Dir(path)
		if _, err := manifestFor(dir, mans); err != nil {
			return err
		}
	}
	childPaths := make([]string, len(prog.Imports))
	for i, imp := range prog.Imports {
		childPaths[i], err = resolveImport(dir, imp.Path, mans, overrides)
		if err != nil {
			return unresolvedImport(path, imp.P, imp.Path, err)
		}
		if err := checkStdlibImport(path, imp.P, imp.Path); err != nil {
			return err
		}
		if err := loadRecursive(childPaths[i], loaded, stack, srcs, overrides, lit, mans); err != nil {
			return importFailure(path, imp.P, imp.Path, err)
		}
	}
	pubUsePaths := make([]string, len(prog.PubUses))
	for i, pu := range prog.PubUses {
		pubUsePaths[i], err = resolveImport(dir, pu.Path, mans, overrides)
		if err != nil {
			return unresolvedImport(path, pu.P, pu.Path, err)
		}
		if err := checkStdlibImport(path, pu.P, pu.Path); err != nil {
			return err
		}
		if err := loadRecursive(pubUsePaths[i], loaded, stack, srcs, overrides, lit, mans); err != nil {
			return importFailure(path, pu.P, pu.Path, err)
		}
	}

	mod := &module{
		path:             path,
		name:             importLocalName(path),
		manglePrefix:     importLocalName(path) + "__",
		prog:             prog,
		imports:          map[string]*module{},
		importPaths:      map[string]string{},
		publicFuncs:      map[string]bool{},
		publicMethods:    map[string]bool{},
		publicPlainFuncs: map[string]bool{},
		publicStructs:    map[string]bool{},
		publicConsts:     map[string]bool{},
		publicEnums:      map[string]bool{},
		allEnums:         map[string]bool{},
		publicVariants:   map[string]string{},
		allVariants:      map[string]string{},
		allConsts:        map[string]bool{},
		allDecls:         map[string]bool{},
		reexports:        map[string]string{},
		reexportTypes:    map[string]string{},
		packageScoped:    map[string]bool{},
	}
	for _, fn := range prog.Funcs {
		fn.SourceModule = path
		fn.SourceFile = path
		fn.DeclName = fn.Name
		if fn.Public {
			mod.publicFuncs[fn.Name] = true
			if fn.Receiver != nil {
				mod.publicMethods[fn.Name] = true
			} else {
				mod.publicPlainFuncs[fn.Name] = true
			}
		}
		mod.allDecls[fn.Name] = true
		if fn.PackageScoped {
			mod.packageScoped[fn.Name] = true
		}
	}
	for _, sd := range prog.Structs {
		sd.SourceModule = path
		if sd.Public {
			mod.publicStructs[sd.Name] = true
		}
		mod.allDecls[sd.Name] = true
		if sd.PackageScoped {
			mod.packageScoped[sd.Name] = true
		}
	}
	for _, ed := range prog.Enums {
		mod.allEnums[ed.Name] = true
		ed.SourceModule = path
		if ed.Public {
			mod.publicEnums[ed.Name] = true
		}
		for _, v := range ed.Variants {
			mod.allVariants[v.Name] = ed.Name
			if !ed.Public {
				continue
			}
			if _, dup := mod.publicVariants[v.Name]; dup {
				mod.publicVariants[v.Name] = ""
			} else {
				mod.publicVariants[v.Name] = ed.Name
			}
		}
		mod.allDecls[ed.Name] = true
		if ed.PackageScoped {
			mod.packageScoped[ed.Name] = true
		}
	}
	for _, ud := range prog.Unions {
		mod.allEnums[ud.Name] = true
		ud.SourceModule = path
		if ud.Public {
			mod.publicEnums[ud.Name] = true
		}
		mod.allDecls[ud.Name] = true
		if ud.PackageScoped {
			mod.packageScoped[ud.Name] = true
		}
	}
	for _, cd := range prog.Consts {
		cd.SourceModule = path
		mod.allConsts[cd.Name] = true
		if cd.Public {
			mod.publicConsts[cd.Name] = true
		}
		mod.allDecls[cd.Name] = true
		if cd.PackageScoped {
			mod.packageScoped[cd.Name] = true
		}
	}
	for _, td := range prog.Traits {
		td.SourceModule = path
		if td.Public {
			mod.publicStructs[td.Name] = true
		}
		mod.allDecls[td.Name] = true
		if td.PackageScoped {
			mod.packageScoped[td.Name] = true
		}
	}
	for _, impl := range prog.Impls {
		impl.SourceModule = path
	}
	for i, imp := range prog.Imports {
		child := loaded[childPaths[i]]
		if existing, dup := mod.imports[imp.LocalName]; dup && existing != child {
			return fmt.Errorf("%s: import name %q bound twice (paths %s and %s)",
				path, imp.LocalName, existing.path, child.path)
		}
		mod.imports[imp.LocalName] = child
		mod.importPaths[imp.LocalName] = childPaths[i]
	}
	for i, pu := range prog.PubUses {
		mod.pubUses = append(mod.pubUses, pubUseEntry{childPath: pubUsePaths[i], names: pu.Names, pos: pu.P})
	}
	loaded[path] = mod
	return nil
}

func resolveCyclicImports(loaded map[string]*module) {
	for _, mod := range loaded {
		for localName, childPath := range mod.importPaths {
			if mod.imports[localName] != nil {
				continue
			}
			if child, ok := loaded[childPath]; ok {
				mod.imports[localName] = child
			}
		}
	}
}

func resolveImportPath(importingDir, importPath string) string {
	if key := stdlib.ModuleKey(importPath); key != "" {
		return key
	}
	resolved := filepath.Join(importingDir, importPath)
	if filepath.Ext(resolved) == "" {
		resolved += ".fern"
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return resolved
	}
	return abs
}

func resolveImport(importingDir, importPath string, mans map[string]*manifest.Manifest, overrides map[string]string) (string, error) {
	if stdlib.IsStdlibPath(importPath) || importingDir == "" ||
		strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		return resolveImportPath(importingDir, importPath), nil
	}
	man, err := manifestFor(importingDir, mans)
	if err != nil {
		return "", err
	}
	if man == nil {
		return resolveImportPath(importingDir, importPath), nil
	}
	seg, rest, _ := strings.Cut(importPath, "/")
	if _, isDep := man.Deps[seg]; isDep {
		depDir, err := declaredDepDir(man, seg, mans)
		if err != nil {
			return "", err
		}
		return resolveDepImport(depDir, rest, mans)
	}
	p := resolveImportPath(importingDir, importPath)
	if moduleExists(p, overrides) {
		return p, nil
	}
	return "", &undeclaredImport{seg: seg, manifest: filepath.Join(man.Dir, manifest.FileName)}
}

type undeclaredImport struct{ seg, manifest string }

func (e *undeclaredImport) Error() string {
	return fmt.Sprintf("not found, and %q is not a declared dependency in %s (add it under [dependencies], e.g. %s = { path = \"../%s\" })",
		e.seg, e.manifest, e.seg, e.seg)
}

func declaredDepDir(man *manifest.Manifest, seg string, mans map[string]*manifest.Manifest) (string, error) {
	d := man.Deps[seg]
	if d.Workspace {
		return workspaceMemberDir(man.Dir, seg, mans)
	}
	vr := vendorRootFor(man.Dir)
	if vr == "" {
		if ws, werr := manifest.FindWorkspace(man.Dir); werr == nil && ws != nil {
			if st, err := os.Stat(filepath.Join(ws.Dir, "vendor")); err == nil && st.IsDir() {
				vr = ws.Dir
			}
		}
	}
	if vr != "" {
		depDir := filepath.Join(vr, "vendor", seg)
		if st, err := os.Stat(depDir); err == nil && st.IsDir() {
			return depDir, nil
		}
		return "", fmt.Errorf("dependency %q is not in the vendor directory %s — re-run `fern -vendor %s`", seg, filepath.Join(vr, "vendor"), filepath.Join(vr, manifest.FileName))
	}
	if d.Version != "" {
		return lockedDepDir(man.Dir, seg)
	}
	if d.URL != "" {
		depDir, present, err := pkgcache.Dir(d.Hash)
		if err != nil {
			return "", fmt.Errorf("dependency %q: %w", seg, err)
		}
		if !present {
			return "", fmt.Errorf("dependency %q (%s) is not in the package store — run `fern -fetch %s` to download and verify it", seg, d.Hash, filepath.Join(man.Dir, manifest.FileName))
		}
		return depDir, nil
	}
	depDir, _ := man.DepDir(seg)
	return depDir, nil
}

func capGrants(mans map[string]*manifest.Manifest, entry string) (map[string][]string, error) {
	byDir := map[string]*manifest.Manifest{}
	for _, m := range mans {
		if m != nil {
			byDir[m.Dir] = m
		}
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	rootDir := filepath.Dir(entry)
	if m, err := manifestFor(rootDir, mans); err != nil {
		return nil, err
	} else if m != nil {
		rootDir = m.Dir
	}
	type edge struct {
		from, to string
		caps     []string
	}
	var edges []edge
	var out map[string][]string
	for _, mdir := range dirs {
		man := byDir[mdir]
		for name, d := range man.Deps {
			if d.Capabilities == nil {
				continue
			}
			dir, err := declaredDepDir(man, name, mans)
			if err != nil {
				continue
			}
			if out == nil {
				out = map[string][]string{}
			}
			if _, seen := out[dir]; !seen {
				out[dir] = []string{}
			}
			edges = append(edges, edge{man.Dir, dir, d.Capabilities})
		}
	}
	changed := true
	for changed {
		changed = false
		for _, e := range edges {
			held, governed := out[e.from]
			for _, c := range e.caps {
				if governed && e.from != rootDir && !slices.Contains(held, c) {
					continue
				}
				if !slices.Contains(out[e.to], c) {
					out[e.to] = append(out[e.to], c)
					changed = true
				}
			}
		}
	}
	for dir := range out {
		sort.Strings(out[dir])
	}
	var viols []string
	for _, mdir := range dirs {
		man := byDir[mdir]
		held, governed := out[man.Dir]
		if !governed || man.Dir == rootDir {
			continue
		}
		heldSet := map[string]bool{}
		for _, c := range held {
			heldSet[c] = true
		}
		names := make([]string, 0, len(man.Deps))
		for name := range man.Deps {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, c := range man.Deps[name].Capabilities {
				if !heldSet[c] {
					viols = append(viols, fmt.Sprintf("%s: dependency %q of %q is granted '%s' but %q itself holds only [%s] (attenuation: a dependency may grant at most what it holds)",
						filepath.Join(man.Dir, manifest.FileName), name, man.Name, c, man.Name, strings.Join(held, ", ")))
				}
			}
		}
	}
	if len(viols) > 0 {
		return nil, errors.New(strings.Join(viols, "\n"))
	}
	return out, nil
}

func resolveDepImport(depDir, rest string, mans map[string]*manifest.Manifest) (string, error) {
	if rest != "" {
		return resolveImportPath(depDir, rest), nil
	}
	depMan, err := manifestFor(depDir, mans)
	if err != nil {
		return "", err
	}
	lib := manifest.DefaultLib
	if depMan != nil && depMan.Dir == depDir {
		lib = depMan.Lib
	}
	return filepath.Abs(filepath.Join(depDir, lib))
}

func LockedDepDir(manDir, name string) (string, error) { return lockedDepDir(manDir, name) }

func lockedDepDir(manDir, name string) (string, error) {
	locked, err := mvs.ReadLock(manDir)
	if err != nil {
		return "", err
	}
	if locked == nil {
		return "", fmt.Errorf("dependency %q is versioned but %s has no %s — run `fern -resolve %s`", name, manDir, mvs.LockFileName, filepath.Join(manDir, manifest.FileName))
	}
	sel, ok := locked[name]
	if !ok {
		return "", fmt.Errorf("dependency %q is not in %s — run `fern -resolve %s`", name, filepath.Join(manDir, mvs.LockFileName), filepath.Join(manDir, manifest.FileName))
	}
	if sel.Source.Path != "" {
		return sel.Source.Path, nil
	}
	dir, present, err := pkgcache.Dir(sel.Source.Hash)
	if err != nil {
		return "", fmt.Errorf("dependency %q: %w", name, err)
	}
	if !present {
		return "", fmt.Errorf("dependency %q@%s (%s) is not in the package store — run `fern -fetch %s`", name, sel.Version, sel.Source.Hash, filepath.Join(manDir, manifest.FileName))
	}
	return dir, nil
}

func workspaceMemberDir(manDir, name string, mans map[string]*manifest.Manifest) (string, error) {
	ws, err := manifest.FindWorkspace(manDir)
	if err != nil {
		return "", err
	}
	if ws == nil {
		return "", fmt.Errorf("dependency %q is `workspace = true` but no [workspace] governs %s", name, manDir)
	}
	for _, rel := range ws.Members {
		dir := ws.MemberDir(rel)
		mm, err := manifestFor(dir, mans)
		if err != nil {
			return "", err
		}
		if mm != nil && mm.Dir == dir && mm.Name == name {
			return dir, nil
		}
	}
	return "", fmt.Errorf("workspace dependency %q is not a member of the workspace at %s (its [workspace] members = %v)", name, filepath.Join(ws.Dir, manifest.FileName), ws.Members)
}

func vendorRootFor(manDir string) string {
	if st, err := os.Stat(filepath.Join(manDir, "vendor")); err == nil && st.IsDir() {
		return manDir
	}
	d := manDir
	for {
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		if filepath.Base(parent) == "vendor" {
			root := filepath.Dir(parent)
			if st, err := os.Stat(parent); err == nil && st.IsDir() && root != parent {
				return root
			}
		}
		d = parent
	}
}

func manifestFor(dir string, mans map[string]*manifest.Manifest) (*manifest.Manifest, error) {
	if m, ok := mans[dir]; ok {
		return m, nil
	}
	m, err := manifest.FindForDir(dir)
	if err != nil {
		return nil, err
	}
	mans[dir] = m
	return m, nil
}

func moduleExists(path string, overrides map[string]string) bool {
	if _, ok := overrides[path]; ok {
		return true
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if _, err := os.Stat(path + ".md"); err == nil {
		return true
	}
	return false
}

func checkStdlibImport(path string, pos ast.Position, importPath string) error {
	if !stdlib.IsStdlibPath(importPath) {
		return nil
	}
	if _, ok := stdlib.Resolve(importPath); ok {
		return nil
	}
	return diag.Errors{&importError{path: path, pos: pos, code: "E086", msg: fmt.Sprintf("unknown stdlib module %q", importPath)}}
}

var errImportCycle = errors.New("import cycle")

func importFailure(path string, pos ast.Position, importPath string, err error) error {
	switch {
	case errors.Is(err, errImportCycle):
		return &importError{path: path, pos: pos, code: "E086", msg: fmt.Sprintf("import %q closes an import cycle", importPath)}
	case os.IsNotExist(underlyingErr(err)):
		return &importError{path: path, pos: pos, code: "E086", msg: fmt.Sprintf("cannot resolve import %q: not found", importPath)}
	}
	return err
}

func unresolvedImport(path string, pos ast.Position, importPath string, err error) error {
	var u *undeclaredImport
	if errors.As(err, &u) {
		return &importError{path: path, pos: pos, code: "E086", msg: fmt.Sprintf("cannot resolve import %q: %s", importPath, u)}
	}
	return fmt.Errorf("%s: %w", path, err)
}

const stdlibPrefix = "stdlib://"

func readSource(path string, overrides map[string]string) (string, error) {
	if strings.HasPrefix(path, stdlibPrefix) {
		importPath := strings.TrimPrefix(path, stdlibPrefix)
		src, ok := stdlib.Resolve(importPath)
		if !ok {
			return "", fmt.Errorf("unknown stdlib module %q", importPath)
		}
		return src, nil
	}
	if overrides != nil {
		if src, ok := overrides[path]; ok {
			return src, nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(b), nil
}

func readModuleSource(path string, overrides map[string]string) (string, *LiterateModule, error) {
	if strings.HasPrefix(path, stdlibPrefix) {
		m, err := stdlib.ReadModule(strings.TrimPrefix(path, stdlibPrefix))
		if err != nil {
			return "", nil, err
		}
		if m.DocPath == "" {
			return m.Code, nil, nil
		}
		return m.Code, &LiterateModule{DocPath: stdlibPrefix + m.DocPath, DocSrc: m.DocSource, LineMap: m.LineMap}, nil
	}
	src, err := readSource(path, overrides)
	if err == nil {
		return src, nil, nil
	}
	if strings.HasPrefix(path, stdlibPrefix) || !strings.HasSuffix(path, ".fern") || !os.IsNotExist(underlyingErr(err)) {
		return "", nil, err
	}
	docPath := path + ".md"
	docSrc, derr := readSource(docPath, overrides)
	if derr != nil {
		return "", nil, err
	}
	doc := literate.Parse(docSrc)
	if doc.HasFiles() {
		return "", nil, fmt.Errorf("cannot import multi-file literate document %s: it tangles to several modules, not one importable module", docPath)
	}
	code, lineMap, terr := doc.Tangle()
	if terr != nil {
		return "", nil, fmt.Errorf("%s", diag.Format(docPath, docSrc, terr))
	}
	return code, &LiterateModule{DocPath: docPath, DocSrc: docSrc, LineMap: lineMap}, nil
}

func underlyingErr(err error) error {
	if u := errors.Unwrap(err); u != nil {
		return u
	}
	return err
}

func importClosures(loaded map[string]*module) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for path, mod := range loaded {
		closure := map[string]bool{path: true}
		stack := []*module{mod}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			reach := func(child *module) {
				if child == nil || closure[child.path] {
					return
				}
				closure[child.path] = true
				stack = append(stack, child)
			}
			for _, child := range cur.imports {
				reach(child)
			}
			for _, pu := range cur.pubUses {
				reach(loaded[pu.childPath])
			}
		}
		out[path] = closure
	}
	return out
}

func directImports(loaded map[string]*module) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for path, mod := range loaded {
		direct := map[string]bool{path: true}
		for _, childPath := range mod.importPaths {
			direct[childPath] = true
		}
		out[path] = direct
	}
	return out
}

func importLocalName(path string) string {
	trimmed := strings.TrimPrefix(path, stdlibPrefix)
	base := filepath.Base(trimmed)
	if ext := filepath.Ext(base); ext == ".fern" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

func combine(loaded map[string]*module, entryPath string) (*ast.Program, error) {
	combined := &ast.Program{
		ModuleImports:     importClosures(loaded),
		EntryModule:       entryPath,
		DirectImports:     directImports(loaded),
		LoadedStdlibPaths: map[string]bool{},
	}
	for path := range loaded {
		if strings.HasPrefix(path, stdlibPrefix) {
			combined.LoadedStdlibPaths[path] = true
		}
	}
	assignManglePrefixes(loaded, entryPath)
	if err := resolveReexports(loaded); err != nil {
		return nil, err
	}
	var firstErr error
	for _, p := range sortedPaths(loaded) {
		errs := loaded[p].rewriteAll(loaded[p].manglePrefix)
		if len(errs) > 0 && firstErr == nil {
			firstErr = errs[0]
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	for _, p := range sortedPaths(loaded) {
		mod := loaded[p]
		combined.Funcs = append(combined.Funcs, mod.prog.Funcs...)
		combined.Structs = append(combined.Structs, mod.prog.Structs...)
		combined.Enums = append(combined.Enums, mod.prog.Enums...)
		combined.Unions = append(combined.Unions, mod.prog.Unions...)
		combined.Consts = append(combined.Consts, mod.prog.Consts...)
		combined.Traits = append(combined.Traits, mod.prog.Traits...)
		combined.Impls = append(combined.Impls, mod.prog.Impls...)
		combined.Resources = append(combined.Resources, mod.prog.Resources...)
		combined.Comments = append(combined.Comments, mod.prog.Comments...)
		for _, tr := range mod.prog.TypeRefs {
			tr.SourceModule = p
			combined.TypeRefs = append(combined.TypeRefs, tr)
		}
		if p == entryPath {
			combined.TodoSites = append(combined.TodoSites, mod.prog.TodoSites...)
		}
	}
	return combined, nil
}

func sortedPaths(loaded map[string]*module) []string {
	paths := make([]string, 0, len(loaded))
	for p := range loaded {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func assignManglePrefixes(loaded map[string]*module, entryPath string) {
	paths := sortedPaths(loaded)

	used := map[string]bool{}
	for _, p := range paths {
		m := loaded[p]
		if m.path == entryPath {
			m.manglePrefix = ""
			continue
		}
		if strings.HasPrefix(m.path, stdlibPrefix) {
			base := sanitizeManglePrefix(m.name)
			m.manglePrefix = base + "__"
			for k := 1; used[m.manglePrefix]; k++ {
				m.manglePrefix = fmt.Sprintf("%s_%d__", base, k)
			}
			used[m.manglePrefix] = true
		}
	}
	for _, p := range paths {
		m := loaded[p]
		if m.path == entryPath || strings.HasPrefix(m.path, stdlibPrefix) {
			continue
		}
		base := sanitizeManglePrefix(m.name)
		pref := base + "__"
		for k := 1; used[pref]; k++ {
			pref = fmt.Sprintf("%s_%d__", base, k)
		}
		m.manglePrefix = pref
		used[pref] = true
	}
}

func sanitizeManglePrefix(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
			b.WriteByte(c)
		case c >= '0' && c <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func (m *module) exportedMangled(name string) (mangled string, isType bool, ok bool) {
	if mg, ok := m.reexports[name]; ok {
		return mg, false, true
	}
	if mg, ok := m.reexportTypes[name]; ok {
		return mg, true, true
	}
	if m.publicFuncs[name] || m.publicConsts[name] {
		return m.manglePrefix + name, false, true
	}
	if m.publicStructs[name] || m.publicEnums[name] {
		return m.manglePrefix + name, true, true
	}
	return "", false, false
}

func resolveReexports(loaded map[string]*module) error {
	paths := sortedPaths(loaded)

	type pending struct {
		mod     *module
		child   *module
		name    string
		pos     ast.Position
		modPath string
	}
	var work []pending
	for _, p := range paths {
		mod := loaded[p]
		for _, pu := range mod.pubUses {
			child := loaded[pu.childPath]
			for _, name := range pu.names {
				work = append(work, pending{mod: mod, child: child, name: name, pos: pu.pos, modPath: p})
			}
		}
	}

	for len(work) > 0 {
		progress := false
		remaining := work[:0]
		for _, w := range work {
			if w.child == nil {
				return fmt.Errorf("%s: `pub use` target module was not loaded", w.modPath)
			}
			mangled, isType, ok := w.child.exportedMangled(w.name)
			if !ok {
				remaining = append(remaining, w)
				continue
			}
			if isType {
				w.mod.reexportTypes[w.name] = mangled
				w.mod.publicStructs[w.name] = true
				if w.child.allEnums[w.name] {
					w.mod.allEnums[w.name] = true
				}
			} else {
				w.mod.reexports[w.name] = mangled
				w.mod.publicFuncs[w.name] = true
			}
			progress = true
		}
		work = remaining
		if len(work) > 0 && !progress {
			w := work[0]
			return fmt.Errorf("%s:%s: module %q does not export %q (cannot `pub use` it)", w.modPath, w.pos, importLocalName(w.child.path), w.name)
		}
	}
	return nil
}

func isRuntimeHelperName(name string) bool {
	if strings.HasPrefix(name, "__method_") {
		return true
	}
	if strings.HasPrefix(name, "__map_") {
		return true
	}
	if strings.HasPrefix(name, "__mapiter_") {
		return true
	}
	if name == "map_new_impl" {
		return true
	}
	return false
}

func (m *module) rewriteAll(selfPrefix string) []error {
	return m.rewriteAllOpts(selfPrefix, false, nil)
}

func (m *module) rewriteAllOpts(selfPrefix string, flatNamespace bool, skipPaths map[string]bool) []error {
	ownFuncs := map[string]bool{}
	ownStructs := map[string]bool{}
	ownEnums := map[string]bool{}
	for _, ed := range m.prog.Enums {
		ownEnums[ed.Name] = true
	}
	for _, ud := range m.prog.Unions {
		ownEnums[ud.Name] = true
	}
	ownConsts := map[string]bool{}
	for _, fn := range m.prog.Funcs {
		if isRuntimeHelperName(fn.Name) {
			continue
		}
		if fn.Receiver != nil || fn.AssocType != "" {
			continue
		}
		ownFuncs[fn.Name] = true
	}
	for _, sd := range m.prog.Structs {
		ownStructs[sd.Name] = true
	}
	for _, cd := range m.prog.Consts {
		ownConsts[cd.Name] = true
	}
	ownTraits := map[string]bool{}
	for _, td := range m.prog.Traits {
		ownTraits[td.Name] = true
	}

	r := &rewriter{
		modPath:       m.path,
		selfPrefix:    selfPrefix,
		ownFuncs:      ownFuncs,
		ownStructs:    ownStructs,
		ownEnums:      ownEnums,
		ownConsts:     ownConsts,
		ownTraits:     ownTraits,
		imports:       m.imports,
		flatNamespace: flatNamespace,
		skipPaths:     skipPaths,
	}
	for _, fn := range m.prog.Funcs {
		restoreTypeParams := r.bindTypeParams(fn.TypeParams)
		if fn.Receiver == nil && fn.AssocType == "" && !isRuntimeHelperName(fn.Name) {
			fn.Name = selfPrefix + fn.Name
		} else if fn.Receiver != nil {
			r.rewriteType(&fn.Receiver.Type)
		} else if fn.AssocType != "" {
			fn.AssocType = r.rewriteStructName(fn.AssocType)
		}
		for i := range fn.Params {
			r.rewriteType(&fn.Params[i].Type)
		}
		r.rewriteType(&fn.ReturnType)
		r.rewriteFuncBody(fn)
		restoreTypeParams()
	}
	for _, sd := range m.prog.Structs {
		restoreTypeParams := r.bindTypeParams(sd.TypeParams)
		sd.Name = selfPrefix + sd.Name
		for i := range sd.Fields {
			r.rewriteType(&sd.Fields[i].Type)
		}
		for i, dn := range sd.Derives {
			sd.Derives[i] = r.rewriteTraitNameAt(dn, sd.P)
		}
		restoreTypeParams()
	}
	for _, ed := range m.prog.Enums {
		restoreTypeParams := r.bindTypeParams(ed.TypeParams)
		ed.Name = selfPrefix + ed.Name
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				r.rewriteType(&ed.Variants[i].Payloads[j])
			}
		}
		for i, dn := range ed.Derives {
			ed.Derives[i] = r.rewriteTraitNameAt(dn, ed.P)
		}
		restoreTypeParams()
	}
	for _, ud := range m.prog.Unions {
		restoreTypeParams := r.bindTypeParams(ud.TypeParams)
		ud.Name = selfPrefix + ud.Name
		for i := range ud.Members {
			var t ast.Type = ud.Members[i]
			r.rewriteType(&t)
			if st, ok := t.(ast.StructType); ok {
				ud.Members[i] = st
			}
		}
		restoreTypeParams()
	}
	for _, cd := range m.prog.Consts {
		cd.Name = selfPrefix + cd.Name
		r.rewriteType(&cd.Type)
		r.rewriteExpr(&cd.Value)
	}
	for _, td := range m.prog.Traits {
		restoreTypeParams := r.bindTypeParams(td.TypeParams)
		td.Name = selfPrefix + td.Name
		for i := range td.Methods {
			restoreMethodParams := r.bindTypeParams(td.Methods[i].TypeParams)
			for j := range td.Methods[i].Params {
				r.rewriteType(&td.Methods[i].Params[j].Type)
			}
			r.rewriteType(&td.Methods[i].Result)
			r.rewriteBounds(td.Methods[i].Bounds, td.Methods[i].BoundArgs, td.Methods[i].P)
			r.rewriteTraitDefaultBody(&td.Methods[i])
			restoreMethodParams()
		}
		for k, sup := range td.Supertraits {
			td.Supertraits[k] = r.rewriteTraitNameAt(sup, td.NamePos)
			if k < len(td.SupertraitArgs) {
				for j := range td.SupertraitArgs[k] {
					r.rewriteType(&td.SupertraitArgs[k][j])
				}
			}
		}
		restoreTypeParams()
	}
	for _, impl := range m.prog.Impls {
		restoreTypeParams := r.bindTypeParams(impl.TypeParams)
		r.rewriteType(&impl.Type)
		impl.Trait = r.rewriteTraitNameAt(impl.Trait, impl.TraitPos)
		for i := range impl.TraitArgs {
			r.rewriteType(&impl.TraitArgs[i])
		}
		r.rewriteBounds(impl.Bounds, impl.BoundArgs, impl.P)
		restoreTypeParams()
	}
	for _, fn := range m.prog.Funcs {
		restoreTypeParams := r.bindTypeParams(fn.TypeParams)
		fn.ImplTrait = r.resolveTraitName(fn.ImplTrait)
		r.rewriteBounds(fn.Bounds, fn.BoundArgs, fn.P)
		restoreTypeParams()
	}
	return r.errs
}

func (r *rewriter) rewriteBounds(bounds map[string][]string, boundArgs map[string][][]ast.Type, p ast.Position) {
	for tp, traits := range bounds {
		for k, tn := range traits {
			bounds[tp][k] = r.rewriteTraitNameAt(tn, p)
		}
	}
	for _, perBound := range boundArgs {
		for _, args := range perBound {
			for i := range args {
				r.rewriteType(&args[i])
			}
		}
	}
}

type rewriter struct {
	modPath       string
	selfPrefix    string
	ownFuncs      map[string]bool
	ownStructs    map[string]bool
	ownEnums      map[string]bool
	ownConsts     map[string]bool
	ownTraits     map[string]bool
	imports       map[string]*module
	errs          []error
	localVars     map[string]bool
	typeParams    map[string]bool
	flatNamespace bool
	skipPaths     map[string]bool
}

func (r *rewriter) refuse(pos ast.Position, format string, args ...any) {
	r.errs = append(r.errs, &importError{path: r.modPath, pos: pos, code: "E087", msg: fmt.Sprintf(format, args...)})
}

func (r *rewriter) packageScopedOK(mod *module, name string, pos ast.Position) (ok, handled bool) {
	if !mod.packageScoped[name] {
		return false, false
	}
	if samePackage(r.modPath, mod.path) {
		return true, true
	}
	r.refuse(pos, "%s.%s is `pub(package)` — only modules in the same package as %s may use it", mod.name, name, mod.name)
	return false, true
}

func (r *rewriter) reportUndeclared(mod *module, name, kind string, pos ast.Position) {
	hint := didYouMean(name, mod.exportedNames())
	if hint == "" {
		if m := closest(name, mod.methodNames()); m != "" {
			hint = fmt.Sprintf(" — %q is a method, call it on the value: `x.%s(…)`", m, m)
		}
	}
	r.refuse(pos, "module %q has no %s %q%s", mod.name, kind, name, hint)
}

func (m *module) methodNames() []string {
	out := make([]string, 0, len(m.publicMethods))
	for n := range m.publicMethods {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *module) exportedNames() []string {
	seen := map[string]bool{}
	for _, set := range []map[string]bool{m.publicFuncs, m.publicStructs, m.publicEnums, m.publicConsts} {
		for n := range set {
			seen[n] = true
		}
	}
	for n := range m.publicMethods {
		delete(seen, n)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func didYouMean(name string, candidates []string) string {
	best := closest(name, candidates)
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func closest(name string, candidates []string) string {
	best, bestD := "", 0
	max := len(name)/3 + 1
	for _, c := range candidates {
		d := editDistance(name, c)
		if d <= max && (best == "" || d < bestD) {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = prev[j] + 1
			if cur[j-1]+1 < cur[j] {
				cur[j] = cur[j-1] + 1
			}
			if prev[j-1]+cost < cur[j] {
				cur[j] = prev[j-1] + cost
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func (r *rewriter) checkPublicFunc(mod *module, fn string, pos ast.Position) {
	if mod.publicMethods[fn] && !mod.publicPlainFuncs[fn] {
		r.refuse(pos, "%s.%s is a method — call it on the value: `x.%s(…)`", mod.name, fn, fn)
		return
	}
	if mod.publicFuncs[fn] {
		return
	}
	if _, handled := r.packageScopedOK(mod, fn, pos); handled {
		return
	}
	if !mod.allDecls[fn] {
		r.reportUndeclared(mod, fn, "function", pos)
		return
	}
	r.refuse(pos, "%s.%s is not exported (declare it as `pub function %s …` to make it accessible from other modules)", mod.name, fn, fn)
}

func (r *rewriter) checkPublicStruct(mod *module, name string, pos ast.Position) {
	if mod.publicStructs[name] || mod.publicEnums[name] {
		return
	}
	if _, handled := r.packageScopedOK(mod, name, pos); handled {
		return
	}
	if !mod.allDecls[name] {
		r.reportUndeclared(mod, name, "type", pos)
		return
	}
	r.refuse(pos, "%s.%s is not exported (declare it as `pub struct %s …` to make it accessible from other modules)", mod.name, name, name)
}

func (r *rewriter) checkPublicValue(mod *module, name string, pos ast.Position) {
	if mod.publicFuncs[name] || mod.publicConsts[name] {
		return
	}
	if _, handled := r.packageScopedOK(mod, name, pos); handled {
		return
	}
	if !mod.allDecls[name] {
		r.reportUndeclared(mod, name, "function or const", pos)
		return
	}
	hint := "function"
	if mod.allConsts[name] {
		hint = "const"
	}
	r.refuse(pos, "%s.%s is not exported (declare it as `pub %s %s …` to make it accessible from other modules)", mod.name, name, hint, name)
}

func (r *rewriter) importedModule(localName string) (*module, string, bool) {
	mod, ok := r.imports[localName]
	if !ok {
		return nil, "", false
	}
	if r.flatNamespace {
		if r.skipPaths[mod.path] {
			return mod, mod.manglePrefix, true
		}
		return mod, "", true
	}
	return mod, mod.manglePrefix, true
}

func (r *rewriter) rewriteFuncBody(fn *ast.FuncDecl) {
	prev := r.localVars
	r.localVars = map[string]bool{}
	r.withScope(paramNames(fn.Params), func() { r.rewriteBlock(fn.Body) })
	r.localVars = prev
}

func (r *rewriter) withScope(names []string, f func()) {
	prev := r.localVars
	locals := make(map[string]bool, len(prev)+len(names))
	for k := range prev {
		locals[k] = true
	}
	for _, n := range names {
		locals[n] = true
	}
	r.localVars = locals
	f()
	r.localVars = prev
}

func (r *rewriter) bindStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Var:
		r.localVars[x.Name] = true
	case *ast.Destructure:
		for _, name := range destructureNames(x) {
			r.localVars[name] = true
		}
	}
}

func destructureNames(d *ast.Destructure) []string {
	names := append([]string{}, d.Names...)
	if d.AtName != "" {
		names = append(names, d.AtName)
	}
	for _, sub := range d.Nested {
		if sub != nil {
			names = append(names, destructureNames(sub)...)
		}
	}
	return names
}

func paramNames(params []ast.Param) []string {
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return names
}

func (r *rewriter) rewriteTraitDefaultBody(m *ast.TraitMethod) {
	if m.Body == nil {
		return
	}
	prev := r.localVars
	r.localVars = map[string]bool{}
	r.withScope(paramNames(m.Params), func() { r.rewriteBlock(m.Body) })
	r.localVars = prev
}

func (r *rewriter) rewriteBlock(b *ast.Block) {
	if b == nil {
		return
	}
	r.withScope(nil, func() {
		for _, s := range b.Stmts {
			r.rewriteStmt(s)
			r.bindStmt(s)
		}
	})
}

func (r *rewriter) rewriteStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Block:
		r.rewriteBlock(x)
	case *ast.If:
		r.rewriteExpr(&x.Cond)
		r.rewriteStmt(x.Then)
		if x.Else != nil {
			r.rewriteStmt(x.Else)
		}
	case *ast.While:
		r.rewriteExpr(&x.Cond)
		r.rewriteStmt(x.Body)
	case *ast.Loop:
		r.rewriteStmt(x.Body)
	case *ast.For:
		r.withScope(nil, func() {
			if x.Init != nil {
				r.rewriteStmt(x.Init)
				r.bindStmt(x.Init)
			}
			r.rewriteExpr(&x.Cond)
			if x.Step != nil {
				r.rewriteStmt(x.Step)
			}
			r.rewriteStmt(x.Body)
		})
	case *ast.ForEach:
		if x.Pattern != nil {
			r.rewriteDestructureStructNames(x.Pattern)
		}
		r.rewriteExpr(&x.Iter)
		names := []string{x.Var}
		if x.Pattern != nil {
			names = append(names, destructureNames(x.Pattern)...)
		}
		r.withScope(names, func() { r.rewriteStmt(x.Body) })
	case *ast.Return:
		if x.Value != nil {
			r.rewriteExpr(&x.Value)
		}
	case *ast.Var:
		r.rewriteType(&x.Type)
		r.rewriteExpr(&x.Init)
	case *ast.Destructure:
		r.rewriteDestructureStructNames(x)
		r.rewriteExpr(&x.Init)
	case *ast.ExprStmt:
		r.rewriteExpr(&x.Expr)
	case *ast.Match:
		r.rewriteExpr(&x.Tag)
		for _, arm := range x.Arms {
			r.rewriteVariantPattern(&arm.VariantModule, &arm.VariantName, arm.P)
			r.rewritePatElems(arm.Payloads, arm.TupleElems, arm.P)
			r.withScope(arm.Binders(), func() {
				if arm.Guard != nil {
					r.rewriteExpr(&arm.Guard)
				}
				r.rewriteBlock(arm.Body)
			})
		}
	case *ast.Defer:
		r.rewriteExpr(&x.Expr)
	case *ast.FuncDecl:
		for i := range x.Params {
			r.rewriteType(&x.Params[i].Type)
		}
		r.rewriteType(&x.ReturnType)
		r.rewriteNestedFuncBody(x.Params, x.Body)
	}
}

func (r *rewriter) rewriteNestedFuncBody(params []ast.Param, body *ast.Block) {
	r.withScope(paramNames(params), func() { r.rewriteBlock(body) })
}

func (r *rewriter) rewriteExpr(slot *ast.Expr) {
	if slot == nil || *slot == nil {
		return
	}
	switch x := (*slot).(type) {
	case *ast.Ident:
		if r.localVars[x.Name] {
			return
		}
		if r.ownFuncs[x.Name] || r.ownConsts[x.Name] {
			x.Name = r.selfPrefix + x.Name
		}
	case *ast.Call:
		if args := r.rewriteTypeArgs(x.TypeArgs); args != nil {
			x.TypeArgs = args
		}
		if fa, ok := x.Callee.(*ast.FieldAccess); ok {
			if id, ok := fa.Target.(*ast.Ident); ok && !r.localVars[id.Name] && !r.typeParams[id.Name] {
				if mod, prefix, ok := r.importedModule(id.Name); ok {
					if en, handled := r.qualifiedVariant(mod, prefix, fa.Field, fa.P); handled {
						if en != "" {
							x.Callee = &ast.Ident{P: id.P, Name: fa.Field, EnumName: en}
						}
						for i := range x.Args {
							r.rewriteExpr(&x.Args[i])
						}
						return
					}
					r.checkPublicFunc(mod, fa.Field, fa.P)
					mangled := prefix + fa.Field
					if rx, ok := mod.reexports[fa.Field]; ok {
						mangled = rx
					}
					x.Module = &ast.ModuleCallSite{
						Module:    id.Name,
						ModulePos: id.P,
						Field:     fa.Field,
						FieldPos:  fa.FieldPos,
						Mangled:   mangled,
					}
					x.Callee = &ast.Ident{P: id.P, Name: mangled}
					for i := range x.Args {
						r.rewriteExpr(&x.Args[i])
					}
					return
				}
			}
		}
		if ix, ok := x.Callee.(*ast.Index); ok && r.rewriteTypeArgIndex(&ix.Idx) {
			r.rewriteExpr(&ix.Array)
		} else {
			r.rewriteExpr(&x.Callee)
		}
		for i := range x.Args {
			r.rewriteExpr(&x.Args[i])
		}
	case *ast.FieldAccess:
		if id, ok := x.Target.(*ast.Ident); ok && r.typeParams[id.Name] && !r.localVars[id.Name] {
			return
		}
		if qual, ok := x.Target.(*ast.FieldAccess); ok {
			if id, ok := qual.Target.(*ast.Ident); ok && !r.localVars[id.Name] && !r.typeParams[id.Name] {
				if mod, _, ok := r.importedModule(id.Name); ok && mod.allEnums[qual.Field] {
					x.Target = &ast.Ident{P: qual.P, Name: r.rewriteStructNameAt(id.Name+"."+qual.Field, qual.P)}
					return
				}
			}
		}
		if id, ok := x.Target.(*ast.Ident); ok {
			if r.ownEnums[id.Name] && !r.localVars[id.Name] && !r.typeParams[id.Name] {
				id.Name = r.selfPrefix + id.Name
				return
			}
			if mod, prefix, ok := r.importedModule(id.Name); ok && !r.localVars[id.Name] && !r.typeParams[id.Name] {
				if en, handled := r.qualifiedVariant(mod, prefix, x.Field, x.P); handled {
					if en != "" {
						*slot = &ast.Ident{P: id.P, Name: x.Field, EnumName: en}
					}
					return
				}
				r.checkPublicValue(mod, x.Field, x.P)
				target := prefix + x.Field
				if rx, ok := mod.reexports[x.Field]; ok {
					target = rx
				}
				*slot = &ast.Ident{P: id.P, Name: target}
				return
			}
		}
		r.rewriteExpr(&x.Target)
	case *ast.Binary:
		r.rewriteExpr(&x.Left)
		r.rewriteExpr(&x.Right)
	case *ast.Unary:
		r.rewriteExpr(&x.Operand)
	case *ast.Index:
		r.rewriteExpr(&x.Array)
		r.rewriteExpr(&x.Idx)
	case *ast.ArrayLit:
		for i := range x.Elems {
			r.rewriteExpr(&x.Elems[i])
		}
	case *ast.Assign:
		r.rewriteExpr(&x.Target)
		r.rewriteExpr(&x.Value)
	case *ast.IfExpr:
		r.rewriteExpr(&x.Cond)
		r.rewriteExpr(&x.Then)
		r.rewriteExpr(&x.Else)
	case *ast.TryOp:
		r.rewriteExpr(&x.Inner)
	case *ast.CastExpr:
		r.rewriteExpr(&x.Inner)
		r.rewriteType(&x.Target)
	case *ast.DowncastExpr:
		r.rewriteExpr(&x.Inner)
		r.rewriteType(&x.Target)
	case *ast.SliceExpr:
		r.rewriteExpr(&x.Source)
		if x.Low != nil {
			r.rewriteExpr(&x.Low)
		}
		if x.High != nil {
			r.rewriteExpr(&x.High)
		}
	case *ast.FString:
		for i := range x.Parts {
			if x.Parts[i].Expr != nil {
				r.rewriteExpr(&x.Parts[i].Expr)
			}
		}
		if x.Desugared != nil {
			r.rewriteExpr(&x.Desugared)
		}
	case *ast.MakeClosure:
		for i := range x.Captures {
			r.rewriteExpr(&x.Captures[i])
		}
	case *ast.Lambda:
		for i := range x.Params {
			r.rewriteType(&x.Params[i].Type)
		}
		r.rewriteType(&x.ReturnType)
		r.rewriteNestedFuncBody(x.Params, x.Body)
	case *ast.MatchExpr:
		r.rewriteExpr(&x.Tag)
		for _, arm := range x.Arms {
			r.rewriteVariantPattern(&arm.VariantModule, &arm.VariantName, arm.P)
			r.rewritePatElems(arm.Payloads, arm.TupleElems, arm.P)
			r.withScope(arm.Binders(), func() {
				if arm.Guard != nil {
					r.rewriteExpr(&arm.Guard)
				}
				r.rewriteExpr(&arm.Body)
			})
		}
	case *ast.BlockExpr:
		r.withScope(nil, func() {
			for _, st := range x.Stmts {
				r.rewriteStmt(st)
				r.bindStmt(st)
			}
			if x.Tail != nil {
				r.rewriteExpr(&x.Tail)
			}
		})
	case *ast.StructLit:
		x.TypeName = r.rewriteStructNameAt(x.TypeName, x.P)
		if args := r.rewriteTypeArgs(x.TypeArgs); args != nil {
			x.TypeArgs = args
		}
		for i := range x.Fields {
			r.rewriteExpr(&x.Fields[i].Value)
		}
		if x.Base != nil {
			r.rewriteExpr(&x.Base)
		}
	case *ast.TupleLit:
		for i := range x.Elems {
			r.rewriteExpr(&x.Elems[i])
		}
	}
}

func (r *rewriter) rewriteVariantPattern(armModule *string, armName *string, pos ast.Position) {
	if *armModule != "" {
		if indexByte(*armModule, '.') >= 0 {
			*armModule = r.rewriteStructNameAt(*armModule, pos)
			return
		}
		if r.ownEnums[*armModule] {
			*armModule = r.selfPrefix + *armModule
			return
		}
		mod, prefix, ok := r.importedModule(*armModule)
		if !ok {
			r.refuse(pos, "unknown module %q in variant pattern", *armModule)
			return
		}
		*armModule = mod.path
		if _, isVariant := mod.publicVariants[*armName]; isVariant && !mod.publicStructs[*armName] {
			return
		}
		if en, private := mod.allVariants[*armName]; private && !mod.publicStructs[*armName] {
			r.refuse(pos, "%s.%s is a variant of enum %s, which is not exported (declare it as `pub enum %s …` to make it accessible from other modules)", mod.name, *armName, en, en)
			return
		}
		*armName = prefix + *armName
		return
	}
	if r.ownStructs[*armName] {
		*armName = r.selfPrefix + *armName
	}
}

func (r *rewriter) rewritePatElems(payloads []*ast.TuplePatElem, elems []ast.TuplePatElem, pos ast.Position) {
	for _, el := range payloads {
		if el != nil {
			r.rewritePatElem(el, pos)
		}
	}
	for i := range elems {
		r.rewritePatElem(&elems[i], pos)
	}
}

func (r *rewriter) rewritePatElem(el *ast.TuplePatElem, pos ast.Position) {
	if el.VariantName != "" {
		r.rewriteVariantPattern(&el.VariantModule, &el.VariantName, pos)
	}
	r.rewritePatElems(el.VariantPayloads, el.Nested, pos)
}

func (r *rewriter) qualifiedVariant(mod *module, prefix, name string, pos ast.Position) (string, bool) {
	if mod.publicPlainFuncs[name] || mod.publicConsts[name] {
		return "", false
	}
	if en, ok := mod.publicVariants[name]; ok {
		if en == "" {
			r.refuse(pos, "%s.%s is a variant of more than one exported enum in module %q; match on a value of the enum you mean", mod.name, name, mod.name)
			return "", true
		}
		return prefix + en, true
	}
	if en, ok := mod.allVariants[name]; ok && !mod.allDecls[name] {
		r.refuse(pos, "%s.%s is a variant of enum %s, which is not exported (declare it as `pub enum %s …` to make it accessible from other modules)", mod.name, name, en, en)
		return "", true
	}
	return "", false
}

func (r *rewriter) rewriteDestructureStructNames(d *ast.Destructure) {
	if d.StructName != "" {
		d.StructName = r.rewriteStructNameAt(d.StructName, d.P)
	}
	for _, sub := range d.Nested {
		if sub != nil {
			r.rewriteDestructureStructNames(sub)
		}
	}
}

func (r *rewriter) rewriteStructName(name string) string {
	return r.rewriteStructNameAt(name, ast.Position{})
}

func (r *rewriter) rewriteStructNameAt(name string, pos ast.Position) string {
	if r.typeParams[name] {
		return name
	}
	if dot := indexByte(name, '.'); dot >= 0 {
		modName, structName := name[:dot], name[dot+1:]
		if mod, prefix, ok := r.importedModule(modName); ok {
			r.checkPublicStruct(mod, structName, pos)
			if rx, ok := mod.reexportTypes[structName]; ok {
				return rx
			}
			return prefix + structName
		}
		return name
	}
	if r.ownStructs[name] || r.ownEnums[name] || r.ownTraits[name] {
		return r.selfPrefix + name
	}
	return name
}

func (r *rewriter) rewriteTypeArgIndex(slot *ast.Expr) bool {
	switch x := (*slot).(type) {
	case *ast.Ident:
		if r.localVars[x.Name] || r.ownFuncs[x.Name] || r.ownConsts[x.Name] {
			return false
		}
		if r.ownStructs[x.Name] || r.ownEnums[x.Name] {
			x.Name = r.selfPrefix + x.Name
			return true
		}
	case *ast.FieldAccess:
		id, ok := x.Target.(*ast.Ident)
		if !ok || r.localVars[id.Name] || r.typeParams[id.Name] {
			return false
		}
		if mod, _, ok := r.importedModule(id.Name); ok && (mod.publicStructs[x.Field] || mod.publicEnums[x.Field]) {
			*slot = &ast.Ident{P: id.P, Name: r.rewriteStructNameAt(id.Name+"."+x.Field, x.P)}
			return true
		}
	}
	return false
}

func (r *rewriter) rewriteTraitNameAt(name string, pos ast.Position) string {
	if dot := indexByte(name, '.'); dot >= 0 {
		if mod, _, ok := r.importedModule(name[:dot]); ok {
			r.checkPublicStruct(mod, name[dot+1:], pos)
		}
	}
	return r.resolveTraitName(name)
}

func (r *rewriter) resolveTraitName(name string) string {
	if dot := indexByte(name, '.'); dot >= 0 {
		modName, traitName := name[:dot], name[dot+1:]
		if mod, prefix, ok := r.importedModule(modName); ok {
			if rx, ok := mod.reexportTypes[traitName]; ok {
				return rx
			}
			return prefix + traitName
		}
		return name
	}
	if r.ownTraits[name] {
		return r.selfPrefix + name
	}
	return name
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// bindTypeParams restores the outer type namespace after a declaration.
func (r *rewriter) bindTypeParams(names []string) func() {
	if len(names) == 0 {
		return func() {}
	}
	previous := r.typeParams
	bound := make(map[string]bool, len(previous)+len(names))
	for name := range previous {
		bound[name] = true
	}
	for _, name := range names {
		bound[name] = true
	}
	r.typeParams = bound
	return func() { r.typeParams = previous }
}

func (r *rewriter) rewriteType(slot *ast.Type) {
	if slot == nil || *slot == nil {
		return
	}
	switch t := (*slot).(type) {
	case ast.StructType:
		if r.typeParams[t.Name] && len(t.Args) == 0 {
			*slot = ast.ParamType{Name: t.Name}
			return
		}
		newName := r.rewriteStructName(t.Name)
		args := r.rewriteTypeArgs(t.Args)
		if newName != t.Name || args != nil {
			if args == nil {
				args = t.Args
			}
			*slot = ast.StructType{Name: newName, Args: args}
		}
	case ast.EnumType:
		if r.typeParams[t.Name] && len(t.Args) == 0 {
			*slot = ast.ParamType{Name: t.Name}
			return
		}
		newName := r.rewriteStructName(t.Name)
		args := r.rewriteTypeArgs(t.Args)
		if newName != t.Name || args != nil {
			if args == nil {
				args = t.Args
			}
			*slot = ast.EnumType{Name: newName, Args: args}
		}
	case ast.ArrayType:
		elem := t.Elem
		r.rewriteType(&elem)
		*slot = ast.ArrayType{Elem: elem}
	case ast.SliceType:
		elem := t.Elem
		r.rewriteType(&elem)
		*slot = ast.SliceType{Elem: elem}
	case ast.TupleType:
		elems := make([]ast.Type, len(t.Elems))
		copy(elems, t.Elems)
		for i := range elems {
			r.rewriteType(&elems[i])
		}
		*slot = ast.TupleType{Elems: elems}
	case *ast.FuncType:
		for i := range t.Params {
			r.rewriteType(&t.Params[i])
		}
		r.rewriteType(&t.Result)
	case ast.DynTraitType:
		changed := false
		newTraits := make([]string, len(t.Traits))
		for i, tr := range t.Traits {
			nt := r.rewriteTraitNameAt(tr, ast.Position{})
			newTraits[i] = nt
			if nt != tr {
				changed = true
			}
		}
		var newArgs [][]ast.Type
		if len(t.Args) > 0 {
			newArgs = make([][]ast.Type, len(t.Args))
			for i, args := range t.Args {
				if len(args) == 0 {
					continue
				}
				na := make([]ast.Type, len(args))
				for j := range args {
					na[j] = args[j]
					r.rewriteType(&na[j])
				}
				newArgs[i] = na
			}
			changed = true
		}
		var newAssoc [][]ast.AssocBinding
		if len(t.AssocBindings) > 0 {
			newAssoc = make([][]ast.AssocBinding, len(t.AssocBindings))
			for i, binds := range t.AssocBindings {
				if len(binds) == 0 {
					continue
				}
				nb := make([]ast.AssocBinding, len(binds))
				for j := range binds {
					nb[j] = binds[j]
					r.rewriteType(&nb[j].Type)
				}
				newAssoc[i] = nb
			}
			changed = true
		}
		if changed {
			*slot = ast.NewDynTraitTypeFull(newTraits, newArgs, newAssoc)
		}
	}
}

func (r *rewriter) rewriteTypeArgs(args []ast.Type) []ast.Type {
	if len(args) == 0 {
		return nil
	}
	out := make([]ast.Type, len(args))
	copy(out, args)
	for i := range out {
		r.rewriteType(&out[i])
	}
	return out
}

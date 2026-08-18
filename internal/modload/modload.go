// Package modload loads a multi-file lang program: parse the entry
// file, recursively pull in everything it imports (relative to the
// importing file's directory), detect cycles, and stitch the
// modules together into a single ast.Program for the rest of the
// pipeline (checker / IR lowering / codegen).
//
// Module identity is path-derived: the local name a qualified call
// uses (`mod.fn(args)`) comes from the import path's basename
// without the `.fern` extension. `import "./math/vec";` binds the
// remote module as `vec` in the importing file.
//
// Names from non-entry modules are mangled to `<mod>__<name>` so
// they can coexist with the entry module's names (and with names
// from other imported modules) in the single combined program.
// Internal references inside the non-entry module — direct calls
// to its own functions, function values referencing them — get
// rewritten to the mangled form during loading. Cross-module
// references in any module — `mod.fn(args)`, `mod.fn` as a value —
// get rewritten to flat references at the mangled name.
//
// Visibility: top-level decls are private to their declaring
// module by default. Prefixing with `pub` (`pub function …` /
// `pub struct …` / `pub const …`) marks a decl as exported.
// Cross-module references to non-`pub` decls are rejected during
// loading with a diagnostic that names the offending qualified
// reference and suggests the fix (with the right keyword for the
// referenced decl kind).
//
// Aliasing is supported: `import "std/test" as t;` binds the
// qualifier to `t` (Import.LocalName carries the alias), so `t.foo()`
// resolves through the same per-module import table as a basename
// qualifier. Without an alias the local name comes from the path
// basename.

package modload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/literate"
	"github.com/jakechampion/lang/internal/manifest"
	"github.com/jakechampion/lang/internal/mvs"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/pkgcache"
	"github.com/jakechampion/lang/internal/stdlib"
)

// Load parses entryPath, recursively loads every module it imports,
// mangles non-entry modules' top-level decls, rewrites cross-module
// references, and returns the combined Program plus a per-file
// source map (keyed by canonical absolute path) so diagnostic
// messages can locate errors back to the right file.
//
// The returned Program is what the checker / IR lowering / codegen
// expect: a single flat list of FuncDecls and StructDecls with
// every name globally unique.
func Load(entryPath string) (*ast.Program, map[string]string, error) {
	return loadCore(entryPath, nil)
}

// LoadWith is like Load but consults `overrides` before reading from
// disk. Keys are absolute paths (matching what filepath.Abs would
// produce on the current OS); values are the source text to use for
// that path. Used by the LSP to load multi-file programs against the
// editor's in-memory document buffer instead of stale on-disk
// content. Empty overrides == Load.
func LoadWith(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, error) {
	prog, srcs, _, err := loadCoreLit(entryPath, overrides)
	return prog, srcs, err
}

// LiterateModule describes a module whose source was tangled from a
// `.fern.md` literate document during loading (a `.fern` import that
// resolved to a `<<*>>`-rooted document). It carries what a caller
// needs to map diagnostics in the generated source back to the lines
// the author wrote: the document path + source, and the tangle line
// map (generated line → document line). Keyed in the LoadWithLiterate
// result by the module's canonical (`.fern`) path.
type LiterateModule struct {
	DocPath string
	DocSrc  string
	LineMap []literate.Line
}

// LoadWithLiterate is LoadWith plus a third result: the literate
// modules tangled while loading (empty when no import resolved to a
// `.fern.md`). The CLI uses it to remap diagnostics in an imported
// literate library back onto its document. A `.fern` import resolves to
// a single-root `.fern.md` when no plain `.fern` of that name exists;
// importing a multi-file (`file=`) document is an error (it has no
// single importable module).
func LoadWithLiterate(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, map[string]*LiterateModule, error) {
	return loadCoreLit(entryPath, overrides)
}

// LoadSource loads a program whose entry source is held in memory
// rather than on disk — the shape every in-memory compile path needs
// now that the auto-prelude is gone and `std/…` / `core/…` imports
// must be resolved through modload (stdin / REPL / playground / the
// wasm bundle). The synthetic entry path is absolute so filepath.Abs
// (here and in loadCore) short-circuits without calling os.Getwd —
// which is unimplemented under GOOS=js, where the browser playground
// and cmd/fern-wasm run. In-memory callers only import stdlib (served
// from the embedded FS), so the synthetic directory is never read.
func LoadSource(src string) (*ast.Program, map[string]string, error) {
	const entry = "/__fern_source__/main.fern"
	return loadCore(entry, map[string]string{entry: src})
}

func loadCore(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, error) {
	prog, srcs, _, err := loadCoreLit(entryPath, overrides)
	return prog, srcs, err
}

func loadCoreLit(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, map[string]*LiterateModule, error) {
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, nil, nil, err
	}
	loaded := map[string]*module{}          // path → loaded module
	stack := map[string]bool{}              // path → true while in flight (cycle detection)
	srcs := map[string]string{}             // path → source text (for diag formatting)
	lit := map[string]*LiterateModule{}     // path → literate provenance (tangled imports)
	mans := map[string]*manifest.Manifest{} // dir → governing manifest (nil = none)
	if err := loadRecursive(entryAbs, loaded, stack, srcs, overrides, lit, mans); err != nil {
		// Return the partial source map (loadRecursive stamps srcs[path]
		// BEFORE parsing each file, so the failing file's source is already
		// captured) so the CLI/LSP formatter can render the offending line +
		// caret. Discarding it here left every parse error rendering a blank
		// source line while checker errors — reached only after a clean parse,
		// with srcs intact — rendered correctly.
		return nil, srcs, lit, err
	}
	resolveCyclicImports(loaded)
	prog, err := combine(loaded, entryAbs)
	if err != nil {
		return nil, srcs, lit, err
	}
	if prog.CapGrants, err = capGrants(mans); err != nil {
		return nil, srcs, lit, err
	}
	return prog, srcs, lit, nil
}

// LoadStdlibFlat loads each stdlib path in `paths` (and every
// stdlib module it transitively imports) and returns a single
// combined Program whose decls keep their bare names — NO
// `<mod>__` mangling. Qualified call sites inside the loaded
// stdlib bodies (`int.foo()`, `string.find(s, ...)`) rewrite to
// flat bare calls (`foo()`, `find(s, ...)`) so the call resolves
// against the bare-named decls in the same combined Program.
//
// Used by `LoadStdlibFlat`: stdlib
// modules can now use qualified imports for cross-module calls
// without each importer module having to be loaded through the
// normal mangling path. Safe because every free-function /
// struct / const name in `internal/stdlib/` is globally unique
// (receiver methods don't collide because dispatch is by
// receiver type, not by mangled name).
//
// Only stdlib paths are accepted (`std/…` / `core/…`); a
// non-stdlib path returns an error. The combined Program's
// `LoadedStdlibPaths` is populated for the checker's dedup
// against any same-paths loaded via the entry program's
// `import` statements.
func LoadStdlibFlat(paths []string) (*ast.Program, error) {
	return LoadStdlibFlatSkipping(paths, nil)
}

// LoadStdlibFlatSkipping is LoadStdlibFlat with a `skipPaths` set
// of canonical `stdlib://…` paths that should not contribute decls
// to the combined Program. Transitive imports through stdlib still
// load + rewrite (so receiver-method visibility / publicFuncs
// metadata stays consistent with the full graph), but the
// combine step skips the modules whose path is in skipPaths.
//
// Used by `LoadStdlibFlat`: the entry
// program may have already loaded some stdlib modules through the
// regular `modload.Load` mangling path, and re-loading them
// flat-namespace here would surface duplicate decls — receiver
// method `__method_<Type>_<Name>` names land bare under both
// modes and the checker's redeclaration gate fires. skipPaths
// lets the caller exclude those modules from the flat-load
// contribution.
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
	for _, mod := range loaded {
		errs := mod.rewriteAllOpts("", true, skipPaths)
		for _, e := range errs {
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	// Flat-load semantics: stdlib decls loaded flat are
	// universally visible to every other module.
	// `methodVisibleHere` reads that off an empty `SourceModule` on
	// the FuncDecl, so we clear the stamp `loadRecursive` set.
	// Without this, a stdlib body that calls into another stdlib
	// module via receiver-method dispatch (e.g. std/json calling
	// `s.parse_int_radix(16)` whose hoisted method lives in
	// std/i32) would fail the visibility check and the checker
	// would re-interpret the call as a plain field-access — the
	// "field access on non-struct value of type string" cascade.
	//
	// Cross-stdlib free-function calls (rewritten from `int.foo()`
	// to bare `foo()` by the flat-namespace rewriter above) don't
	// participate in the method visibility check, so they're fine
	// either way.
	// Sort by path before merging — Go map iteration order is
	// randomized, and the combined Program's slice order must be
	// deterministic so downstream stages emit byte-identical output
	// across runs (see TestLoadDeterministic).
	pathsFlat := make([]string, 0, len(loaded))
	for p := range loaded {
		pathsFlat = append(pathsFlat, p)
	}
	sort.Strings(pathsFlat)
	for _, p := range pathsFlat {
		mod := loaded[p]
		if skipPaths[mod.path] {
			continue
		}
		for _, fn := range mod.prog.Funcs {
			fn.SourceModule = ""
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

// module bundles a parsed file with its canonical path and the
// derived module name (basename without `.fern`).
type module struct {
	path string
	name string
	// manglePrefix is the `<prefix>__`-style string prepended to this
	// module's non-entry decls (and used to rewrite cross-module
	// references to them). Defaults to `name + "__"` — the historical
	// basename scheme — but combine() resets the entry module's to ""
	// and disambiguates non-stdlib modules whose basenames collide
	// (e.g. a/util.fern + b/util.fern) so two distinct modules don't
	// both mangle to `util__X`. See docs/ADVERSARIAL-REVIEW-2026-06.md (M2).
	manglePrefix string
	prog         *ast.Program
	imports      map[string]*module // local-name → loaded module
	// importPaths mirrors imports keyed by the canonical child path
	// rather than the loaded module pointer. Used to patch a nil
	// imports[localName] entry once every cyclically-loaded module
	// is in the global `loaded` map (see resolveCyclicImports).
	importPaths map[string]string
	// publicFuncs / publicStructs / publicConsts / publicEnums hold
	// the original (pre-mangle) names of `pub` decls, populated when
	// the module loads. The rewriter uses them to gate cross-module
	// references. publicEnums also covers `pub type Tok = A | B;`
	// unions because the parser desugars unions to enums.
	publicFuncs map[string]bool
	// publicMethods is the subset of publicFuncs declared with a receiver.
	// They live in publicFuncs because method dispatch resolves visibility
	// through it, but they cannot be CALLED module-qualified — so the
	// did-you-mean path has to tell them apart from plain functions or it
	// suggests a name that fails differently. See reportUndeclared.
	publicMethods map[string]bool
	// publicPlainFuncs is the receiverless subset. A module may declare both
	// a method and a plain function under one name; only the latter makes
	// `mod.name(…)` a real call.
	publicPlainFuncs map[string]bool
	publicStructs    map[string]bool
	publicConsts     map[string]bool
	publicEnums      map[string]bool
	// allConsts is the pre-mangle name set of every const in this
	// module (public or private). The visibility-error path uses it
	// to decide whether `mod.X` should suggest `pub function X`
	// (default) or `pub const X` (when X is a known private const).
	allConsts map[string]bool
	// allDecls is the pre-mangle name set of EVERY top-level decl in
	// this module, exported or not. The visibility checks below need it
	// to tell "declared, but private" from "no such name": without it
	// both report `mod.X is not exported`, which sends a reader off to
	// add `pub` to a declaration that does not exist. For a TYPE that
	// message is also the only error produced — nothing downstream
	// reports the unknown name — so a typo in a qualified type is
	// indistinguishable from a visibility mistake.
	allDecls map[string]bool
	// reexports maps a `pub use`-re-exported name to the flat mangled
	// name it ultimately resolves to (e.g. "split" → "helpers__split").
	// A consumer's `thismod.split` rewrites to that mangled name rather
	// than `thismod__split`. Filled by resolveReexports after mangle
	// prefixes are assigned. See docs/PRELUDE-TO-MODULES.md.
	reexports map[string]string
	// reexportTypes is the type/trait counterpart of reexports: a
	// `pub use`-re-exported struct / enum / trait name → the original
	// module's mangled type name. Consumed by the type-name rewriters
	// (rewriteStructNameAt / rewriteTraitNameAt) so a consumer's
	// `facade.SomeType` resolves to `orig__SomeType`, not `facade__SomeType`.
	// See docs/PRELUDE-TO-MODULES.md.
	reexportTypes map[string]string
	// pubUses records this module's `pub use` directives (resolved
	// target path + names) so resolveReexports can build the reexports
	// table once every module + its prefix is known.
	pubUses []pubUseEntry
	// packageScoped holds the pre-mangle names of this module's
	// `pub(package)` decls (across kinds). A cross-module reference to
	// one is allowed only from a module in the SAME package (same
	// directory; the stdlib is one package) — see samePackage. Distinct
	// from publicFuncs/etc. so cross-package refs still fail. See
	// docs/PUB-PACKAGE.md.
	packageScoped map[string]bool
}

// samePackage reports whether two module paths belong to the same
// package for `pub(package)` visibility: the same directory, or both
// inside the embedded stdlib (which is treated as a single package).
func samePackage(a, b string) bool {
	aStd := strings.HasPrefix(a, stdlibPrefix)
	bStd := strings.HasPrefix(b, stdlibPrefix)
	if aStd || bStd {
		return aStd && bStd
	}
	return filepath.Dir(a) == filepath.Dir(b)
}

// pubUseEntry is one resolved `pub use "path".{names…};` directive.
type pubUseEntry struct {
	childPath string
	names     []string
	pos       ast.Position
}

// loadRecursive parses path (if not already loaded), then recurses
// into every import. Cycle detection uses the in-flight `stack`:
// if we're asked to load a path we're already loading, that's a
// cycle. Disk-path cycles error; stdlib-to-stdlib cycles are
// allowed (the stdlib's method graph has natural cycles —
// std/string's bodies dispatch (i32) byte methods from std/i32;
// std/i32's bodies dispatch (string) methods from std/string —
// and modload's role here is to surface every needed source file,
// not to enforce a strict DAG). When a stdlib cycle is detected
// we just return without recursing; the back-edge's `imports`
// pointer is patched up in the second pass below.
func loadRecursive(path string, loaded map[string]*module, stack map[string]bool, srcs map[string]string, overrides map[string]string, lit map[string]*LiterateModule, mans map[string]*manifest.Manifest) error {
	if _, done := loaded[path]; done {
		return nil
	}
	if stack[path] {
		if strings.HasPrefix(path, stdlibPrefix) {
			return nil
		}
		return fmt.Errorf("import cycle detected including %s", path)
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
		// Stamp the path on each structured error so callers
		// (LSP workspace mode, CLI formatter) can attribute it
		// back to this file. Returned unwrapped so errors.As
		// works for diag.Errors / diag.Filed downstream.
		return diag.WithFile(err, path)
	}

	// Recurse into imports first. We don't add this module to
	// `loaded` until after recursion succeeds — that way a back-
	// edge from a child reaches stack[path]==true and the cycle
	// check fires, rather than seeing an already-loaded entry and
	// silently completing.
	//
	// Stdlib modules don't have a real filesystem directory; their
	// relative imports resolve from the embedded tree's root, so we
	// pass an empty importing-dir and let resolveImportPath route
	// any `std/…` / `core/…` references through the stdlib package.
	// A relative import like `./helper` from within a stdlib module
	// isn't supported in this first cut — they'd land outside the
	// embedded FS — but the existing stdlib modules don't need it.
	dir := ""
	if !strings.HasPrefix(path, stdlibPrefix) {
		dir = filepath.Dir(path)
	}
	childPaths := make([]string, len(prog.Imports))
	for i, imp := range prog.Imports {
		childPaths[i], err = resolveImport(dir, imp.Path, mans, overrides)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := loadRecursive(childPaths[i], loaded, stack, srcs, overrides, lit, mans); err != nil {
			return err
		}
	}
	// `pub use "path".{…};` re-export targets are loaded like imports so
	// the target module's decls are in the combined program; the actual
	// re-export table is built later (resolveReexports), once mangle
	// prefixes are known.
	pubUsePaths := make([]string, len(prog.PubUses))
	for i, pu := range prog.PubUses {
		pubUsePaths[i], err = resolveImport(dir, pu.Path, mans, overrides)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := loadRecursive(pubUsePaths[i], loaded, stack, srcs, overrides, lit, mans); err != nil {
			return err
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
		allConsts:        map[string]bool{},
		allDecls:         map[string]bool{},
		reexports:        map[string]string{},
		reexportTypes:    map[string]string{},
		packageScoped:    map[string]bool{},
	}
	for _, fn := range prog.Funcs {
		// Stamp every FuncDecl with the path of the module that
		// declared it. The checker reads this during method
		// dispatch to scope visibility — a method declared in
		// module A is only callable from a file whose import
		// closure reaches A.
		fn.SourceModule = path
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
		// Same source-module stamping as FuncDecl. The LSP answers
		// cross-module goto-def on type names with it, and the checker
		// scopes nominal type names by it — every module's decls land
		// in one merged table, so `struct V` here must not make another
		// module's `V` type parameter concrete (#6118).
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
		ed.SourceModule = path
		if ed.Public {
			mod.publicEnums[ed.Name] = true
		}
		mod.allDecls[ed.Name] = true
		if ed.PackageScoped {
			mod.packageScoped[ed.Name] = true
		}
	}
	for _, ud := range prog.Unions {
		ud.SourceModule = path
		if ud.Public {
			// `pub type Tok = A | B;` desugars to an enum later in the
			// checker; the publicEnums map carries the export bit
			// across that pass.
			mod.publicEnums[ud.Name] = true
		}
		mod.allDecls[ud.Name] = true
		if ud.PackageScoped {
			mod.packageScoped[ud.Name] = true
		}
	}
	for _, cd := range prog.Consts {
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
		// Stamp the declaring module so the checker's trait
		// coherence (orphan-rule) check can tell a local trait from
		// an imported one. See docs/TRAITS.md.
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
		// Disallow two imports with the same local name in the
		// same module — `mod.fn` would be ambiguous and the
		// rewriter would pick one arbitrarily.
		if existing, dup := mod.imports[imp.LocalName]; dup && existing != child {
			return fmt.Errorf("%s: import name %q bound twice (paths %s and %s)",
				path, imp.LocalName, existing.path, child.path)
		}
		// child may be nil if the recursive load short-circuited
		// on a stdlib cycle above — the back-edge's parent isn't
		// in `loaded` yet. `importPaths` records the canonical
		// path under each local name so the caller of
		// loadRecursive (`Load` / `LoadStdlibFlat`) can patch the
		// pointer in a second pass once every module is loaded.
		mod.imports[imp.LocalName] = child
		mod.importPaths[imp.LocalName] = childPaths[i]
	}
	for i, pu := range prog.PubUses {
		mod.pubUses = append(mod.pubUses, pubUseEntry{childPath: pubUsePaths[i], names: pu.Names, pos: pu.P})
	}
	loaded[path] = mod
	return nil
}

// resolveCyclicImports walks every loaded module and fills in any
// `imports[localName]` entry that's nil because the corresponding
// child wasn't yet in `loaded` at the time loadRecursive set up the
// parent's imports map (stdlib cycle back-edge). Idempotent; safe
// to call when there are no cycles.
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

// resolveImportPath turns an `import "./util"` style path into a
// canonical key the loader uses to identify a module. For disk
// imports the key is the absolute filesystem path; for stdlib
// imports (paths prefixed with `std/` or `core/`) the key is a
// `stdlib://…` URI so the loader knows to fetch source from the
// embedded FS instead of the disk. We auto-append `.fern` if the
// import path doesn't already include the extension, so users
// can write either form.
func resolveImportPath(importingDir, importPath string) string {
	if stdlib.IsStdlibPath(importPath) {
		key := importPath
		if !strings.HasSuffix(key, ".fern") {
			key += ".fern"
		}
		return stdlibPrefix + key
	}
	resolved := filepath.Join(importingDir, importPath)
	if filepath.Ext(resolved) == "" {
		resolved += ".fern"
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		// Best-effort; if Abs fails the load step will surface
		// the underlying file error.
		return resolved
	}
	return abs
}

// resolveImport is resolveImportPath plus the fern.toml dependency
// namespace (docs/PACKAGE-MANAGEMENT-SOTA.md — manifest slice). The
// added rule: when the importing file is governed by a manifest (the
// nearest fern.toml up the directory tree) and a bare import's first
// segment names a DECLARED dependency, the import resolves into that
// dependency's directory — `import "helper"` to its lib module
// (manifest `lib`, default lib.fern), `import "helper/sub"` to
// `<dep-dir>/sub.fern`. Everything else — stdlib paths, `./`/`../`
// relatives, manifest-less programs, and bare paths that resolve to an
// existing sibling file — keeps today's behaviour, so the manifest is
// strictly opt-in. A bare import that matches neither a declared
// dependency nor an existing file errors here, naming the manifest:
// the resolver, not the cache layout, is what enforces that a package
// only reaches its declared dependencies.
//
// mans caches per-directory manifest lookups for the whole load (nil
// value = "no manifest governs this directory"); overrides is the
// LoadWith in-memory file set, consulted so existence checks agree
// with what readModuleSource will actually see.
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
	// Not a declared dependency: a bare path that resolves to an existing
	// module keeps working (pre-manifest programs use `import "sub/mod"`
	// for subdirectories). Only a path that resolves to NOTHING becomes a
	// manifest error, so adding a fern.toml can't break a loading program.
	p := resolveImportPath(importingDir, importPath)
	if moduleExists(p, overrides) {
		return p, nil
	}
	return "", fmt.Errorf("import %q: not found relative to %s, and %q is not a declared dependency in %s (add it under [dependencies], e.g. %s = { path = \"../%s\" })",
		importPath, importingDir, seg, filepath.Join(man.Dir, manifest.FileName), seg, seg)
}

// declaredDepDir resolves the DECLARED dependency `seg` of `man` to its
// package directory, one branch per dependency form. Shared by
// resolveImport (the import path) and capGrants (the capability-grant
// table), so the two can never disagree about which directory a
// dependency entry governs.
func declaredDepDir(man *manifest.Manifest, seg string, mans map[string]*manifest.Manifest) (string, error) {
	d := man.Deps[seg]
	if d.Workspace {
		// Workspace-member dependency (Rec §5): resolve `seg` to the
		// enclosing workspace's member whose package name is `seg`,
		// instead of a path/url. Keeps cross-member deps explicit
		// (isolation — still a declared dep) while dropping brittle
		// `../../member` paths.
		return workspaceMemberDir(man.Dir, seg, mans)
	}
	// Vendored mode (Rec §6): when a `vendor/` tree governs this package,
	// a declared dependency resolves to `<vendor-root>/vendor/<name>/` —
	// flat, offline, path/url origins irrelevant, no network. Detected
	// from man.Dir alone (no load-wide threading): the vendor root is the
	// directory whose `vendor/` subdir either IS man's dir or contains it.
	// Isolation is still enforced — only DECLARED deps resolve — so a
	// vendored package can't reach a sibling in vendor it didn't declare.
	// A declared dep missing from a vendor tree is an error (the vendor
	// dir is stale — re-run `fern -vendor`), never a fallback to the
	// network.
	vr := vendorRootFor(man.Dir)
	if vr == "" {
		// A workspace member's own directory has no `vendor/`, but the
		// workspace root may (populated by `fern -vendor <root>`). Fall
		// back to the enclosing workspace root's vendor tree so members
		// share one vendored dependency set.
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
		// Versioned (MVS) dependency: the concrete version was chosen by
		// `fern -resolve` and pinned in fern.lock. Resolve through the lock
		// — the compiler reads the lock, never the index or the network.
		return lockedDepDir(man.Dir, seg)
	}
	if d.URL != "" {
		// Hash-addressed dependency: resolve through the content-addressed
		// store, NEVER the network — `fern -fetch` is the only fetcher (the
		// no-build-time-network constraint). Absent from the store is a
		// user-actionable error, not a download.
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

// capGrants derives the per-package capability-grant table
// (docs/PACKAGE-CAPABILITIES-BRIEF.md phase 2) from every manifest the
// load consulted: each dependency entry whose `capabilities` key is
// present contributes its grant to the entry's resolved directory —
// unioned when several manifests grant the same package. Unused or
// unresolvable declared deps are skipped silently (a dep that never
// loaded contributes no code to enforce against). Returns nil when no
// manifest grants anything, so grant-free programs stay allocation-free.
//
// It also enforces the ATTENUATION rule (brief phase 3): a governed
// manifest — one whose own package dir received a capability grant
// from some parent — may grant each of its dependencies at most the
// union of capabilities it holds itself. Each granting edge is checked
// against ITS grantor's holdings independently (a sibling's legitimate
// grant of the same capability never excuses an amplifying edge), and
// any violation is a load error. An ungoverned grantor — no
// `capabilities` key anywhere declares it, which includes the root —
// imposes no ceiling. Violations are reported all at once, sorted by
// granting manifest dir, then dependency name, then capability.
func capGrants(mans map[string]*manifest.Manifest) (map[string][]string, error) {
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
			merged := map[string]bool{}
			for _, c := range out[dir] {
				merged[c] = true
			}
			for _, c := range d.Capabilities {
				merged[c] = true
			}
			union := make([]string, 0, len(merged))
			for c := range merged {
				union = append(union, c)
			}
			sort.Strings(union)
			out[dir] = union
		}
	}
	var viols []string
	for _, mdir := range dirs {
		man := byDir[mdir]
		held, governed := out[man.Dir]
		if !governed {
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

// resolveDepImport resolves an import INTO a dependency directory:
// a bare `import "<dep>"` (rest == "") reaches the dependency's lib
// module — named by ITS OWN manifest's `lib` (an ancestor manifest
// doesn't speak for it), default lib.fern — and `import "<dep>/sub"`
// reaches `<dep-dir>/sub.fern`.
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

// LockedDepDir is lockedDepDir for callers outside the loader — `fern
// -vendor`, which must reach a versioned dependency's source the same way
// a build does, and report the same diagnostic when it cannot.
func LockedDepDir(manDir, name string) (string, error) { return lockedDepDir(manDir, name) }

// lockedDepDir resolves a versioned (MVS) dependency `name` through the
// package's fern.lock: the lock pins an exact version and its source (a
// local dir, or a url whose archive lives in the content-addressed
// store). Missing lock → run `fern -resolve`; a locked url version
// absent from the store → run `fern -fetch`/`-resolve`. The loader never
// reads the index or the network.
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

// workspaceMemberDir finds the directory of the workspace member named
// `name` (by its package name), for a `{ workspace = true }` dependency
// declared in the manifest at manDir. It locates the enclosing
// [workspace] root, then scans its members for one whose own manifest's
// package name matches. An unresolvable name is an error naming the
// workspace root (the members list is stale or the name is wrong).
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

// vendorRootFor returns the vendor root governing a package whose
// manifest lives at manDir, or "" when the package is not vendored.
// Two shapes count as vendored, both confirmed by an actual `vendor/`
// directory (so a project that merely has a path component named
// "vendor" isn't misread):
//   - manDir itself has a `vendor/` subdir → manDir is the vendor root
//     (the top-level project after `fern -vendor`);
//   - manDir sits under `<root>/vendor/<pkg>[/…]` → <root> is the vendor
//     root (a vendored dependency resolving ITS deps flat in the same
//     tree).
func vendorRootFor(manDir string) string {
	if st, err := os.Stat(filepath.Join(manDir, "vendor")); err == nil && st.IsDir() {
		return manDir
	}
	// Find the last `vendor` path component; the directory before it is
	// the root. filepath.Dir peels one segment at a time so this handles
	// nested subdirectories inside a vendored package too.
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

// manifestFor returns the manifest governing dir (nearest fern.toml at
// or above it), caching both hits and misses in mans.
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

// moduleExists reports whether readModuleSource would find source for
// a resolved disk path: an in-memory override, the file itself, or its
// literate `.fern.md` sibling.
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

// stdlibPrefix tags a path as referring to the embedded stdlib
// rather than the local filesystem. The prefix is chosen to be
// distinct from any absolute filesystem path filepath.Abs would
// produce (those start with `/` on Unix or a drive letter on
// Windows), so the `loaded` map keys can't collide.
const stdlibPrefix = "stdlib://"

// readSource reads the source text for a module path. Disk paths
// go through os.ReadFile; stdlib paths (`stdlib://…`) come from
// the embedded FS in the stdlib package. A missing stdlib module
// surfaces a clear "unknown stdlib module" error rather than
// falling back to disk.
func readSource(path string, overrides map[string]string) (string, error) {
	if strings.HasPrefix(path, stdlibPrefix) {
		importPath := strings.TrimPrefix(path, stdlibPrefix)
		src, ok := stdlib.Resolve(importPath)
		if !ok {
			return "", fmt.Errorf("unknown stdlib module %q", importPath)
		}
		return src, nil
	}
	// In-memory override (set by LoadWith) takes precedence over
	// disk — the editor's buffer is the source of truth while a
	// file is open, even when it hasn't been saved yet. The
	// override map is threaded through loadRecursive rather than
	// stashed on a package-level var so concurrent `Load` calls
	// (e.g. parallel fernsmith differential subtests) don't race.
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

// readModuleSource reads a module's source like readSource, but when a
// disk `.fern` target doesn't exist it falls back to a sibling `.fern.md`
// literate document: the document is tangled and its generated source
// becomes the module, with the tangle provenance returned for diagnostic
// remapping. A plain `.fern` always wins over a `.fern.md` of the same
// name; importing a multi-file (`file=`) document is an error.
func readModuleSource(path string, overrides map[string]string) (string, *LiterateModule, error) {
	src, err := readSource(path, overrides)
	if err == nil {
		return src, nil, nil
	}
	// Only a missing local `.fern` triggers the literate fallback —
	// stdlib paths and genuine read errors propagate unchanged.
	if strings.HasPrefix(path, stdlibPrefix) || !strings.HasSuffix(path, ".fern") || !os.IsNotExist(underlyingErr(err)) {
		return "", nil, err
	}
	docPath := path + ".md" // foo.fern → foo.fern.md
	docSrc, derr := readSource(docPath, overrides)
	if derr != nil {
		// No literate sibling either: report the original `.fern` error.
		return "", nil, err
	}
	doc := literate.Parse(docSrc)
	if doc.HasFiles() {
		return "", nil, fmt.Errorf("cannot import multi-file literate document %s: it tangles to several modules, not one importable module", docPath)
	}
	code, lineMap, terr := doc.Tangle()
	if terr != nil {
		// Tangle errors carry document-coordinate positions already.
		return "", nil, fmt.Errorf("%s", diag.Format(docPath, docSrc, terr))
	}
	return code, &LiterateModule{DocPath: docPath, DocSrc: docSrc, LineMap: lineMap}, nil
}

// underlyingErr unwraps the fmt.Errorf("read %s: %w") that readSource
// returns so os.IsNotExist can see the *PathError underneath.
func underlyingErr(err error) error {
	if u := errors.Unwrap(err); u != nil {
		return u
	}
	return err
}

// importClosures computes, for each loaded module, the set of
// module paths it transitively imports — including itself. The
// returned map is keyed by canonical module path; each value is
// a set whose membership answers "is module B in module A's
// import closure?" with a single map lookup.
//
// The checker uses this for module-scoped method dispatch: at a
// call site inside module A, a method declared in module B is
// callable only if `closures[A][B]` is true. Self-membership
// keeps the same lookup working when A == B (a module always
// sees its own methods).
//
// O(N²) worst case for N modules — fine for the small import
// graphs lang programs actually have. If that ever stops being
// true, a SCC-based fix-point would replace the per-module
// BFS without changing the contract.
func importClosures(loaded map[string]*module) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for path, mod := range loaded {
		closure := map[string]bool{path: true}
		stack := []*module{mod}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, child := range cur.imports {
				if closure[child.path] {
					continue
				}
				closure[child.path] = true
				stack = append(stack, child)
			}
		}
		out[path] = closure
	}
	return out
}

// directImports is importClosures without the transitive step: each
// module maps to the paths its own `import` declarations name, plus
// itself. Trait-method resolution needs the distinction — a trait
// reachable only through some other module's imports ranks below one
// the calling module imported or declared itself.
//
// Keyed off importPaths rather than the resolved `imports` pointers so
// a back-edge left nil by a stdlib import cycle still contributes its
// canonical path.
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

// importLocalName mirrors the parser-side helper but is duplicated
// here so the driver doesn't need to re-parse to compute it. Stdlib
// path keys (`stdlib://std/i32.fern`) get their basename extracted
// the same way as disk paths — the `stdlib://` prefix doesn't
// participate.
func importLocalName(path string) string {
	trimmed := strings.TrimPrefix(path, stdlibPrefix)
	base := filepath.Base(trimmed)
	if ext := filepath.Ext(base); ext == ".fern" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

// combine produces the final flat ast.Program. Entry-module decls
// keep their original names; non-entry module decls get prefixed
// with `<modname>__`. Internal references to non-entry-module
// decls (own-module calls / values) get rewritten with the
// matching prefix; cross-module references (`mod.fn`) get
// rewritten as direct calls to the mangled name.
//
// Cross-module references to non-`pub` decls are rejected here —
// the rewriter accumulates those errors and combine returns the
// first one (other errors are wrapped under it).
func combine(loaded map[string]*module, entryPath string) (*ast.Program, error) {
	combined := &ast.Program{
		ModuleImports:     importClosures(loaded),
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
	for _, mod := range loaded {
		errs := mod.rewriteAll(mod.manglePrefix)
		for _, e := range errs {
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	// Sort modules by path before merging so the combined Program's
	// function / struct / enum order doesn't depend on Go map
	// iteration order. Without this, two LoadAll calls on the same
	// project produce Programs whose Funcs slices differ in order,
	// which propagates non-determinism through every downstream
	// stage (IR, codegen) and breaks the byte-identical self-host
	// fixed-point gates and reproducible builds. Pinned by
	// TestLoadDeterministic.
	paths := make([]string, 0, len(loaded))
	for p := range loaded {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
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
		// TypeRefs is a parser-recorded side table the LSP uses
		// for hover / definition on type annotations. Merging
		// them here means cross-module type queries find the
		// right TypeRef for the entry module's source.
		combined.TypeRefs = append(combined.TypeRefs, mod.prog.TypeRefs...)
		// TodoSites is carried over from the ENTRY module only:
		// ast.Position has no filename, so `todo` positions from
		// imported modules could not be attributed to their file
		// in `-check`'s warning output.
		if p == entryPath {
			combined.TodoSites = append(combined.TodoSites, mod.prog.TodoSites...)
		}
	}
	return combined, nil
}

// assignManglePrefixes finalises each loaded module's manglePrefix. The
// entry module gets "" (its decls keep their original names). Every
// other module defaults to `name + "__"`, preserving the historical
// basename scheme — which stdlib dispatch and interp interop depend on
// (e.g. `int__int_to_string`). The one adjustment: when two NON-stdlib
// modules share a basename (a common `util.fern` / `types.fern`
// per-directory layout), the later one in sorted-path order gets a
// numeric disambiguator (`util_1__`, `util_2__`, …) so the two distinct
// modules don't both mangle to `util__X` and trip a spurious "redeclared"
// error. Processing in sorted-path order makes the assignment
// deterministic (the common root prefix doesn't affect relative order),
// so the combined Program — and the self-host byte-identical gate — stays
// reproducible. stdlib modules are never disambiguated (they keep the
// bare prefix); a colliding user module is what moves.
func assignManglePrefixes(loaded map[string]*module, entryPath string) {
	paths := make([]string, 0, len(loaded))
	for p := range loaded {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	used := map[string]bool{}
	// Reserve entry ("") and every stdlib / bare prefix first so a
	// colliding user module is the one that moves, not stdlib.
	for _, p := range paths {
		m := loaded[p]
		if m.path == entryPath {
			m.manglePrefix = ""
			continue
		}
		if strings.HasPrefix(m.path, stdlibPrefix) {
			m.manglePrefix = m.name + "__"
			used[m.manglePrefix] = true
		}
	}
	for _, p := range paths {
		m := loaded[p]
		if m.path == entryPath || strings.HasPrefix(m.path, stdlibPrefix) {
			continue
		}
		pref := m.name + "__"
		for k := 1; used[pref]; k++ {
			pref = fmt.Sprintf("%s_%d__", m.name, k)
		}
		m.manglePrefix = pref
		used[pref] = true
	}
}

// exportedMangled resolves a name in this module to the flat mangled
// name a consumer would call, if the name is part of this module's
// public surface. Re-exports are checked first (so a re-exported name
// resolves to its *original* module's mangled name, not this module's),
// then this module's own public funcs/consts (values) and structs/enums/
// traits (types). isType marks the latter.
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

// resolveReexports builds every module's `reexports` table from its
// `pub use` directives, now that mangle prefixes are assigned. A
// re-exported name is also added to the re-exporting module's public
// funcs so a consumer's visibility check passes. Resolution iterates to
// a fixpoint so a re-export of a name that the target itself re-exports
// resolves once the target's entry is filled. Values (function / const)
// land in the `reexports` table; types (struct / enum / trait) in
// `reexportTypes`.
func resolveReexports(loaded map[string]*module) error {
	paths := make([]string, 0, len(loaded))
	for p := range loaded {
		paths = append(paths, p)
	}
	sort.Strings(paths)

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
				// Type / trait re-export: record it in the type table and
				// add the name to the re-exporting module's public structs
				// (traits register there too) so a consumer's visibility
				// check passes for `facade.SomeType`.
				w.mod.reexportTypes[w.name] = mangled
				w.mod.publicStructs[w.name] = true
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

// isRuntimeHelperName reports whether a function name should be
// exempt from modload's `<mod>__` prefix mangling. The set covers:
//
//   - `__method_<Type>_<Name>` receiver-method hoist targets.
//     The checker's auto-discovery pass keys off the `__method_`
//     prefix to register `Type.<Name>` in the Methods map.
//   - `__map_*` / `__mapiter_*` Map runtime helpers. The codegen
//     translates `__method_Map_get` etc. to `__map_get_impl` via
//     a hardcoded `case` switch, so the target name has to live
//     at its bare form for the call to resolve.
//   - `map_new_impl` — the one Map runtime helper without the
//     `__` prefix. `map_new` is a checker builtin that codegen
//     rewrites to `map_new_impl`.
//
// Everything else gets the module prefix as usual. User-defined
// `__foo` names in entry modules are unaffected (entry has no
// prefix); user-defined `__foo` names in imported modules
// would also stay bare under this rule but the convention in
// this codebase is that `__`-prefixed names ARE runtime helpers,
// so the bare-name preservation is correct.
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

// rewriteAll walks the module's AST applying two related rewrites:
//
//  1. `selfPrefix` is prepended to every top-level Func / Struct
//     name and to every internal reference to one (call site,
//     function-value reference, struct literal type name). For
//     the entry module selfPrefix is empty so this is a no-op.
//  2. `mod.fn(args)` and `mod.fn` (where `mod` is one of this
//     module's imports) get rewritten to direct references to
//     the imported module's mangled names.
//
// Both rewrites happen in one walk so the mangled output is
// consistent for the rest of the pipeline.
func (m *module) rewriteAll(selfPrefix string) []error {
	return m.rewriteAllOpts(selfPrefix, false, nil)
}

// rewriteAllOpts is rewriteAll with a `flatNamespace` knob — when
// true, cross-module references rewrite without the `<mod>__`
// prefix (`int.foo()` → `foo()`) and the own-decl selfPrefix is
// expected to be empty too. Used by `LoadStdlibFlat` so the
// flat loader can rewrite qualified imports inside stdlib
// bodies while keeping stdlib decl names bare.
//
// `skipPaths` is consulted only in flat-namespace mode: a cross-
// module reference targeting a skipped module rewrites to the
// mangled `<mod>__` form, matching the entry program's parallel
// modload-mangled load of that same path.
func (m *module) rewriteAllOpts(selfPrefix string, flatNamespace bool, skipPaths map[string]bool) []error {
	// Build the set of own-module function, struct, and const names
	// so we can recognise internal references (`fn(args)` /
	// `Foo { ... }` / `K`) versus references to outside symbols.
	ownFuncs := map[string]bool{}
	ownStructs := map[string]bool{}
	ownEnums := map[string]bool{}
	for _, ed := range m.prog.Enums {
		ownEnums[ed.Name] = true
	}
	for _, ud := range m.prog.Unions {
		// Same `ownEnums` set covers union names: the checker
		// desugars `type X = …;` to an EnumDecl with the same
		// name, so cross-module references treat union names and
		// enum names interchangeably for prefix purposes.
		ownEnums[ud.Name] = true
	}
	ownConsts := map[string]bool{}
	for _, fn := range m.prog.Funcs {
		// Runtime / codegen helpers keep their bare names —
		// see `isRuntimeHelperName` for the full rationale.
		// Their internal references — bare-name calls between
		// one helper and another — must NOT pick up the module
		// prefix either, or we'd produce `mod__<name>` idents
		// that nothing in the combined Program resolves.
		if isRuntimeHelperName(fn.Name) {
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
		// Receiver methods don't get the module prefix. Dispatch
		// happens through the checker's receiver-hoist + Methods
		// map: a method gets hoisted to `__method_<Type>_<Name>`
		// based on its receiver, independent of the source
		// module. Mangling the source-level name here would force
		// the hoist to produce `__method_<Type>_<mod>__<name>`,
		// which no `(x).method()` call site would resolve to.
		// Visibility across modules is enforced separately by
		// `checker.methodVisibleHere`, which consults each
		// FuncDecl's SourceModule (stamped during loadRecursive)
		// against the program's import-closure map.
		//
		// Same exemption applies to functions whose source-level
		// name is a runtime / codegen helper — see
		// `isRuntimeHelperName`. The checker's auto-discovery
		// keys off the `__method_` prefix and the codegen's
		// `case "map_new"` / `case "__map_get_impl"` / etc.
		// switches resolve targets by their bare name; prefixing
		// here would leave every call site dangling.
		if fn.Receiver == nil && fn.AssocType == "" && !isRuntimeHelperName(fn.Name) {
			fn.Name = selfPrefix + fn.Name
		} else if fn.Receiver != nil {
			r.rewriteType(&fn.Receiver.Type)
		} else if fn.AssocType != "" {
			// Associated-function impl member (`impl Trait for T { function f() }`):
			// exempt from the module prefix exactly like a receiver method. The
			// checker hoists it to `__assoc_<T>_<f>` from AssocType + the BARE
			// name, and conformance + `T.f()` dispatch look up that bare form;
			// prefixing the name here produced `__assoc_<T>_<mod>__<f>`, which no
			// conformance check or call site resolved (a primitive impl like
			// `impl num.Zero for i32` then failed "missing method"). Rewrite the
			// AssocType so a user-type impl in this module hoists under the type's
			// mangled name; a primitive (i32/f64/…) is left unchanged.
			fn.AssocType = r.rewriteStructName(fn.AssocType)
		}
		for i := range fn.Params {
			r.rewriteType(&fn.Params[i].Type)
		}
		r.rewriteType(&fn.ReturnType)
		r.rewriteFuncBody(fn)
	}
	for _, sd := range m.prog.Structs {
		sd.Name = selfPrefix + sd.Name
		for i := range sd.Fields {
			r.rewriteType(&sd.Fields[i].Type)
		}
		// `@derive(Trait)` trait names are rewritten like any other
		// trait reference so a cross-module `@derive(cmp.Eq)` lines up.
		for i, dn := range sd.Derives {
			sd.Derives[i] = r.rewriteTraitNameAt(dn, sd.P)
		}
	}
	for _, ed := range m.prog.Enums {
		ed.Name = selfPrefix + ed.Name
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				r.rewriteType(&ed.Variants[i].Payloads[j])
			}
		}
		for i, dn := range ed.Derives {
			ed.Derives[i] = r.rewriteTraitNameAt(dn, ed.P)
		}
	}
	for _, ud := range m.prog.Unions {
		// Mangling lines up with the synthesised EnumDecl the
		// checker will produce. Member struct names get the same
		// own-module prefix because they're declared alongside.
		ud.Name = selfPrefix + ud.Name
		for i, member := range ud.Members {
			if ownStructs[member] {
				ud.Members[i] = selfPrefix + member
			}
		}
	}
	for _, cd := range m.prog.Consts {
		cd.Name = selfPrefix + cd.Name
		r.rewriteType(&cd.Type)
		r.rewriteExpr(&cd.Value)
	}
	// Traits / impls / type-parameter bounds. Trait names are mangled
	// like struct names (own-module names get selfPrefix), and the
	// trait references in impls + bounds are rewritten through the same
	// qualified-or-own logic so a `[T: mod.Trait]` bound or an
	// `impl mod.Trait for …` lines up with the mangled TraitDecl.Name.
	// The impl's `for` type and the impl methods (ordinary receiver
	// methods in prog.Funcs) are already rewritten above. See
	// docs/TRAITS.md (Phase 3).
	for _, td := range m.prog.Traits {
		td.Name = selfPrefix + td.Name
		for i := range td.Methods {
			for j := range td.Methods[i].Params {
				r.rewriteType(&td.Methods[i].Params[j].Type)
			}
			r.rewriteType(&td.Methods[i].Result)
		}
		// Supertrait references mangle the same way a `[T: mod.Trait]`
		// bound or an `impl mod.Trait for …` does, so they line up with
		// the mangled TraitDecl.Name they point at.
		for k, sup := range td.Supertraits {
			td.Supertraits[k] = r.rewriteTraitNameAt(sup, td.NamePos)
		}
	}
	for _, impl := range m.prog.Impls {
		r.rewriteType(&impl.Type)
		impl.Trait = r.rewriteTraitNameAt(impl.Trait, impl.TraitPos)
		// Generic-trait args (`impl From[mod.Foo] for …`) are types too —
		// mangle any struct/enum names so they line up with the trait sig.
		for i := range impl.TraitArgs {
			r.rewriteType(&impl.TraitArgs[i])
		}
	}
	for _, fn := range m.prog.Funcs {
		// The impl method's own record of which trait provided it,
		// mangled to line up with the ImplDecl.Trait rewritten above.
		fn.ImplTrait = r.resolveTraitName(fn.ImplTrait)
		for tp, traits := range fn.Bounds {
			for k, tn := range traits {
				fn.Bounds[tp][k] = r.rewriteTraitNameAt(tn, fn.P)
			}
		}
		// Generic-trait bound args (`T: From[mod.Foo]`) are types — mangle
		// any struct/enum names so they line up with the trait signature.
		for _, perBound := range fn.BoundArgs {
			for _, args := range perBound {
				for i := range args {
					r.rewriteType(&args[i])
				}
			}
		}
	}
	return r.errs
}

// rewriter holds the per-module state the AST walk needs.
type rewriter struct {
	modPath    string             // path of the module being rewritten (for error messages)
	selfPrefix string             // prefix for this module's own decls
	ownFuncs   map[string]bool    // names of funcs declared in this module (pre-mangle)
	ownStructs map[string]bool    // names of structs declared in this module (pre-mangle)
	ownEnums   map[string]bool    // names of enums + unions declared in this module (pre-mangle)
	ownConsts  map[string]bool    // names of consts declared in this module (pre-mangle)
	ownTraits  map[string]bool    // names of traits declared in this module (pre-mangle)
	imports    map[string]*module // local name → imported module
	errs       []error            // visibility / unresolved-name errors collected during the walk
	// localVars is the set of identifier names bound as local
	// variables / parameters in the currently-walking function
	// body. Populated by `rewriteFuncBody` from the function's
	// params + a pre-walk that collects `var` declarations. The
	// Ident rewriter consults the set so a local `var range: u32`
	// inside a function whose enclosing module also declares
	// `function range(…)` doesn't get the module-prefix mangling
	// applied to its uses.
	localVars map[string]bool
	// flatNamespace, when true, drops the `<mod>__` prefix on
	// cross-module references — `int.foo()` becomes `foo()`
	// instead of `int__foo()`. Used by `LoadStdlibFlat` so the
	// flat loader can rewrite qualified imports inside
	// stdlib bodies without mangling stdlib decls (decls stay
	// at their bare names, which user code calls directly).
	// Safe for stdlib because every free-function name there is
	// globally unique (receiver methods don't collide because
	// dispatch is by receiver type).
	flatNamespace bool
	// skipPaths is consulted only in flat-namespace mode: a
	// cross-module reference whose target lives in a skipped
	// module rewrites to the mangled `<mod>__` form (matching
	// what modload's regular `Load`+`combine` would produce
	// for the entry program's parallel load of that path).
	// Without this, a flat-namespace body referencing a
	// skipped module's decl by qualified name would rewrite to
	// bare — but the entry program's modload-mangled copy lives
	// under the prefixed name, and the bare lookup would fail.
	skipPaths map[string]bool
}

// packageScopedOK reports whether `name` is a `pub(package)` decl of
// `mod` that the current module may use (i.e. is in the same package).
// `handled` is true when `name` is package-scoped at all — so the caller
// knows not to fall through to the generic "not exported" error. When
// handled but not same-package, it records the package-scope error.
func (r *rewriter) packageScopedOK(mod *module, name string, pos ast.Position) (ok, handled bool) {
	if !mod.packageScoped[name] {
		return false, false
	}
	if samePackage(r.modPath, mod.path) {
		return true, true
	}
	r.errs = append(r.errs, fmt.Errorf("%s:%s: %s.%s is `pub(package)` — only modules in the same package as %s may use it",
		r.modPath, pos, mod.name, name, mod.name))
	return false, true
}

// checkPublicFunc records an error if `fn` isn't exported from
// `mod`. Cross-module function references go through this gate;
// same-module references skip it because internal calls aren't
// visibility-restricted.
// reportUndeclared records the error for a `mod.X` where the module has no
// top-level `X` at all — as opposed to having a private one. The two used to
// share the "is not exported" message, so a typo in a qualified name told the
// reader to add `pub` to a declaration that did not exist. For a qualified
// TYPE that was also the only error emitted, since nothing downstream reports
// the unknown name.
//
// `kind` names what the reference position expected ("type", "function",
// "function or const") so the message reads at the site rather than
// describing the checker.
func (r *rewriter) reportUndeclared(mod *module, name, kind string, pos ast.Position) {
	hint := didYouMean(name, mod.exportedNames())
	// A method is exported under its bare name but is not callable
	// module-qualified, so offering it as a plain did-you-mean sends the
	// reader from this error straight into an E001 on the name it just
	// told them to write. Name the receiver form instead — that is the
	// spelling that works.
	if hint == "" {
		if m := closest(name, mod.methodNames()); m != "" {
			hint = fmt.Sprintf(" — %q is a method, call it on the value: `x.%s(…)`", m, m)
		}
	}
	r.errs = append(r.errs, fmt.Errorf("%s:%s: module %q has no %s %q%s",
		r.modPath, pos, mod.name, kind, name, hint))
}

// methodNames is the exported receiver methods, for the hint above. Sorted so
// the suggestion is deterministic.
func (m *module) methodNames() []string {
	out := make([]string, 0, len(m.publicMethods))
	for n := range m.publicMethods {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// exportedNames is every name this module makes visible to importers, for
// the did-you-mean hint. Sorted so the suggestion is deterministic.
func (m *module) exportedNames() []string {
	seen := map[string]bool{}
	for _, set := range []map[string]bool{m.publicFuncs, m.publicStructs, m.publicEnums, m.publicConsts} {
		for n := range set {
			seen[n] = true
		}
	}
	// Methods are in publicFuncs for visibility, but `mod.method(x)` is not a
	// call anyone can write — suggesting one here would be advice that fails.
	// reportUndeclared offers them separately, in their receiver form.
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

// didYouMean returns " (did you mean \"X\"?)" for the closest candidate
// within a small edit distance, or "" when nothing is close enough. Keeping
// the threshold tight matters more than coverage: a wrong suggestion on a
// name the reader typed correctly costs more than no suggestion.
func didYouMean(name string, candidates []string) string {
	best := closest(name, candidates)
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

// closest is the nearest candidate within the same tight edit-distance
// threshold didYouMean uses, or "" when nothing is close enough.
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
	// A method is exported and visible, but `mod.name(x)` is not how it is
	// called — the reference mangles to a symbol nothing declares, so letting
	// it through hands the reader an "undefined identifier mod__name" from
	// the checker with no mention of the receiver form they actually want.
	// Reported here, where the module is still in hand to say so.
	if mod.publicMethods[fn] && !mod.publicPlainFuncs[fn] {
		r.errs = append(r.errs, fmt.Errorf("%s:%s: %s.%s is a method — call it on the value: `x.%s(…)`",
			r.modPath, pos, mod.name, fn, fn))
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
	r.errs = append(r.errs, fmt.Errorf("%s:%s: %s.%s is not exported (declare it as `pub function %s …` to make it accessible from other modules)",
		r.modPath, pos, mod.name, fn, fn))
}

// checkPublicStruct records an error if `name` isn't an exported
// struct of `mod`. Used at every cross-module struct-type or
// struct-literal reference. Falls back on publicEnums for type-name
// references like `mod.Token` that resolve to an enum (or to a
// union, which the checker desugars to an enum) — those go through
// the same `mod.Foo` parse shape as struct types and we can't tell
// them apart pre-checker.
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
	r.errs = append(r.errs, fmt.Errorf("%s:%s: %s.%s is not exported (declare it as `pub struct %s …` to make it accessible from other modules)",
		r.modPath, pos, mod.name, name, name))
}

// checkPublicValue gates a `mod.X` reference where X is expected
// to be a function value or a const — both share the value-style
// reference shape (`Ident("X")` after rewriting). Public funcs and
// public consts are accepted. Private decls produce a fix-hint
// keyed off the actual declaration kind so users see `pub const X`
// for a private const and `pub function X` for a private function.
// Unknown names default to the function hint, which is the more
// common case at this position; unresolved-name errors will surface
// later in the checker either way.
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
	r.errs = append(r.errs, fmt.Errorf("%s:%s: %s.%s is not exported (declare it as `pub %s %s …` to make it accessible from other modules)",
		r.modPath, pos, mod.name, name, hint, name))
}

// importedModule looks up a local-name binding from this module's
// import list, returning the resolved module + its mangling prefix.
// `mod.fn(args)`, `mod.fn` (value), and `mod.Foo` references all
// route through here; visibility checks at the call sites use the
// returned *module to ask whether the named decl is `pub`.
func (r *rewriter) importedModule(localName string) (*module, string, bool) {
	mod, ok := r.imports[localName]
	if !ok {
		return nil, "", false
	}
	if r.flatNamespace {
		// In flat-namespace mode, references to OWN-pass modules
		// rewrite to bare names (the decls land bare-named in the
		// combined Program). References to skipped modules — i.e.
		// modules the flat loader is skipping because the
		// entry program already loaded them through modload's
		// regular mangling path — need to use the same mangled
		// prefix `Load`+`combine` would produce, since the
		// skipped module's decls live in `prog` under those
		// mangled names. Without this, a stdlib body loaded via
		// LoadStdlibFlat that calls `int.foo()` qualified would
		// rewrite to bare `foo()`, which the entry's mangled
		// `int__foo` wouldn't match.
		if r.skipPaths[mod.path] {
			return mod, mod.manglePrefix, true
		}
		return mod, "", true
	}
	return mod, mod.manglePrefix, true
}

// rewriteFuncBody walks fn's body with a fresh local-var set
// scoped to that function. The set is the union of fn's
// parameters + any `var` declared anywhere in the body,
// including in nested blocks. The granularity is intentionally
// coarse — we collect at function granularity, not lexical
// scope — because we only need to answer "is this Ident a
// local at *all* in this function?" so the Ident rewriter can
// skip the module-prefix mangling for local variable
// references that happen to share a name with a module-level
// function or const. A var declared inside a block that
// shadows a function with the same name correctly suppresses
// the rewrite for *every* use of that name inside the
// function — that's not lexically precise but it matches the
// "if there's any local binding, treat it as a local" rule
// that's correct for our purposes (we'd never want to silently
// mangle a name that has a local binding somewhere in scope).
func (r *rewriter) rewriteFuncBody(fn *ast.FuncDecl) {
	prev := r.localVars
	r.localVars = map[string]bool{}
	for _, p := range fn.Params {
		r.localVars[p.Name] = true
	}
	collectLocals(fn.Body, r.localVars)
	r.rewriteBlock(fn.Body)
	r.localVars = prev
}

// collectLocals adds to dst every name b binds — a `var`, a
// destructure, a `for` binder, and a match arm's payload and `@`
// bindings — at any depth, INCLUDING inside a block sitting in
// expression position (`defer { … }`, a value `if`). It runs over
// ast.Walk rather than its own switch so a new binding form is
// reachable here the moment the shared walk reaches it.
//
// A nested function or lambda is its own scope and is pruned:
// rewriteNestedFuncBody seeds one from this set and adds its own
// binders there, so letting them bleed out would suppress the
// rewrite of a module name the enclosing body legitimately uses.
func collectLocals(b *ast.Block, dst map[string]bool) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		ast.Walk(s, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl, *ast.Lambda:
				return false
			case *ast.Var:
				dst[x.Name] = true
			case *ast.Destructure:
				for _, name := range x.Names {
					dst[name] = true
				}
			case *ast.ForEach:
				dst[x.Var] = true
			case *ast.Match:
				for _, arm := range x.Arms {
					for _, name := range arm.Bindings {
						dst[name] = true
					}
					if arm.AtBinding != "" {
						dst[arm.AtBinding] = true
					}
				}
			}
			return true
		})
	}
}

func (r *rewriter) rewriteBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		r.rewriteStmt(s)
	}
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
		if x.Init != nil {
			r.rewriteStmt(x.Init)
		}
		r.rewriteExpr(&x.Cond)
		if x.Step != nil {
			r.rewriteStmt(x.Step)
		}
		r.rewriteStmt(x.Body)
	case *ast.ForEach:
		r.rewriteExpr(&x.Iter)
		r.rewriteStmt(x.Body)
	case *ast.Return:
		if x.Value != nil {
			r.rewriteExpr(&x.Value)
		}
	case *ast.Var:
		r.rewriteType(&x.Type)
		r.rewriteExpr(&x.Init)
	case *ast.Destructure:
		r.rewriteExpr(&x.Init)
	case *ast.ExprStmt:
		r.rewriteExpr(&x.Expr)
	case *ast.Match:
		r.rewriteExpr(&x.Tag)
		for _, arm := range x.Arms {
			r.rewriteVariantPattern(&arm.VariantModule, &arm.VariantName, arm.P)
			if arm.Guard != nil {
				r.rewriteExpr(&arm.Guard)
			}
			r.rewriteBlock(arm.Body)
		}
	case *ast.Defer:
		r.rewriteExpr(&x.Expr)
	case *ast.FuncDecl:
		// Nested local function — its name doesn't get the module
		// prefix because closure conversion mangles it on its own
		// (`__closure_<name>_N`). Walk the body for refs, with the
		// nested function's params + locals added to the shadow set
		// (a param named like a module-level function must stay
		// bound to the param).
		for i := range x.Params {
			r.rewriteType(&x.Params[i].Type)
		}
		r.rewriteType(&x.ReturnType)
		r.rewriteNestedFuncBody(x.Params, x.Body)
	}
}

// rewriteNestedFuncBody rewrites the body of a nested function
// (a Lambda expression or a local FuncDecl statement) with the
// nested function's params and body locals ADDED to the enclosing
// local-shadow set. Unlike rewriteFuncBody (top-level functions),
// the enclosing locals stay in the set: a nested function can
// capture them, and a captured local that shares a module-level
// function's name must keep resolving to the local.
func (r *rewriter) rewriteNestedFuncBody(params []ast.Param, body *ast.Block) {
	prev := r.localVars
	locals := make(map[string]bool, len(prev)+len(params))
	for k := range prev {
		locals[k] = true
	}
	for _, p := range params {
		locals[p.Name] = true
	}
	collectLocals(body, locals)
	r.localVars = locals
	r.rewriteBlock(body)
	r.localVars = prev
}

// rewriteExpr is the workhorse — checks every expression node for
// shapes that need a name rewrite.
func (r *rewriter) rewriteExpr(slot *ast.Expr) {
	if slot == nil || *slot == nil {
		return
	}
	switch x := (*slot).(type) {
	case *ast.Ident:
		// Same-module function value or const reference: prefix
		// with selfPrefix. Cross-module references arrive as
		// `mod.X` (FieldAccess) and are handled below.
		//
		// A local variable / parameter that happens to share a
		// name with a module-level function or const wins — we
		// skip the prefix so the reference stays bound to the
		// local. `localVars` is populated per-function by
		// `rewriteFuncBody`.
		if r.localVars[x.Name] {
			return
		}
		if r.ownFuncs[x.Name] || r.ownConsts[x.Name] {
			x.Name = r.selfPrefix + x.Name
		}
	case *ast.Call:
		// Recognise `mod.fn(args)` BEFORE recursing — the inner
		// FieldAccess shouldn't be visited as a normal field
		// access because mod isn't a struct.
		if fa, ok := x.Callee.(*ast.FieldAccess); ok {
			if id, ok := fa.Target.(*ast.Ident); ok {
				if mod, prefix, ok := r.importedModule(id.Name); ok {
					r.checkPublicFunc(mod, fa.Field, fa.P)
					mangled := prefix + fa.Field
					// A `pub use`-re-exported name resolves to its original
					// module's mangled name, not this module's prefix.
					if rx, ok := mod.reexports[fa.Field]; ok {
						mangled = rx
					}
					// Preserve the source-level call site so the
					// LSP can resolve hover / goto-def on `fn` in
					// `mod.fn()` after we rewrite the AST to a
					// mangled flat call.
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
		r.rewriteExpr(&x.Callee)
		for i := range x.Args {
			r.rewriteExpr(&x.Args[i])
		}
	case *ast.FieldAccess:
		// `mod.X` in non-callee position — either a function value
		// or a const reference. Both rewrite to a flat Ident at
		// the imported module's mangled name; the imported module
		// must export the named decl.
		if id, ok := x.Target.(*ast.Ident); ok {
			if mod, prefix, ok := r.importedModule(id.Name); ok {
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
		// Anonymous function expression — its body can reference
		// module-local functions and consts (e.g. a comparator
		// closure calling a sibling helper), which need the same
		// selfPrefix mangling as references anywhere else in the
		// module (#4802: this case was missing, so `import`ing any
		// module whose lambdas called module-local functions failed
		// E001). The lambda's params and body locals shadow
		// module-level names inside the body, but enclosing locals
		// stay visible to it (captures) — so extend the current
		// local set rather than resetting it like rewriteFuncBody
		// does for a top-level function.
		for i := range x.Params {
			r.rewriteType(&x.Params[i].Type)
		}
		r.rewriteType(&x.ReturnType)
		r.rewriteNestedFuncBody(x.Params, x.Body)
	case *ast.MatchExpr:
		r.rewriteExpr(&x.Tag)
		for _, arm := range x.Arms {
			r.rewriteVariantPattern(&arm.VariantModule, &arm.VariantName, arm.P)
			if arm.Guard != nil {
				r.rewriteExpr(&arm.Guard)
			}
			r.rewriteExpr(&arm.Body)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			r.rewriteStmt(st)
		}
		if x.Tail != nil {
			r.rewriteExpr(&x.Tail)
		}
	case *ast.StructLit:
		// Three shapes here:
		//   - `Foo { … }` where Foo lives in this module → prefix
		//     with selfPrefix.
		//   - `mod.Foo { … }` where mod is one of this module's
		//     imports → flatten to `<modname>__Foo` after the
		//     visibility check.
		//   - Anything else (a dotted name we don't recognise) is
		//     a checker-time error; leave it alone.
		x.TypeName = r.rewriteStructNameAt(x.TypeName, x.P)
		for i := range x.Fields {
			r.rewriteExpr(&x.Fields[i].Value)
		}
		// Struct-update `Foo { ...base, field: v }`: the spread
		// source is an ordinary expression and may itself reference
		// module-local names (or be a nested struct literal), so it
		// needs the same rewrite pass as the field values.
		if x.Base != nil {
			r.rewriteExpr(&x.Base)
		}
	case *ast.TupleLit:
		// `(e1, e2, …)` — recurse into each element. The cursor
		// idiom returns `(result, cursor)` tuples whose elements are
		// struct-update literals of module-local types; without this
		// the StructLit TypeName inside a tuple is left unmangled.
		for i := range x.Elems {
			r.rewriteExpr(&x.Elems[i])
		}
	}
}

// rewriteVariantPattern walks a match arm's variant pattern and
// applies the same name-mangling pipeline used for struct types:
//
//   - `mod.TokA(...)` → VariantModule resolves to the imported
//     module's canonical path, VariantName gains the module's
//     mangle prefix.
//   - `TokA(...)` with TokA declared in this module → VariantName
//     gains selfPrefix to line up with the (already-mangled)
//     EnumVariant.Name produced by the union desugar.
//
// The checker then matches arm.VariantName against the variants
// of the scrutinee enum (also mangled), and compares
// arm.VariantModule to the enum's SourceModule for safety.
func (r *rewriter) rewriteVariantPattern(armModule *string, armName *string, pos ast.Position) {
	if *armModule != "" {
		// `Color.Red`-style enum-qualified match arm. The
		// qualifier names an enum in this module, not an imported
		// module — leave both fields alone so the printer can
		// round-trip the qualifier and the checker can verify it
		// matches the scrutinee's enum. We just suppress the
		// "unknown module" diagnostic below.
		if r.ownEnums[*armModule] {
			return
		}
		mod, prefix, ok := r.importedModule(*armModule)
		if !ok {
			r.errs = append(r.errs, fmt.Errorf("%s:%s: unknown module %q in variant pattern", r.modPath, pos, *armModule))
			return
		}
		*armModule = mod.path
		*armName = prefix + *armName
		return
	}
	// Unqualified: if this module declares an enum/union the variant
	// could belong to, prepend selfPrefix so the name lines up with
	// the mangled EnumVariant. Same-module references stay bare
	// otherwise (e.g. core enums in a flat-namespace stdlib path).
	if r.ownStructs[*armName] {
		*armName = r.selfPrefix + *armName
	}
}

// rewriteStructName turns a struct name (possibly qualified as
// `mod.Foo` from the parser) into the flat mangled form. Same-
// module names get selfPrefix; imported names get the imported
// module's prefix and trip the visibility check.
func (r *rewriter) rewriteStructName(name string) string {
	return r.rewriteStructNameAt(name, ast.Position{})
}

// rewriteStructNameAt is rewriteStructName plus the source position
// so visibility errors can point back at the offending reference.
func (r *rewriter) rewriteStructNameAt(name string, pos ast.Position) string {
	if dot := indexByte(name, '.'); dot >= 0 {
		modName, structName := name[:dot], name[dot+1:]
		if mod, prefix, ok := r.importedModule(modName); ok {
			r.checkPublicStruct(mod, structName, pos)
			// A `pub use`-re-exported type resolves to its original
			// module's mangled name, not this facade's prefix.
			if rx, ok := mod.reexportTypes[structName]; ok {
				return rx
			}
			return prefix + structName
		}
		// Unrecognised module — leave as-is so the checker can
		// surface a clear "unknown module" error.
		return name
	}
	if r.ownStructs[name] || r.ownEnums[name] {
		return r.selfPrefix + name
	}
	return name
}

// rewriteTraitNameAt mangles a trait reference the same way
// rewriteStructNameAt mangles a struct reference: a qualified
// `mod.Trait` resolves through the importing module's table to the
// imported module's prefix; a bare own-module trait gets selfPrefix.
// An unrecognised bare name (e.g. a built-in or as-yet-unresolved
// trait) is left untouched for the checker to diagnose. See
// docs/TRAITS.md (Phase 3).
func (r *rewriter) rewriteTraitNameAt(name string, pos ast.Position) string {
	if dot := indexByte(name, '.'); dot >= 0 {
		// Traits register into the same publicStructs visibility set as
		// struct/enum names (see the `pub trait` handling in
		// loadRecursive), so reuse that gate.
		if mod, _, ok := r.importedModule(name[:dot]); ok {
			r.checkPublicStruct(mod, name[dot+1:], pos)
		}
	}
	return r.resolveTraitName(name)
}

// resolveTraitName is rewriteTraitNameAt's name mapping without the
// visibility check, for slots that repeat a trait reference already
// gated elsewhere (a method's ImplTrait stamp mirrors its ImplDecl's
// Trait) and so must not report the same error twice.
func (r *rewriter) resolveTraitName(name string) string {
	if dot := indexByte(name, '.'); dot >= 0 {
		modName, traitName := name[:dot], name[dot+1:]
		if mod, prefix, ok := r.importedModule(modName); ok {
			// A `pub use`-re-exported trait resolves to its declaring
			// module's mangled name, not this facade's prefix.
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

// indexByte is a tiny wrapper around strings.IndexByte to keep the
// modload package's import surface minimal — there's only one
// caller, and pulling in `strings` for one helper isn't worth the
// extra import line.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// rewriteType prefixes nominal struct-type references — own-module
// names get selfPrefix, qualified `mod.Foo` references get the
// imported module's prefix. Other type shapes recurse where
// appropriate.
func (r *rewriter) rewriteType(slot *ast.Type) {
	if slot == nil || *slot == nil {
		return
	}
	switch t := (*slot).(type) {
	case ast.StructType:
		newName := r.rewriteStructName(t.Name)
		args := r.rewriteTypeArgs(t.Args)
		if newName != t.Name || args != nil {
			if args == nil {
				args = t.Args
			}
			*slot = ast.StructType{Name: newName, Args: args}
		}
	case ast.EnumType:
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
		// `dyn mod.Trait` (or `dyn mod.A + B`) — mangle EVERY trait name
		// in the set the same way bounds and impls do, so the dyn type's
		// traits line up with the mangled TraitDecl.Name in Info.Traits.
		// Without this a qualified `dyn cmp.Display` keeps its dotted name
		// and fails the `unknown trait` check in validateDynTraitTypes.
		// DynTraitType carries no position; the public-visibility check
		// reports at the zero position, which is acceptable for the rare
		// non-pub case. Re-normalise (sort+dedup) via NewDynTraitTypeFull
		// since mangling can reorder names. Any generic trait-arguments
		// (`dyn Container[mod.Foo]`) are themselves rewritten and carried
		// through, kept paired with their trait across the re-sort.
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

// rewriteTypeArgs rewrites each element of a generic type-argument
// list (the `Args` on StructType / EnumType — `Map[K, V]`,
// `Option[T]`). Returns nil when there are no args so callers can
// cheaply detect "nothing to replace".
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

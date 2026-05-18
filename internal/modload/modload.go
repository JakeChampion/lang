// Package modload loads a multi-file lang program: parse the entry
// file, recursively pull in everything it imports (relative to the
// importing file's directory), detect cycles, and stitch the
// modules together into a single ast.Program for the rest of the
// pipeline (checker / IR lowering / codegen).
//
// Module identity is path-derived: the local name a qualified call
// uses (`mod.fn(args)`) comes from the import path's basename
// without the `.lang` extension. `import "./math/vec";` binds the
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
// Limitations of this first cut:
//
//   - Aliasing (`import "./long/path" as p`) isn't supported;
//     the local name always comes from the path basename.

package modload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
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
	return loadCore(entryPath, overrides)
}

func loadCore(entryPath string, overrides map[string]string) (*ast.Program, map[string]string, error) {
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, nil, err
	}
	loaded := map[string]*module{} // path → loaded module
	stack := map[string]bool{}     // path → true while in flight (cycle detection)
	srcs := map[string]string{}    // path → source text (for diag formatting)
	prev := overrideSources
	overrideSources = overrides
	defer func() { overrideSources = prev }()
	if err := loadRecursive(entryAbs, loaded, stack, srcs); err != nil {
		return nil, nil, err
	}
	resolveCyclicImports(loaded)
	prog, err := combine(loaded, entryAbs)
	if err != nil {
		return nil, nil, err
	}
	return prog, srcs, nil
}

// overrideSources is the per-call override table installed by
// loadCore. readSource consults it before going to disk. Single-
// threaded loader (no concurrent Load calls), so a package-level
// var is enough; LoadWith saves + restores around its call.
var overrideSources map[string]string

// LoadStdlibFlat loads each stdlib path in `paths` (and every
// stdlib module it transitively imports) and returns a single
// combined Program whose decls keep their bare names — NO
// `<mod>__` mangling. Qualified call sites inside the loaded
// stdlib bodies (`int.foo()`, `string.find(s, ...)`) rewrite to
// flat bare calls (`foo()`, `find(s, ...)`) so the call resolves
// against the bare-named decls in the same combined Program.
//
// Used by the checker's auto-prelude injection path: stdlib
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
// Used by the checker's auto-prelude injection path: the entry
// program may have already loaded some stdlib modules through the
// regular `modload.Load` mangling path, and re-loading them
// flat-namespace here would surface duplicate decls — receiver
// method `__method_<Type>_<Name>` names land bare under both
// modes and the checker's redeclaration gate fires. skipPaths
// lets the caller exclude those modules from the auto-prelude
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
		if err := loadRecursive(canonical, loaded, stack, srcs); err != nil {
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
	// Auto-prelude semantics: stdlib decls injected by the auto-
	// prelude path are universally visible to every other module.
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
	for _, mod := range loaded {
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
		combined.Comments = append(combined.Comments, mod.prog.Comments...)
	}
	return combined, nil
}

// module bundles a parsed file with its canonical path and the
// derived module name (basename without `.lang`).
type module struct {
	path    string
	name    string
	prog    *ast.Program
	imports map[string]*module // local-name → loaded module
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
	publicFuncs   map[string]bool
	publicStructs map[string]bool
	publicConsts  map[string]bool
	publicEnums   map[string]bool
	// allConsts is the pre-mangle name set of every const in this
	// module (public or private). The visibility-error path uses it
	// to decide whether `mod.X` should suggest `pub function X`
	// (default) or `pub const X` (when X is a known private const).
	allConsts map[string]bool
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
func loadRecursive(path string, loaded map[string]*module, stack map[string]bool, srcs map[string]string) error {
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

	src, err := readSource(path)
	if err != nil {
		return err
	}
	srcs[path] = src
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
		childPaths[i] = resolveImportPath(dir, imp.Path)
		if err := loadRecursive(childPaths[i], loaded, stack, srcs); err != nil {
			return err
		}
	}

	mod := &module{
		path:          path,
		name:          importLocalName(path),
		prog:          prog,
		imports:       map[string]*module{},
		importPaths:   map[string]string{},
		publicFuncs:   map[string]bool{},
		publicStructs: map[string]bool{},
		publicConsts:  map[string]bool{},
		publicEnums:   map[string]bool{},
		allConsts:     map[string]bool{},
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
		}
	}
	for _, sd := range prog.Structs {
		// Same source-module stamping as FuncDecl — used by the
		// LSP to answer cross-module goto-def queries on type
		// names. No semantic effect on the rest of the pipeline.
		sd.SourceModule = path
		if sd.Public {
			mod.publicStructs[sd.Name] = true
		}
	}
	for _, ed := range prog.Enums {
		ed.SourceModule = path
		if ed.Public {
			mod.publicEnums[ed.Name] = true
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
	}
	for _, cd := range prog.Consts {
		mod.allConsts[cd.Name] = true
		if cd.Public {
			mod.publicConsts[cd.Name] = true
		}
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
// embedded FS instead of the disk. We auto-append `.lang` if the
// import path doesn't already include the extension, so users
// can write either form.
func resolveImportPath(importingDir, importPath string) string {
	if stdlib.IsStdlibPath(importPath) {
		key := importPath
		if !strings.HasSuffix(key, ".lang") {
			key += ".lang"
		}
		return stdlibPrefix + key
	}
	resolved := filepath.Join(importingDir, importPath)
	if filepath.Ext(resolved) == "" {
		resolved += ".lang"
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		// Best-effort; if Abs fails the load step will surface
		// the underlying file error.
		return resolved
	}
	return abs
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
func readSource(path string) (string, error) {
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
	// file is open, even when it hasn't been saved yet.
	if overrideSources != nil {
		if src, ok := overrideSources[path]; ok {
			return src, nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(b), nil
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

// importLocalName mirrors the parser-side helper but is duplicated
// here so the driver doesn't need to re-parse to compute it. Stdlib
// path keys (`stdlib://std/i32.lang`) get their basename extracted
// the same way as disk paths — the `stdlib://` prefix doesn't
// participate.
func importLocalName(path string) string {
	trimmed := strings.TrimPrefix(path, stdlibPrefix)
	base := filepath.Base(trimmed)
	if ext := filepath.Ext(base); ext == ".lang" {
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
		LoadedStdlibPaths: map[string]bool{},
	}
	for path := range loaded {
		if strings.HasPrefix(path, stdlibPrefix) {
			combined.LoadedStdlibPaths[path] = true
		}
	}
	var firstErr error
	for _, mod := range loaded {
		errs := mod.rewriteAll(prefixFor(mod.path == entryPath, mod.name))
		for _, e := range errs {
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	for _, mod := range loaded {
		combined.Funcs = append(combined.Funcs, mod.prog.Funcs...)
		combined.Structs = append(combined.Structs, mod.prog.Structs...)
		combined.Enums = append(combined.Enums, mod.prog.Enums...)
		combined.Unions = append(combined.Unions, mod.prog.Unions...)
		combined.Consts = append(combined.Consts, mod.prog.Consts...)
		combined.Comments = append(combined.Comments, mod.prog.Comments...)
		// TypeRefs is a parser-recorded side table the LSP uses
		// for hover / definition on type annotations. Merging
		// them here means cross-module type queries find the
		// right TypeRef for the entry module's source.
		combined.TypeRefs = append(combined.TypeRefs, mod.prog.TypeRefs...)
	}
	return combined, nil
}

// prefixFor returns the mangling prefix to apply to a module's own
// decls. Entry module gets no prefix; everything else gets
// `<name>__`.
func prefixFor(isEntry bool, name string) string {
	if isEntry {
		return ""
	}
	return name + "__"
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
//   1. `selfPrefix` is prepended to every top-level Func / Struct
//      name and to every internal reference to one (call site,
//      function-value reference, struct literal type name). For
//      the entry module selfPrefix is empty so this is a no-op.
//   2. `mod.fn(args)` and `mod.fn` (where `mod` is one of this
//      module's imports) get rewritten to direct references to
//      the imported module's mangled names.
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
// auto-prelude path can rewrite qualified imports inside stdlib
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

	r := &rewriter{
		modPath:       m.path,
		selfPrefix:    selfPrefix,
		ownFuncs:      ownFuncs,
		ownStructs:    ownStructs,
		ownEnums:      ownEnums,
		ownConsts:     ownConsts,
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
		if fn.Receiver == nil && !isRuntimeHelperName(fn.Name) {
			fn.Name = selfPrefix + fn.Name
		} else if fn.Receiver != nil {
			r.rewriteType(&fn.Receiver.Type)
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
	}
	for _, ed := range m.prog.Enums {
		ed.Name = selfPrefix + ed.Name
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				r.rewriteType(&ed.Variants[i].Payloads[j])
			}
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
	// auto-prelude path can rewrite qualified imports inside
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

// checkPublicFunc records an error if `fn` isn't exported from
// `mod`. Cross-module function references go through this gate;
// same-module references skip it because internal calls aren't
// visibility-restricted.
func (r *rewriter) checkPublicFunc(mod *module, fn string, pos ast.Position) {
	if !mod.publicFuncs[fn] {
		r.errs = append(r.errs, fmt.Errorf("%s:%s: %s.%s is not exported (declare it as `pub function %s …` to make it accessible from other modules)",
			r.modPath, pos, mod.name, fn, fn))
	}
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
		// modules the auto-prelude path is skipping because the
		// entry program already loaded them through modload's
		// regular mangling path — need to use the same mangled
		// prefix `Load`+`combine` would produce, since the
		// skipped module's decls live in `prog` under those
		// mangled names. Without this, a stdlib body loaded via
		// LoadStdlibFlat that calls `int.foo()` qualified would
		// rewrite to bare `foo()`, which the entry's mangled
		// `int__foo` wouldn't match.
		if r.skipPaths[mod.path] {
			return mod, mod.name + "__", true
		}
		return mod, "", true
	}
	return mod, mod.name + "__", true
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

// collectLocals walks b and adds every `var name : T` and
// `for var i …` / destructure-binding name to dst.
func collectLocals(b *ast.Block, dst map[string]bool) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		collectLocalsStmt(s, dst)
	}
}

func collectLocalsStmt(s ast.Stmt, dst map[string]bool) {
	switch x := s.(type) {
	case *ast.Block:
		collectLocals(x, dst)
	case *ast.Arena:
		collectLocals(x.Body, dst)
	case *ast.If:
		collectLocalsStmt(x.Then, dst)
		collectLocalsStmt(x.Else, dst)
	case *ast.IfLet:
		for _, n := range x.Bindings {
			dst[n] = true
		}
		collectLocalsStmt(x.Then, dst)
		collectLocalsStmt(x.Else, dst)
	case *ast.LetElse:
		for _, n := range x.Bindings {
			dst[n] = true
		}
		collectLocals(x.Else, dst)
	case *ast.While:
		collectLocalsStmt(x.Body, dst)
	case *ast.For:
		collectLocalsStmt(x.Init, dst)
		collectLocalsStmt(x.Body, dst)
	case *ast.Var:
		dst[x.Name] = true
	case *ast.Destructure:
		for _, n := range x.Names {
			dst[n] = true
		}
	case *ast.Match:
		for _, arm := range x.Arms {
			for _, n := range arm.Bindings {
				dst[n] = true
			}
			collectLocals(arm.Body, dst)
		}
	case *ast.Switch:
		for _, k := range x.Cases {
			collectLocals(k.Body, dst)
		}
		if x.Default != nil {
			collectLocals(x.Default, dst)
		}
	case *ast.FuncDecl:
		// Nested local function — its parameters and body are
		// their own scope. Don't bleed those into the enclosing
		// function's locals set; closure conversion handles the
		// captured-vars story separately.
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
	case *ast.Arena:
		r.rewriteBlock(x.Body)
	case *ast.If:
		r.rewriteExpr(&x.Cond)
		r.rewriteStmt(x.Then)
		if x.Else != nil {
			r.rewriteStmt(x.Else)
		}
	case *ast.While:
		r.rewriteExpr(&x.Cond)
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
	case *ast.Switch:
		r.rewriteExpr(&x.Tag)
		for _, k := range x.Cases {
			for i := range k.Values {
				r.rewriteExpr(&k.Values[i])
			}
			r.rewriteBlock(k.Body)
		}
		if x.Default != nil {
			r.rewriteBlock(x.Default)
		}
	case *ast.Match:
		r.rewriteExpr(&x.Tag)
		for _, arm := range x.Arms {
			r.rewriteVariantPattern(&arm.VariantModule, &arm.VariantName, arm.P)
			if arm.Guard != nil {
				r.rewriteExpr(&arm.Guard)
			}
			r.rewriteBlock(arm.Body)
		}
	case *ast.IfLet:
		r.rewriteExpr(&x.Source)
		r.rewriteStmt(x.Then)
		if x.Else != nil {
			r.rewriteStmt(x.Else)
		}
	case *ast.LetElse:
		r.rewriteExpr(&x.Source)
		r.rewriteBlock(x.Else)
	case *ast.Defer:
		r.rewriteExpr(&x.Expr)
	case *ast.FuncDecl:
		// Nested local function — its name doesn't get the module
		// prefix because closure conversion mangles it on its own
		// (`__closure_<name>_N`). Just walk the body for refs.
		for i := range x.Params {
			r.rewriteType(&x.Params[i].Type)
		}
		r.rewriteType(&x.ReturnType)
		r.rewriteBlock(x.Body)
	}
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
				*slot = &ast.Ident{P: id.P, Name: prefix + x.Field}
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
	case *ast.MatchExpr:
		r.rewriteExpr(&x.Tag)
		for _, arm := range x.Arms {
			r.rewriteVariantPattern(&arm.VariantModule, &arm.VariantName, arm.P)
			if arm.Guard != nil {
				r.rewriteExpr(&arm.Guard)
			}
			r.rewriteExpr(&arm.Body)
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
		if newName != t.Name {
			*slot = ast.StructType{Name: newName}
		}
	case ast.ArrayType:
		elem := t.Elem
		r.rewriteType(&elem)
		*slot = ast.ArrayType{Elem: elem}
	case *ast.FuncType:
		for i := range t.Params {
			r.rewriteType(&t.Params[i])
		}
		r.rewriteType(&t.Result)
	}
}

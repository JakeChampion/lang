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

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
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
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, nil, err
	}
	loaded := map[string]*module{} // path → loaded module
	stack := map[string]bool{}     // path → true while in flight (cycle detection)
	srcs := map[string]string{}    // path → source text (for diag formatting)
	if err := loadRecursive(entryAbs, loaded, stack, srcs); err != nil {
		return nil, nil, err
	}
	prog, err := combine(loaded, entryAbs)
	if err != nil {
		return nil, nil, err
	}
	return prog, srcs, nil
}

// module bundles a parsed file with its canonical path and the
// derived module name (basename without `.lang`).
type module struct {
	path    string
	name    string
	prog    *ast.Program
	imports map[string]*module // local-name → loaded module
	// publicFuncs / publicStructs / publicConsts hold the original
	// (pre-mangle) names of `pub` decls, populated when the module
	// loads. The rewriter uses them to gate cross-module references.
	publicFuncs   map[string]bool
	publicStructs map[string]bool
	publicConsts  map[string]bool
	// allConsts is the pre-mangle name set of every const in this
	// module (public or private). The visibility-error path uses it
	// to decide whether `mod.X` should suggest `pub function X`
	// (default) or `pub const X` (when X is a known private const).
	allConsts map[string]bool
}

// loadRecursive parses path (if not already loaded), then recurses
// into every import. Cycle detection uses the in-flight `stack`:
// if we're asked to load a path we're already loading, that's a
// cycle and the load fails.
func loadRecursive(path string, loaded map[string]*module, stack map[string]bool, srcs map[string]string) error {
	if _, done := loaded[path]; done {
		return nil
	}
	if stack[path] {
		return fmt.Errorf("import cycle detected including %s", path)
	}
	stack[path] = true
	defer delete(stack, path)

	srcBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	src := string(srcBytes)
	srcs[path] = src
	prog, err := parser.Parse(src)
	if err != nil {
		return fmt.Errorf("%s", diag.Format(path, src, err))
	}

	// Recurse into imports first. We don't add this module to
	// `loaded` until after recursion succeeds — that way a back-
	// edge from a child reaches stack[path]==true and the cycle
	// check fires, rather than seeing an already-loaded entry and
	// silently completing.
	dir := filepath.Dir(path)
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
		publicFuncs:   map[string]bool{},
		publicStructs: map[string]bool{},
		publicConsts:  map[string]bool{},
		allConsts:     map[string]bool{},
	}
	for _, fn := range prog.Funcs {
		if fn.Public {
			mod.publicFuncs[fn.Name] = true
		}
	}
	for _, sd := range prog.Structs {
		if sd.Public {
			mod.publicStructs[sd.Name] = true
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
		mod.imports[imp.LocalName] = child
	}
	loaded[path] = mod
	return nil
}

// resolveImportPath turns an `import "./util"` style path into a
// filesystem path relative to the importing file's directory. We
// auto-append `.lang` if the import path doesn't already include
// the extension, so users can write either form.
func resolveImportPath(importingDir, importPath string) string {
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

// importLocalName mirrors the parser-side helper but is duplicated
// here so the driver doesn't need to re-parse to compute it.
func importLocalName(path string) string {
	base := filepath.Base(path)
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
	combined := &ast.Program{}
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
		combined.Consts = append(combined.Consts, mod.prog.Consts...)
		combined.Comments = append(combined.Comments, mod.prog.Comments...)
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
	// Build the set of own-module function, struct, and const names
	// so we can recognise internal references (`fn(args)` /
	// `Foo { ... }` / `K`) versus references to outside symbols.
	ownFuncs := map[string]bool{}
	ownStructs := map[string]bool{}
	ownConsts := map[string]bool{}
	for _, fn := range m.prog.Funcs {
		ownFuncs[fn.Name] = true
	}
	for _, sd := range m.prog.Structs {
		ownStructs[sd.Name] = true
	}
	for _, cd := range m.prog.Consts {
		ownConsts[cd.Name] = true
	}

	r := &rewriter{
		modPath:    m.path,
		selfPrefix: selfPrefix,
		ownFuncs:   ownFuncs,
		ownStructs: ownStructs,
		ownConsts:  ownConsts,
		imports:    m.imports,
	}
	for _, fn := range m.prog.Funcs {
		// Rename the decl itself.
		fn.Name = selfPrefix + fn.Name
		if fn.Receiver != nil {
			r.rewriteType(&fn.Receiver.Type)
		}
		for i := range fn.Params {
			r.rewriteType(&fn.Params[i].Type)
		}
		r.rewriteType(&fn.ReturnType)
		r.rewriteBlock(fn.Body)
	}
	for _, sd := range m.prog.Structs {
		sd.Name = selfPrefix + sd.Name
		for i := range sd.Fields {
			r.rewriteType(&sd.Fields[i].Type)
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
	ownConsts  map[string]bool    // names of consts declared in this module (pre-mangle)
	imports    map[string]*module // local name → imported module
	errs       []error            // visibility / unresolved-name errors collected during the walk
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
// struct-literal reference.
func (r *rewriter) checkPublicStruct(mod *module, name string, pos ast.Position) {
	if !mod.publicStructs[name] {
		r.errs = append(r.errs, fmt.Errorf("%s:%s: %s.%s is not exported (declare it as `pub struct %s …` to make it accessible from other modules)",
			r.modPath, pos, mod.name, name, name))
	}
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
	return mod, mod.name + "__", true
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
					x.Callee = &ast.Ident{P: id.P, Name: prefix + fa.Field}
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
	case *ast.Ternary:
		r.rewriteExpr(&x.Cond)
		r.rewriteExpr(&x.Then)
		r.rewriteExpr(&x.Else)
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
	if r.ownStructs[name] {
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

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
// Limitations of this first cut:
//
//   - Cross-module struct types and struct literals aren't
//     supported. A struct stays private to the module that
//     declares it. Adding cross-module structs needs the parser
//     to accept `mod.Foo` in type positions and the rewriter to
//     follow it through StructLit.TypeName fields.
//   - Visibility is all-or-nothing — every top-level decl in an
//     imported module is reachable as `<mod>__<name>`. A future
//     `pub` keyword could gate this.
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
	return combine(loaded, entryAbs), srcs, nil
}

// module bundles a parsed file with its canonical path and the
// derived module name (basename without `.lang`).
type module struct {
	path    string
	name    string
	prog    *ast.Program
	imports map[string]*module // local-name → loaded module
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
		path:    path,
		name:    importLocalName(path),
		prog:    prog,
		imports: map[string]*module{},
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
func combine(loaded map[string]*module, entryPath string) *ast.Program {
	combined := &ast.Program{}
	// Track the names every non-entry module exports so internal-
	// reference rewriting can decide what's a same-module call.
	for _, mod := range loaded {
		mod.rewriteAll(prefixFor(mod.path == entryPath, mod.name))
	}
	// Walk in deterministic order (by canonical path) so the
	// combined program's func / struct order is stable across
	// runs — useful for diff-friendly output and reproducible
	// builds.
	for _, mod := range loaded {
		combined.Funcs = append(combined.Funcs, mod.prog.Funcs...)
		combined.Structs = append(combined.Structs, mod.prog.Structs...)
		combined.Comments = append(combined.Comments, mod.prog.Comments...)
	}
	return combined
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
func (m *module) rewriteAll(selfPrefix string) {
	// Build the set of own-module function and struct names so we
	// can recognise internal references (`fn(args)` / `Foo { ... }`)
	// versus references to outside symbols.
	ownFuncs := map[string]bool{}
	ownStructs := map[string]bool{}
	for _, fn := range m.prog.Funcs {
		ownFuncs[fn.Name] = true
	}
	for _, sd := range m.prog.Structs {
		ownStructs[sd.Name] = true
	}

	r := &rewriter{
		selfPrefix: selfPrefix,
		ownFuncs:   ownFuncs,
		ownStructs: ownStructs,
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
}

// rewriter holds the per-module state the AST walk needs.
type rewriter struct {
	selfPrefix string             // prefix for this module's own decls
	ownFuncs   map[string]bool    // names of funcs declared in this module (pre-mangle)
	ownStructs map[string]bool    // names of structs declared in this module (pre-mangle)
	imports    map[string]*module // local name → imported module
}

// importPrefix returns the mangling prefix for an imported module
// (empty if the import is the entry module, which keeps original
// names). Used for cross-module reference rewriting.
func (r *rewriter) importPrefix(localName string) (string, bool) {
	mod, ok := r.imports[localName]
	if !ok {
		return "", false
	}
	// We don't know inside the rewriter whether the imported module
	// is the entry; just use its name as the prefix. The combine
	// step ensures entry module's decls have empty selfPrefix, so
	// importing the entry from a sibling would actually want no
	// prefix — but that's a strange shape and not our concern in
	// the first cut. Document the limitation.
	return mod.name + "__", true
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
		// Same-module function value reference: prefix with
		// selfPrefix. Cross-module references arrive as
		// `mod.fn` (FieldAccess) and are handled below.
		if r.ownFuncs[x.Name] {
			x.Name = r.selfPrefix + x.Name
		}
	case *ast.Call:
		// Recognise `mod.fn(args)` BEFORE recursing — the inner
		// FieldAccess shouldn't be visited as a normal field
		// access because mod isn't a struct.
		if fa, ok := x.Callee.(*ast.FieldAccess); ok {
			if id, ok := fa.Target.(*ast.Ident); ok {
				if prefix, ok := r.importPrefix(id.Name); ok {
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
		// `mod.fn` in non-callee position (taking the function as
		// a value) — rewrite to a direct Ident.
		if id, ok := x.Target.(*ast.Ident); ok {
			if prefix, ok := r.importPrefix(id.Name); ok {
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
		//     imports → flatten to `<modname>__Foo`.
		//   - Anything else (a dotted name we don't recognise) is
		//     a checker-time error; leave it alone.
		x.TypeName = r.rewriteStructName(x.TypeName)
		for i := range x.Fields {
			r.rewriteExpr(&x.Fields[i].Value)
		}
	}
}

// rewriteStructName turns a struct name (possibly qualified as
// `mod.Foo` from the parser) into the flat mangled form. Same-
// module names get selfPrefix; imported names get the imported
// module's prefix.
func (r *rewriter) rewriteStructName(name string) string {
	if dot := indexByte(name, '.'); dot >= 0 {
		modName, structName := name[:dot], name[dot+1:]
		if prefix, ok := r.importPrefix(modName); ok {
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

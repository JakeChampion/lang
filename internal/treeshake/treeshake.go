// Package treeshake removes unreferenced top-level functions
// from a checked + monomorphised program before codegen.
//
// The stdlib modules a program imports (resolved through
// modload) pull in helpers, but most programs use only a small
// subset. Without tree-shaking, codegen would emit every loaded
// helper, blowing up binary size for trivial programs.
// Tree-shake makes the stdlib effectively pay-for-what-you-use.
//
// Algorithm: collect entry points (main + handle + anything
// referenced as a function value or address-taken), then BFS
// the call graph by scanning each reachable function's body
// for `*ast.Call` and `*ast.Ident` references whose name
// resolves to a top-level FuncDecl. Drop Funcs not reached.
//
// Idempotent — running on an already-pruned program is a
// no-op.
package treeshake

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// DynCoercionImplMethods returns the mangled impl-method names that a
// `dyn Trait` vtable points at, for every (trait, concrete) pair the
// checker recorded as a coercion site. These methods are reachable only
// through the runtime vtable, never via a static call the tree-shake /
// IR reachability walkers can follow, so each backend pins them as
// extra roots (`treeshake.Run(prog, DynCoercionImplMethods(info)...)`).
//
// For a struct/enum concrete the vtable slot points directly at the real
// receiver method, so the mangled `__method_<C>_<m>` is rooted (mirrors
// ir.collectVtables' struct/enum slot resolution: info.Methods, falling
// back to the conventional name).
//
// For a primitive/string concrete the value is heap-boxed at the coercion
// site and the vtable slot points at a synthesized unboxing WRAPPER
// `__dynbox_<C>_<m>` (docs/DYN-TRAITS.md §4.2.3). That wrapper is generated
// during IR lowering, not present in the AST, so rooting its name here is a
// (harmless) no-op for the AST-level tree-shaker; what tree-shake must keep
// alive is the REAL `__method_<C>_<m>` the wrapper calls — that AST func
// must survive into IR lowering where the wrapper's call edge picks it up.
// So both the real method and the wrapper name are rooted: the former keeps
// the AST func, the latter documents the vtable edge (and is robust to any
// IR-level reachability consumer of these roots).
//
// Shared by the wasm and x86-64 build paths. See docs/DYN-TRAITS.md.
func DynCoercionImplMethods(info *checker.Info) []string {
	if info == nil || len(info.DynCoercions) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	isPrimitive := func(concrete string) bool {
		if _, isStruct := info.Structs[concrete]; isStruct {
			return false
		}
		if _, isEnum := info.Enums[concrete]; isEnum {
			return false
		}
		return true
	}
	for _, dc := range info.DynCoercions {
		// Root the impl methods of EVERY trait in the `dyn` set (a
		// multi-trait `dyn A + B` needs A's and B's methods kept alive).
		// Fall back to the single Trait field if Traits is unset (older
		// callers / single-trait sites).
		traits := dc.Traits
		if len(traits) == 0 {
			traits = []string{dc.Trait}
		}
		prim := isPrimitive(dc.Concrete)
		for _, tr := range traits {
			td, ok := info.Traits[tr]
			if !ok {
				continue
			}
			for _, m := range td.Methods {
				if m.Assoc {
					continue
				}
				// Always root the real method: a struct/enum vtable slot
				// points at it, and a primitive's wrapper calls it (so it
				// must survive into IR lowering where the wrapper is
				// generated).
				fn := info.Methods[dc.Concrete+"."+m.Name]
				if fn == "" {
					fn = "__method_" + dc.Concrete + "_" + m.Name
				}
				add(fn)
				// For a primitive concrete also root the wrapper name (the
				// actual vtable target). No-op in the AST tree-shaker today.
				if prim {
					add("__dynbox_" + dc.Concrete + "_" + m.Name)
				}
			}
		}
	}
	return out
}

// DowncastImplMethods returns the mangled impl-method names referenced by
// the `(Trait, T)` vtable a `e as? T` downcast compares against. The
// compiled downcast (docs/DYN-TRAITS.md §9) emits `OpConstVtable{Trait,
// T}` and the backend materialises the `__vtable_<Trait>_<T>` cell, whose
// slots are `.quad __method_<T>_<m>` (natives) / function-table indices
// (wasm). Those methods are reachable ONLY through that static vtable, so
// — exactly like DynCoercionImplMethods for coercion sites — they must be
// pinned as tree-shake roots or the vtable would reference a dropped
// symbol (link failure). This matters for a DOWNCAST-ONLY target: a `T`
// that is never coerced to `dyn Trait`, only downcast to, is absent from
// info.DynCoercions and so would be missed by DynCoercionImplMethods.
//
// Struct/enum targets only (the slice-1 downcast scope); collectVtables
// routes struct/enum slots at the real `__method_<T>_<m>`, so that is the
// name that must survive into codegen.
func DowncastImplMethods(prog *ast.Program, info *checker.Info) []string {
	if prog == nil || info == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	ast.WalkProgram(prog, func(n ast.Node) bool {
		dc, ok := n.(*ast.DowncastExpr)
		if !ok || dc.Trait == "" {
			return true
		}
		concrete := ""
		switch t := dc.Target.(type) {
		case ast.StructType:
			concrete = t.Name
		case ast.EnumType:
			concrete = t.Name
		default:
			return true
		}
		// Root the impl methods of EVERY trait in the set, not just the
		// primary `dc.Trait`. The merged `(set, T)` vtable a multi-trait
		// `dyn A + B` downcast compares against contains the concatenation
		// of all the set's traits' methods over T (docs/DYN-TRAITS.md §10),
		// so a downcast-only T (never coerced elsewhere) needs every one of
		// them pinned or the merged vtable cell would reference a dropped
		// symbol. For a single-trait `dyn A` downcast dc.Traits == [A], so
		// this is byte-identical to rooting dc.Trait alone.
		traits := dc.Traits
		if len(traits) == 0 {
			traits = []string{dc.Trait}
		}
		for _, tr := range traits {
			td, ok := info.Traits[tr]
			if !ok {
				continue
			}
			for _, m := range td.Methods {
				if m.Assoc {
					continue
				}
				fn := info.Methods[concrete+"."+m.Name]
				if fn == "" {
					fn = "__method_" + concrete + "_" + m.Name
				}
				add(fn)
			}
		}
		return true
	})
	return out
}

// watHelperDeps lists the stdlib functions a still-in-wat
// helper depends on, plus aliases the codegen layer
// rewrites at emit-time. The AST walker doesn't see those
// rewrites, so tree-shake needs this hint to know that
// e.g. some still-in-wat helper calls a stdlib
// function and shouldn't drop the latter when only the
// former is referenced.
var watHelperDeps = map[string][]string{
	// arr.push(v) lowers entirely in the IR (emitArrayPush) —
	// no per-stride stdlib function to keep alive. The
	// wasm-side `__memcpy` shim is gated separately via the
	// codegen-side wat-helper switch.
	//
	// Map runtime: AST-level calls go through the
	// type-rich `__method_Map_*` / `map_new` /
	// `__method_MapIter_*` names; the stdlib bodies live
	// under `_impl` suffixes that the codegen alias rewrites
	// to. Pull each impl in when its alias is referenced.
	// map_new also roots __map_drop_values: the IR injects a call
	// to it at every owned-Map drop site (after lowering, so no
	// AST reference exists for tree-shake to follow), and every
	// owned map traces back to a map_new. It transitively pulls in
	// __map_dec_value / __map_val_kind / __map_val_stride from its
	// body.
	//
	// The three COW mutators (set / delete / clear) also root
	// __map_clone: the StructLit IR lowering injects a __map_clone
	// call when a Map-typed struct field is initialised by one of
	// these mutator results (issue #2763), and that injection — like
	// __map_drop_values above — happens after lowering, so there's no
	// AST reference for tree-shake to follow on its own.
	"map_new":             {"map_new_impl", "__map_drop_values"},
	"__method_Map_len":    {"__map_len_impl"},
	"__method_Map_has":    {"__map_has_impl", "__map_lookup", "__map_hash"},
	"__method_Map_get":    {"__map_get_impl", "__map_lookup", "__map_hash"},
	"__method_Map_get_or": {"__map_get_or_impl", "__map_lookup", "__map_hash"},
	"__method_Map_set":    {"__map_set_impl", "__map_grow", "__map_hash", "__map_lookup_val", "__map_clone"},
	"__method_Map_delete": {"__map_delete_impl", "__map_hash", "__map_clone"},
	// Struct/enum (keyKind-3) keys route through the `_keyed` runtime
	// variants (#2671). These alias names aren't real functions; the
	// entries pull in the keyed impls (whose bodies — including the
	// hash_fn/eq_fn params — are then walked normally). Enqueued from a
	// map-method Call / MapLit only when the key TypeArg is a
	// struct/enum, alongside that key type's derived hash/eq methods.
	"__method_Map_has_keyed":    {"__map_has_keyed_impl", "__map_lookup_keyed"},
	"__method_Map_get_keyed":    {"__map_get_keyed_impl", "__map_lookup_keyed"},
	"__method_Map_get_or_keyed": {"__map_get_or_keyed_impl", "__map_lookup_keyed"},
	"__method_Map_set_keyed":    {"__map_set_keyed_impl", "__map_grow_keyed", "__map_lookup_val_keyed", "__map_clone"},
	"__method_Map_delete_keyed": {"__map_delete_keyed_impl", "__map_clone"},
	"__method_Map_clear":        {"__map_clear_impl", "__map_clone"},
	"__method_Map_keys":         {"__map_keys_impl", "__map_column"},
	"__method_Map_values":       {"__map_values_impl", "__map_column"},
	"__method_Map_iter":         {"__map_iter_impl"},
	"__method_MapIter_has_next": {"__mapiter_has_next_impl"},
	"__method_MapIter_key":      {"__mapiter_key_impl", "__mapiter_entry_addr"},
	"__method_MapIter_value":    {"__mapiter_value_impl", "__mapiter_entry_addr"},
	"__method_MapIter_advance":  {"__mapiter_advance_impl"},
}

// keyedMapMethod reports whether a mangled method name is a Map
// operation that dispatches by hash/eq (so a struct/enum key routes it
// to the `_keyed` runtime variant — #2671). keys/values/iter/len/clear
// walk entries without hashing and need no keyed variant.
func keyedMapMethod(name string) bool {
	switch name {
	case "__method_Map_set", "__method_Map_get", "__method_Map_get_or",
		"__method_Map_has", "__method_Map_delete":
		return true
	}
	return false
}

// enqueueKeyedMapDeps pulls in the keyed-runtime impl + the key type's
// derived hash/eq methods when `kType` is a struct/enum map key. A
// non-nominal key (i32 / string / tuple) is a no-op — those don't use
// the keyed path. Mirrors the IR's mapKeyKindTag==3 routing so the AST
// tree-shake keeps exactly what codegen emits a call to.
func enqueueKeyedMapDeps(method string, kType ast.Type, enqueue func(string)) {
	name := nominalKeyName(kType)
	if name == "" {
		return
	}
	enqueue(method + "_keyed")
	enqueue("__method_" + name + "_hash")
	enqueue("__method_" + name + "_eq")
}

// nominalKeyName returns the struct/enum type name of a map key, or ""
// for a non-nominal key. Matches the IR's mapKeyTypeName.
func nominalKeyName(t ast.Type) string {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name
	case ast.EnumType:
		return x.Name
	}
	return ""
}

// Run mutates `prog.Funcs` to retain only functions reachable
// from the program's entry points. Function-typed values
// (e.g. `var f = some_func; ... f();`) keep `some_func` alive
// since the Ident reference appears in the body of the
// containing function.
//
// `extras` lists names that should be kept alive even when
// no AST reference points at them — used by codegen-emitted
// wrappers (e.g. the test-path `_start` printing main()'s
// result via `int_to_string`) where the call is generated
// outside the AST and tree-shake would otherwise drop the
// callee.
func Run(prog *ast.Program, extras ...string) {
	if len(prog.Funcs) == 0 {
		return
	}
	byName := map[string]*ast.FuncDecl{}
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	reachable := map[string]bool{}
	// `seen` tracks every name we've expanded the wat-helper
	// dependency map for, INCLUDING names that aren't in
	// byName (still-in-wat helpers like `query_parse`). This
	// ensures wat-helper-only references still pull in their
	// declared stdlib dependencies.
	seen := map[string]bool{}
	var queue []string
	enqueue := func(name string) {
		if name == "" {
			return
		}
		if !seen[name] {
			seen[name] = true
			for _, dep := range watHelperDeps[name] {
				// Recursive enqueue, single hop is enough
				// since deps themselves are lang funcs.
				if !seen[dep] {
					seen[dep] = true
					if _, ok := byName[dep]; ok && !reachable[dep] {
						reachable[dep] = true
						queue = append(queue, dep)
					}
				}
			}
		}
		if reachable[name] {
			return
		}
		if _, ok := byName[name]; !ok {
			return
		}
		reachable[name] = true
		queue = append(queue, name)
	}
	// Entry points: standard CLI main + HTTP handler. If
	// neither is present, fall back to keeping every user-
	// declared (non-stdlib) function — covers test programs
	// that compile a single helper like
	// `function f(): i32 { return 1; }` without a main.
	enqueue("main")
	enqueue("handle")
	// `__state_init` is the synthesised start function that
	// runs state-block init expressions at module instantiation
	// time. Codegen wires it up through the wasm `(start ...)`
	// section / arm64's `_start` prologue, neither of which the
	// AST walker sees — pin it as an entry point so its body
	// (and anything it calls) survives tree-shaking.
	enqueue("__state_init")
	for _, name := range extras {
		enqueue(name)
	}
	hasEntry := reachable["main"] || reachable["handle"]
	if !hasEntry {
		for _, fn := range prog.Funcs {
			enqueue(fn.Name)
		}
	}
	// Walk each reachable function's body, scanning for Call
	// callees and bare function-name Idents (function values).
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		fn := byName[name]
		if fn == nil || fn.Body == nil {
			continue
		}
		walkStmt(fn.Body, byName, enqueue)
	}
	// Filter prog.Funcs to the reachable set, preserving
	// declaration order.
	out := prog.Funcs[:0]
	for _, fn := range prog.Funcs {
		if reachable[fn.Name] {
			out = append(out, fn)
		}
	}
	prog.Funcs = out
}

func walkStmt(s ast.Stmt, byName map[string]*ast.FuncDecl, enqueue func(string)) {
	switch x := s.(type) {
	case *ast.Block:
		for _, st := range x.Stmts {
			walkStmt(st, byName, enqueue)
		}
	case *ast.If:
		walkExpr(x.Cond, byName, enqueue)
		walkStmt(x.Then, byName, enqueue)
		if x.Else != nil {
			walkStmt(x.Else, byName, enqueue)
		}
	case *ast.While:
		walkExpr(x.Cond, byName, enqueue)
		walkStmt(x.Body, byName, enqueue)
	case *ast.Loop:
		walkStmt(x.Body, byName, enqueue)
	case *ast.For:
		if x.Init != nil {
			walkStmt(x.Init, byName, enqueue)
		}
		walkExpr(x.Cond, byName, enqueue)
		if x.Step != nil {
			walkStmt(x.Step, byName, enqueue)
		}
		walkStmt(x.Body, byName, enqueue)
	case *ast.Return:
		if x.Value != nil {
			walkExpr(x.Value, byName, enqueue)
		}
	case *ast.Var:
		walkExpr(x.Init, byName, enqueue)
	case *ast.Destructure:
		walkExpr(x.Init, byName, enqueue)
	case *ast.ExprStmt:
		walkExpr(x.Expr, byName, enqueue)
	case *ast.Match:
		walkExpr(x.Tag, byName, enqueue)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExpr(arm.Guard, byName, enqueue)
			}
			walkStmt(arm.Body, byName, enqueue)
		}
	case *ast.Defer:
		walkExpr(x.Expr, byName, enqueue)
	case *ast.FuncDecl:
		// Local FuncDecl (closure-converted) — its body is
		// reachable via the closure conversion that hoisted
		// it. Walk too.
		if x.Body != nil {
			walkStmt(x.Body, byName, enqueue)
		}
	}
}

func walkExpr(e ast.Expr, byName map[string]*ast.FuncDecl, enqueue func(string)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.Ident:
		// Bare reference to a top-level function (function
		// value, address taken, or callee of a Call which
		// also lands here via Call.Callee).
		enqueue(x.Name)
	case *ast.Call:
		walkExpr(x.Callee, byName, enqueue)
		// Struct/enum (keyKind-3) map key: the IR routes this op to the
		// `_keyed` runtime variant (#2671), which dispatches through the
		// key type's derived hash/eq. Pull both the keyed impl alias and
		// those derived methods so codegen's emitted call resolves.
		if id, ok := x.Callee.(*ast.Ident); ok && keyedMapMethod(id.Name) && len(x.TypeArgs) >= 1 {
			enqueueKeyedMapDeps(id.Name, x.TypeArgs[0], enqueue)
		}
		for _, a := range x.Args {
			walkExpr(a, byName, enqueue)
		}
	case *ast.Binary:
		walkExpr(x.Left, byName, enqueue)
		walkExpr(x.Right, byName, enqueue)
	case *ast.Unary:
		walkExpr(x.Operand, byName, enqueue)
	case *ast.IfExpr:
		walkExpr(x.Cond, byName, enqueue)
		walkExpr(x.Then, byName, enqueue)
		walkExpr(x.Else, byName, enqueue)
	case *ast.TryOp:
		walkExpr(x.Inner, byName, enqueue)
	case *ast.MatchExpr:
		walkExpr(x.Tag, byName, enqueue)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExpr(arm.Guard, byName, enqueue)
			}
			walkExpr(arm.Body, byName, enqueue)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			walkStmt(st, byName, enqueue)
		}
		if x.Tail != nil {
			walkExpr(x.Tail, byName, enqueue)
		}
	case *ast.Assign:
		walkExpr(x.Target, byName, enqueue)
		walkExpr(x.Value, byName, enqueue)
	case *ast.Index:
		walkExpr(x.Array, byName, enqueue)
		walkExpr(x.Idx, byName, enqueue)
	case *ast.SliceExpr:
		walkExpr(x.Source, byName, enqueue)
		walkExpr(x.Low, byName, enqueue)
		walkExpr(x.High, byName, enqueue)
	case *ast.FieldAccess:
		walkExpr(x.Target, byName, enqueue)
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			walkExpr(el, byName, enqueue)
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			walkExpr(f.Value, byName, enqueue)
		}
		// Struct-update `Foo { ...base, field: v }`: the spread source is an
		// ordinary expression, so a call appearing ONLY there (`Foo { ...mk(),
		// … }`) is a real reference. Without this it looked unreachable and the
		// callee was pruned, leaving the emitted call site with an undefined
		// label at assembly time.
		if x.Base != nil {
			walkExpr(x.Base, byName, enqueue)
		}
	case *ast.MapLit:
		// IR lowers `Map { ... }` to map_new + a chain of
		// __method_Map_set calls — pull both alias names so
		// the codegen-emitted impls stay alive even when no
		// AST Call references them directly.
		enqueue("map_new")
		enqueue("__method_Map_set")
		// A struct/enum-keyed map literal lowers to the keyed set
		// variant (#2671); pull its impl + the key's derived hash/eq.
		enqueueKeyedMapDeps("__method_Map_set", x.KeyType, enqueue)
		for _, en := range x.Entries {
			walkExpr(en.Key, byName, enqueue)
			walkExpr(en.Value, byName, enqueue)
		}
	case *ast.TupleLit:
		for _, el := range x.Elems {
			walkExpr(el, byName, enqueue)
		}
	case *ast.EnumLit:
		for _, p := range x.Args {
			walkExpr(p, byName, enqueue)
		}
	case *ast.CastExpr:
		walkExpr(x.Inner, byName, enqueue)
	case *ast.DowncastExpr:
		walkExpr(x.Inner, byName, enqueue)
	case *ast.MakeClosure:
		// Closure formation references the hoisted body.
		enqueue(x.FuncName)
		for _, c := range x.Captures {
			walkExpr(c, byName, enqueue)
		}
	case *ast.Lambda:
		// Anonymous function expression — walk the body so any
		// top-level functions (in particular mangled method
		// names like `__method_string_trim`) referenced from
		// inside the lambda survive treeshake. Closureconv
		// hoists Lambda into a top-level FuncDecl, but that
		// runs AFTER treeshake; without this case the lambda
		// body is invisible to liveness analysis and any method
		// only reachable through a lambda gets pruned, leading
		// to "undefined reference to __method_string_trim" at
		// link time.
		walkStmt(x.Body, byName, enqueue)
	case *ast.CaptureRef:
		// CaptureRef targets a synthesised env variable; no
		// direct function reference.
	case *ast.FString:
		// Walk both the original interpolant Exprs (for any
		// top-level function references they make) and the
		// checker-built Desugared chain. The desugared chain
		// is where the synthesised `<expr>.to_string()` calls
		// live — by the time treeshake runs, the checker has
		// already rewritten those into direct calls keyed by
		// the mangled method name (e.g. `__method_string_to_string`),
		// which is what keeps the stdlib's `(s: string)
		// to_string()` body alive.
		for _, p := range x.Parts {
			if p.Expr != nil {
				walkExpr(p.Expr, byName, enqueue)
			}
		}
		walkExpr(x.Desugared, byName, enqueue)
	}
}

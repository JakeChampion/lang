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
// A `dyn Trait` coercion and an `as? T` downcast reach their
// vtable's impl methods through no call site at all, so the
// walk roots them when it reaches the SITE that builds the
// vtable (dynVtableRoots / downcastRoots). Rooting them from
// the whole program instead kept the impl methods of a
// coercion in a function nothing calls (#4114).
//
// Idempotent — running on an already-pruned program is a
// no-op.
package treeshake

import (
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// dynVtableRoots enqueues the mangled impl-method names that the
// `(traits, concrete)` vtable points at.
//
// These methods are reachable only through the runtime vtable
// (`OpConstVtable` names them by string), never via a static call the
// tree-shake / IR reachability walkers can follow, so the site that
// builds the vtable — a `dyn Trait` coercion or an `e as? T` downcast —
// has to root them itself.
//
// Every trait in the `dyn` set contributes: a multi-trait `dyn A + B`
// needs A's and B's methods kept alive.
//
// For a struct/enum concrete the vtable slot points directly at the real
// receiver method, so the mangled `__method_<C>_<m>` is rooted (mirrors
// ir.collectVtables' struct/enum slot resolution: the trait's registered
// impl, falling back to the conventional name).
//
// For a primitive/string concrete the value is heap-boxed at the coercion
// site and the vtable slot points at a synthesized unboxing WRAPPER
// `__dynbox_<C>_<m>` (docs/DYN-TRAITS.md §4.2.3). That wrapper is generated
// during IR lowering, not present in the AST, so rooting its name here is a
// (harmless) no-op for the AST-level tree-shaker; what tree-shake must keep
// alive is the REAL `__method_<C>_<m>` the wrapper calls — that AST func
// must survive into IR lowering where the wrapper's call edge picks it up.
// So both the real method and the wrapper name are rooted.
//
// See docs/DYN-TRAITS.md.
func dynVtableRoots(info *checker.Info, traits []string, concrete string, enqueue func(string)) {
	if info == nil || concrete == "" {
		return
	}
	_, isStruct := info.Structs[concrete]
	_, isEnum := info.Enums[concrete]
	prim := !isStruct && !isEnum
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
			enqueue(implMethodName(info, concrete, m.Name, tr))
			// For a primitive concrete also root the wrapper name (the
			// actual vtable target). No-op in the AST tree-shaker today.
			if prim {
				enqueue("__dynbox_" + concrete + "_" + m.Name)
			}
		}
	}
}

// coercionTraits returns the trait set of a recorded coercion, falling
// back to the single Trait field when Traits is unset.
func coercionTraits(dc checker.DynCoercion) []string {
	if len(dc.Traits) > 0 {
		return dc.Traits
	}
	return []string{dc.Trait}
}

// downcastRoots enqueues the impl methods referenced by the `(Trait, T)`
// vtable an `e as? T` downcast compares against. The compiled downcast
// (docs/DYN-TRAITS.md §9) emits `OpConstVtable{Trait, T}` and the backend
// materialises the `__vtable_<Trait>_<T>` cell, whose slots are `.quad
// __method_<T>_<m>` (natives) / function-table indices (wasm).
//
// A DOWNCAST-ONLY target — a `T` never coerced to `dyn Trait`, only
// downcast to — is absent from info.DynCoercions, so the coercion sites
// alone would miss it.
//
// Every trait in the set is rooted, not just the primary `dc.Trait`: the
// merged `(set, T)` vtable a multi-trait `dyn A + B` downcast compares
// against holds the concatenation of all the set's traits' methods over T
// (docs/DYN-TRAITS.md §10), so any one of them culled leaves a cell
// pointing at a dropped symbol. For a single-trait `dyn A` downcast
// dc.Traits is [A] and this is the same as rooting dc.Trait alone.
//
// Struct/enum targets only (the slice-1 downcast scope); collectVtables
// routes struct/enum slots at the real `__method_<T>_<m>`, so that is the
// name that must survive into codegen.
func downcastRoots(dc *ast.DowncastExpr, info *checker.Info, enqueue func(string)) {
	if dc == nil || dc.Trait == "" {
		return
	}
	concrete := ""
	switch t := dc.Target.(type) {
	case ast.StructType:
		concrete = t.Name
	case ast.EnumType:
		concrete = t.Name
	default:
		return
	}
	traits := dc.Traits
	if len(traits) == 0 {
		traits = []string{dc.Trait}
	}
	dynVtableRoots(info, traits, concrete, enqueue)
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
func enqueueKeyedMapDeps(method string, kType ast.Type, info *checker.Info, enqueue func(string)) {
	name := nominalKeyName(kType)
	if name == "" {
		return
	}
	enqueue(method + "_keyed")
	enqueue(implMethodName(info, name, "hash", "Hash"))
	enqueue(implMethodName(info, name, "eq", "Eq"))
}

// implMethodName is the mangled function a (type, method) pair resolves
// to, preferring the trait that declares it and falling back to the
// conventional receiver-hoist name. Mirrors the IR's realImplMethodName
// so tree-shake roots exactly the symbol codegen emits a call to.
func implMethodName(info *checker.Info, typeName, method, trait string) string {
	if fn, _, ok := info.ResolveMethod(typeName, method, []string{trait}); ok && fn != "" {
		return fn
	}
	return "__method_" + typeName + "_" + method
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
// callee. `info` resolves the trait-provided methods codegen
// emits calls to but no AST Call names; nil is tolerated (the
// conventional manglings are rooted instead).
func Run(prog *ast.Program, info *checker.Info, extras ...string) {
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
		walkStmt(fn.Body, byName, info, enqueue)
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

func walkStmt(s ast.Stmt, byName map[string]*ast.FuncDecl, info *checker.Info, enqueue func(string)) {
	switch x := s.(type) {
	case *ast.Block:
		for _, st := range x.Stmts {
			walkStmt(st, byName, info, enqueue)
		}
	case *ast.If:
		walkExpr(x.Cond, byName, info, enqueue)
		walkStmt(x.Then, byName, info, enqueue)
		if x.Else != nil {
			walkStmt(x.Else, byName, info, enqueue)
		}
	case *ast.While:
		walkExpr(x.Cond, byName, info, enqueue)
		walkStmt(x.Body, byName, info, enqueue)
	case *ast.Loop:
		walkStmt(x.Body, byName, info, enqueue)
	case *ast.For:
		if x.Init != nil {
			walkStmt(x.Init, byName, info, enqueue)
		}
		walkExpr(x.Cond, byName, info, enqueue)
		if x.Step != nil {
			walkStmt(x.Step, byName, info, enqueue)
		}
		walkStmt(x.Body, byName, info, enqueue)
	case *ast.Return:
		if x.Value != nil {
			walkExpr(x.Value, byName, info, enqueue)
		}
	case *ast.Var:
		walkExpr(x.Init, byName, info, enqueue)
	case *ast.Destructure:
		walkExpr(x.Init, byName, info, enqueue)
	case *ast.ExprStmt:
		walkExpr(x.Expr, byName, info, enqueue)
	case *ast.Match:
		walkExpr(x.Tag, byName, info, enqueue)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExpr(arm.Guard, byName, info, enqueue)
			}
			walkStmt(arm.Body, byName, info, enqueue)
		}
	case *ast.Defer:
		walkExpr(x.Expr, byName, info, enqueue)
	case *ast.FuncDecl:
		// Local FuncDecl (closure-converted) — its body is
		// reachable via the closure conversion that hoisted
		// it. Walk too.
		if x.Body != nil {
			walkStmt(x.Body, byName, info, enqueue)
		}
	}
}

func walkExpr(e ast.Expr, byName map[string]*ast.FuncDecl, info *checker.Info, enqueue func(string)) {
	if e == nil {
		return
	}
	// A `dyn Trait` coercion builds a vtable whose slots name impl
	// methods no call site mentions. Rooting them HERE — keyed on the
	// coercion expression the checker recorded — is what keeps the root
	// gated on the site being reachable: a coercion inside a function
	// nothing calls roots nothing, so the impl method and everything it
	// calls are pruned with it.
	if info != nil {
		if dc, ok := info.DynCoercions[e]; ok {
			dynVtableRoots(info, coercionTraits(dc), dc.Concrete, enqueue)
		}
	}
	switch x := e.(type) {
	case *ast.Ident:
		// Bare reference to a top-level function (function
		// value, address taken, or callee of a Call which
		// also lands here via Call.Callee).
		enqueue(x.Name)
	case *ast.Call:
		walkExpr(x.Callee, byName, info, enqueue)
		// Struct/enum (keyKind-3) map key: the IR routes this op to the
		// `_keyed` runtime variant (#2671), which dispatches through the
		// key type's derived hash/eq. Pull both the keyed impl alias and
		// those derived methods so codegen's emitted call resolves.
		if id, ok := x.Callee.(*ast.Ident); ok && keyedMapMethod(id.Name) && len(x.TypeArgs) >= 1 {
			enqueueKeyedMapDeps(id.Name, x.TypeArgs[0], info, enqueue)
		}
		for _, a := range x.Args {
			walkExpr(a, byName, info, enqueue)
		}
	case *ast.Binary:
		walkExpr(x.Left, byName, info, enqueue)
		walkExpr(x.Right, byName, info, enqueue)
	case *ast.Unary:
		walkExpr(x.Operand, byName, info, enqueue)
	case *ast.IfExpr:
		walkExpr(x.Cond, byName, info, enqueue)
		walkExpr(x.Then, byName, info, enqueue)
		walkExpr(x.Else, byName, info, enqueue)
	case *ast.TryOp:
		walkExpr(x.Inner, byName, info, enqueue)
	case *ast.MatchExpr:
		walkExpr(x.Tag, byName, info, enqueue)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExpr(arm.Guard, byName, info, enqueue)
			}
			walkExpr(arm.Body, byName, info, enqueue)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			walkStmt(st, byName, info, enqueue)
		}
		if x.Tail != nil {
			walkExpr(x.Tail, byName, info, enqueue)
		}
	case *ast.Assign:
		walkExpr(x.Target, byName, info, enqueue)
		walkExpr(x.Value, byName, info, enqueue)
	case *ast.Index:
		walkExpr(x.Array, byName, info, enqueue)
		walkExpr(x.Idx, byName, info, enqueue)
	case *ast.SliceExpr:
		walkExpr(x.Source, byName, info, enqueue)
		walkExpr(x.Low, byName, info, enqueue)
		walkExpr(x.High, byName, info, enqueue)
	case *ast.FieldAccess:
		walkExpr(x.Target, byName, info, enqueue)
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			walkExpr(el, byName, info, enqueue)
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			walkExpr(f.Value, byName, info, enqueue)
		}
		// Struct-update `Foo { ...base, field: v }`: the spread source is an
		// ordinary expression, so a call appearing ONLY there (`Foo { ...mk(),
		// … }`) is a real reference. Without this it looked unreachable and the
		// callee was pruned, leaving the emitted call site with an undefined
		// label at assembly time.
		if x.Base != nil {
			walkExpr(x.Base, byName, info, enqueue)
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
		enqueueKeyedMapDeps("__method_Map_set", x.KeyType, info, enqueue)
		for _, en := range x.Entries {
			walkExpr(en.Key, byName, info, enqueue)
			walkExpr(en.Value, byName, info, enqueue)
		}
	case *ast.TupleLit:
		for _, el := range x.Elems {
			walkExpr(el, byName, info, enqueue)
		}
	case *ast.EnumLit:
		for _, p := range x.Args {
			walkExpr(p, byName, info, enqueue)
		}
	case *ast.CastExpr:
		walkExpr(x.Inner, byName, info, enqueue)
	case *ast.DowncastExpr:
		downcastRoots(x, info, enqueue)
		walkExpr(x.Inner, byName, info, enqueue)
	case *ast.MakeClosure:
		// Closure formation references the hoisted body.
		enqueue(x.FuncName)
		for _, c := range x.Captures {
			walkExpr(c, byName, info, enqueue)
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
		walkStmt(x.Body, byName, info, enqueue)
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
				walkExpr(p.Expr, byName, info, enqueue)
			}
		}
		walkExpr(x.Desugared, byName, info, enqueue)
	}
}

// DropImplMethods names the `drop` method of every `core/mem.Drop` impl in
// the program, so tree-shake keeps it alive as a root.
//
// A `Drop` finalizer has no call site in the source at all — the only
// caller is the `__drop_struct_<C>` / `__drop_enum_<C>` glue, which IR
// lowering synthesises long after tree-shake has run. Without this root the
// method is pruned as unreachable and the natives fail to link on an
// undefined `__method_<C>_drop`.
//
// This is the one root that stays WHOLE-PROGRAM. A `dyn` coercion or an
// `as?` downcast is an expression the walk can reach, so those roots are
// gated on their site being live (dynVtableRoots / downcastRoots); a Drop
// impl has no site at all, and deciding whether the glue will exist means
// asking whether a live function can hold a `C` — type reachability this
// pass does not compute. So a `drop` body survives here even when nothing
// constructs a `C`. It costs no bytes: the IR-level dead-function pass runs
// after the glue is synthesised, sees a real call edge or none, and culls
// it precisely. What it does cost is `platforms.Enforce`, which reads this
// program as the artifact and so can still report an E066 for a capability
// only an unconstructed type's finalizer uses (#4114).
//
// Trait names in info.Impls are module-mangled (`mem__Drop`), so the match
// is on the simple name.
func DropImplMethods(info *checker.Info) []string {
	if info == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for trait, types := range info.Impls {
		simple := trait
		if i := strings.LastIndex(simple, "__"); i >= 0 {
			simple = simple[i+2:]
		}
		if simple != "Drop" {
			continue
		}
		for typeName, impld := range types {
			if !impld {
				continue
			}
			fn := implMethodName(info, typeName, "drop", trait)
			if !seen[fn] {
				seen[fn] = true
				out = append(out, fn)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Package treeshake removes unreferenced top-level functions
// from a checked + monomorphised program before codegen.
//
// The lang prelude (internal/prelude) injects helpers into
// every program, but most programs use only a small subset.
// Without tree-shaking, codegen would emit every prelude
// helper, blowing up binary size for trivial programs.
// Tree-shake makes the prelude effectively pay-for-what-you-
// use.
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
)

// watHelperDeps lists the prelude functions a still-in-wat
// helper depends on, plus aliases the codegen layer
// rewrites at emit-time. The AST walker doesn't see those
// rewrites, so tree-shake needs this hint to know that
// e.g. some still-in-wat helper calls a lang-prelude
// function and shouldn't drop the latter when only the
// former is referenced.
var watHelperDeps = map[string][]string{
	// arr.push(v) lowers entirely in the IR (emitArrayPush) —
	// no per-stride lang-prelude function to keep alive. The
	// wasm-side `__memcpy` shim is gated separately via the
	// codegen-side wat-helper switch.
	//
	// Map runtime: AST-level calls go through the
	// type-rich `__method_Map_*` / `map_new` /
	// `__method_MapIter_*` names; the prelude bodies live
	// under `_impl` suffixes that the codegen alias rewrites
	// to. Pull each impl in when its alias is referenced.
	"map_new":                 {"map_new_impl"},
	"__method_Map_len":        {"__map_len_impl"},
	"__method_Map_has":        {"__map_has_impl", "__map_lookup", "__map_hash"},
	"__method_Map_get":        {"__map_get_impl", "__map_lookup", "__map_hash"},
	"__method_Map_get_or":     {"__map_get_or_impl", "__map_lookup", "__map_hash"},
	"__method_Map_set":        {"__map_set_impl", "__map_grow", "__map_hash"},
	"__method_Map_delete":     {"__map_delete_impl", "__map_hash"},
	"__method_Map_clear":      {"__map_clear_impl"},
	"__method_Map_keys":       {"__map_keys_impl", "__map_column"},
	"__method_Map_values":     {"__map_values_impl", "__map_column"},
	"__method_Map_iter":       {"__map_iter_impl"},
	"__method_MapIter_has_next": {"__mapiter_has_next_impl"},
	"__method_MapIter_key":      {"__mapiter_key_impl", "__mapiter_entry_addr"},
	"__method_MapIter_value":    {"__mapiter_value_impl", "__mapiter_entry_addr"},
	"__method_MapIter_advance":  {"__mapiter_advance_impl"},
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
	// declared lang-prelude dependencies.
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
	// declared (non-prelude) function — covers test programs
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
			if !fn.IsPrelude {
				enqueue(fn.Name)
			}
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
	case *ast.IfLet:
		walkExpr(x.Source, byName, enqueue)
		walkStmt(x.Then, byName, enqueue)
		if x.Else != nil {
			walkStmt(x.Else, byName, enqueue)
		}
	case *ast.LetElse:
		walkExpr(x.Source, byName, enqueue)
		walkStmt(x.Else, byName, enqueue)
	case *ast.While:
		walkExpr(x.Cond, byName, enqueue)
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
	case *ast.Switch:
		walkExpr(x.Tag, byName, enqueue)
		for _, k := range x.Cases {
			for _, v := range k.Values {
				walkExpr(v, byName, enqueue)
			}
			walkStmt(k.Body, byName, enqueue)
		}
		if x.Default != nil {
			walkStmt(x.Default, byName, enqueue)
		}
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
	case *ast.Arena:
		walkStmt(x.Body, byName, enqueue)
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
	case *ast.MapLit:
		// IR lowers `Map { ... }` to map_new + a chain of
		// __method_Map_set calls — pull both alias names so
		// the codegen-emitted impls stay alive even when no
		// AST Call references them directly.
		enqueue("map_new")
		enqueue("__method_Map_set")
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
		// which is what keeps the prelude's `(s: string)
		// to_string()` body alive.
		for _, p := range x.Parts {
			if p.Expr != nil {
				walkExpr(p.Expr, byName, enqueue)
			}
		}
		walkExpr(x.Desugared, byName, enqueue)
	}
}

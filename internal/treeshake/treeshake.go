// Package treeshake removes unreferenced top-level functions
// from a checked + monomorphised program before codegen.
//
// The lang prelude (internal/prelude) injects helpers into
// every program, but most programs use only a small subset.
// Without tree-shaking, codegen would emit (and arm32 would
// fail to lower) every prelude helper — including ones that
// rely on i64 ops that the arm32 backend doesn't yet
// support. Tree-shake makes the prelude effectively
// pay-for-what-you-use.
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

// Run mutates `prog.Funcs` to retain only functions reachable
// from the program's entry points. Function-typed values
// (e.g. `var f = some_func; ... f();`) keep `some_func` alive
// since the Ident reference appears in the body of the
// containing function.
func Run(prog *ast.Program) {
	if len(prog.Funcs) == 0 {
		return
	}
	byName := map[string]*ast.FuncDecl{}
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	reachable := map[string]bool{}
	var queue []string
	enqueue := func(name string) {
		if name == "" || reachable[name] {
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
	case *ast.Ternary:
		walkExpr(x.Cond, byName, enqueue)
		walkExpr(x.Then, byName, enqueue)
		walkExpr(x.Else, byName, enqueue)
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
	case *ast.CaptureRef:
		// CaptureRef targets a synthesised env variable; no
		// direct function reference.
	}
}

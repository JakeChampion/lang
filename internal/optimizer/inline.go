package optimizer

import "github.com/jakechampion/lang/internal/ast"

// Inline replaces calls to "small" pure functions with copies of the
// callee's body, with parameters substituted by the call's arguments.
//
// Eligibility (deliberately conservative):
//
//   - the callee consists of exactly one statement, `return EXPR;`,
//   - EXPR contains no nested function call (so inlining never
//     duplicates side effects or recurses indefinitely),
//   - each call site's argument list contains only identifiers or
//     literals, so substituting a parameter referenced multiple times
//     is safe.
//
// The pass is idempotent — programs without inline candidates are
// unchanged in O(N) AST-walk time.
func Inline(prog *ast.Program) {
	candidates := findInlineable(prog)
	if len(candidates) == 0 {
		return
	}
	for _, fn := range prog.Funcs {
		inlineBlock(fn.Body, candidates)
	}
}

type inlineEntry struct {
	params []ast.Param
	body   ast.Expr
}

func findInlineable(prog *ast.Program) map[string]inlineEntry {
	out := map[string]inlineEntry{}
	for _, fn := range prog.Funcs {
		if len(fn.Body.Stmts) != 1 {
			continue
		}
		ret, ok := fn.Body.Stmts[0].(*ast.Return)
		if !ok || ret.Value == nil {
			continue
		}
		if exprHasCall(ret.Value) {
			continue
		}
		out[fn.Name] = inlineEntry{params: fn.Params, body: ret.Value}
	}
	return out
}

func exprHasCall(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Call:
		return true
	case *ast.Binary:
		return exprHasCall(x.Left) || exprHasCall(x.Right)
	case *ast.Unary:
		return exprHasCall(x.Operand)
	case *ast.Index:
		return exprHasCall(x.Array) || exprHasCall(x.Idx)
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			if exprHasCall(el) {
				return true
			}
		}
	case *ast.Assign:
		return exprHasCall(x.Target) || exprHasCall(x.Value)
	}
	return false
}

func inlineBlock(b *ast.Block, c map[string]inlineEntry) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		inlineStmt(s, c)
	}
}

func inlineStmt(s ast.Stmt, c map[string]inlineEntry) {
	switch x := s.(type) {
	case *ast.Block:
		inlineBlock(x, c)
	case *ast.If:
		x.Cond = inlineExpr(x.Cond, c)
		inlineStmt(x.Then, c)
		if x.Else != nil {
			inlineStmt(x.Else, c)
		}
	case *ast.While:
		x.Cond = inlineExpr(x.Cond, c)
		inlineStmt(x.Body, c)
	case *ast.For:
		if x.Init != nil {
			inlineStmt(x.Init, c)
		}
		x.Cond = inlineExpr(x.Cond, c)
		if x.Step != nil {
			inlineStmt(x.Step, c)
		}
		inlineStmt(x.Body, c)
	case *ast.Return:
		if x.Value != nil {
			x.Value = inlineExpr(x.Value, c)
		}
	case *ast.Var:
		x.Init = inlineExpr(x.Init, c)
	case *ast.ExprStmt:
		x.Expr = inlineExpr(x.Expr, c)
	}
}

func inlineExpr(e ast.Expr, c map[string]inlineEntry) ast.Expr {
	switch x := e.(type) {
	case *ast.Binary:
		x.Left = inlineExpr(x.Left, c)
		x.Right = inlineExpr(x.Right, c)
	case *ast.Unary:
		x.Operand = inlineExpr(x.Operand, c)
	case *ast.Index:
		x.Array = inlineExpr(x.Array, c)
		x.Idx = inlineExpr(x.Idx, c)
	case *ast.ArrayLit:
		for i := range x.Elems {
			x.Elems[i] = inlineExpr(x.Elems[i], c)
		}
	case *ast.Assign:
		x.Target = inlineExpr(x.Target, c)
		x.Value = inlineExpr(x.Value, c)
	case *ast.Call:
		for i := range x.Args {
			x.Args[i] = inlineExpr(x.Args[i], c)
		}
		if id, ok := x.Callee.(*ast.Ident); ok {
			if entry, ok := c[id.Name]; ok &&
				len(x.Args) == len(entry.params) &&
				allArgsSimple(x.Args) {
				return substitute(entry.body, entry.params, x.Args)
			}
		}
	}
	return e
}

func allArgsSimple(args []ast.Expr) bool {
	for _, a := range args {
		switch a.(type) {
		case *ast.Ident, *ast.NumberLit, *ast.BoolLit, *ast.StringLit:
		default:
			return false
		}
	}
	return true
}

// substitute returns a fresh copy of body with each Ident matching a
// parameter name replaced by the corresponding argument. Cloning at
// every binary / unary / index / array-lit boundary is what makes
// multi-call-site inlining safe — without it, two call sites would
// share the same Binary node and a later optimisation could mutate
// one and break the other.
func substitute(body ast.Expr, params []ast.Param, args []ast.Expr) ast.Expr {
	switch x := body.(type) {
	case *ast.Ident:
		for i, p := range params {
			if x.Name == p.Name {
				return args[i]
			}
		}
		return x
	case *ast.Binary:
		return &ast.Binary{
			P:              x.P,
			Op:             x.Op,
			IsStringConcat: x.IsStringConcat,
			Left:           substitute(x.Left, params, args),
			Right:          substitute(x.Right, params, args),
		}
	case *ast.Unary:
		return &ast.Unary{
			P:       x.P,
			Op:      x.Op,
			Operand: substitute(x.Operand, params, args),
		}
	case *ast.Index:
		return &ast.Index{
			P:     x.P,
			Array: substitute(x.Array, params, args),
			Idx:   substitute(x.Idx, params, args),
		}
	case *ast.ArrayLit:
		elems := make([]ast.Expr, len(x.Elems))
		for i, el := range x.Elems {
			elems[i] = substitute(el, params, args)
		}
		return &ast.ArrayLit{P: x.P, Elems: elems}
	}
	// Leaves (NumberLit, BoolLit, StringLit) — return as-is. They're
	// read-only data; aliasing across call sites is safe.
	return body
}

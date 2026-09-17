package checker

import "github.com/jakechampion/lang/internal/ast"

// computeResultBorrows settles, for every function with a body, whether its
// result can alias one of its BORROWED pointer parameters. That is the property
// E051 needs at a call site: a result that cannot be such a borrow is freshly
// owned and may be transferred into an `own` parameter, however the callee's
// parameter list looks (#9538).
//
// Returning a local is not a borrow. A return moves it, so nothing the caller
// holds aliases it afterwards; only a borrowed parameter has an owner that
// outlives the call.
//
// Least fixed point from "nothing borrows". A function is marked when one of
// its returns is derived from a borrowed parameter, and marking one can only
// ever mark its callers, so iterating until nothing changes settles. A
// recursive cycle that returns no borrow is therefore never marked, and a
// function whose body is not here is not resolved by this walk at all —
// calleeResultBorrows falls back to the signature rule for it.
func (c *checker) computeResultBorrows(prog *ast.Program) {
	c.resultBorrows = make(map[string]bool, len(prog.Funcs))
	c.resultAnalysed = make(map[string]bool, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		if fn.Body != nil {
			c.resultAnalysed[fn.Name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range prog.Funcs {
			if fn.Body == nil || c.resultBorrows[fn.Name] {
				continue
			}
			if c.funcResultBorrows(fn) {
				c.resultBorrows[fn.Name] = true
				changed = true
			}
		}
	}
}

// funcResultBorrows reports whether any of fn's returns is derived from one of
// its borrowed pointer parameters.
func (c *checker) funcResultBorrows(fn *ast.FuncDecl) bool {
	borrowed := map[string]bool{}
	for _, p := range fn.Params {
		if !p.Own && p.Type != nil && ast.IsPointerType(p.Type) {
			borrowed[p.Name] = true
		}
	}
	if len(borrowed) == 0 {
		return false
	}
	locals := c.borrowedLocals(fn, borrowed)
	found := false
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.Lambda:
			// A lambda's returns are its own, not this function's.
			return false
		case *ast.Return:
			if s.Value != nil && c.exprBorrows(s.Value, borrowed, locals) {
				found = true
			}
		}
		return true
	})
	return found
}

// borrowedLocals names the locals of fn that can hold a borrow of one of its
// borrowed parameters. Settled to a fixed point rather than walked once, so a
// value that only becomes a borrow on a loop's second iteration is still seen.
func (c *checker) borrowedLocals(fn *ast.FuncDecl, borrowed map[string]bool) map[string]bool {
	locals := map[string]bool{}
	mark := func(name string, init ast.Expr) bool {
		if name == "" || locals[name] || init == nil {
			return false
		}
		if !c.exprBorrows(init, borrowed, locals) {
			return false
		}
		locals[name] = true
		return true
	}
	for changed := true; changed; {
		changed = false
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.Lambda:
				return false
			case *ast.Var:
				changed = mark(s.Name, s.Init) || changed
			case *ast.Assign:
				if id, ok := s.Target.(*ast.Ident); ok {
					changed = mark(id.Name, s.Value) || changed
				}
			}
			return true
		})
	}
	return locals
}

// exprBorrows reports whether e can alias one of the borrowed names.
// Pessimistic: a shape not named here is assumed to be able to.
func (c *checker) exprBorrows(e ast.Expr, borrowed, locals map[string]bool) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ast.NumberLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.UnitLit,
		*ast.StructLit, *ast.TupleLit, *ast.ArrayLit, *ast.MapLit, *ast.EnumLit,
		*ast.Binary, *ast.Unary:
		// A construction is fresh: it copies references into a new value rather
		// than being one.
		return false
	case *ast.Ident:
		return borrowed[x.Name] || locals[x.Name]
	case *ast.FieldAccess:
		return c.exprBorrows(x.Target, borrowed, locals)
	case *ast.Index:
		return c.exprBorrows(x.Array, borrowed, locals)
	case *ast.SliceExpr:
		return c.exprBorrows(x.Source, borrowed, locals)
	case *ast.IfExpr:
		return c.exprBorrows(x.Then, borrowed, locals) || c.exprBorrows(x.Else, borrowed, locals)
	case *ast.Call:
		// A callee that can hand a borrowed parameter back aliases whichever
		// argument it was given, so its result is a borrow HERE only when one
		// of those arguments is one.
		if !c.calleeResultBorrows(x) {
			return false
		}
		for _, a := range x.Args {
			if c.exprBorrows(a, borrowed, locals) {
				return true
			}
		}
		return false
	}
	return true
}

// calleeResultBorrows reports whether the call's result can alias one of the
// arguments the caller passed.
func (c *checker) calleeResultBorrows(x *ast.Call) bool {
	id, ok := x.Callee.(*ast.Ident)
	if !ok {
		return true
	}
	if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
		return false
	}
	if c.resultAnalysed[id.Name] {
		return c.resultBorrows[id.Name]
	}
	return c.signatureResultBorrows(id.Name)
}

// signatureResultBorrows is the approximation used where no body is available:
// a callee with a borrowed pointer parameter could hand that same pointer back.
func (c *checker) signatureResultBorrows(name string) bool {
	sig, ok := c.info.FuncSigs[name]
	if !ok {
		return true
	}
	flags := c.ownFuncs[name]
	for i, pt := range sig.Params {
		if pt == nil || !ast.IsPointerType(pt) {
			continue
		}
		if i >= len(flags) || !flags[i] {
			return true
		}
	}
	return false
}

// isOwnedScrutinee answers a DIFFERENT question from an argument transfer, and
// deliberately keeps the narrower pre-#9538 answer for a call.
//
// Matching decomposes the box in place, and the RC lowering releases what it
// decomposes off the same signature-shaped rule the checker used before the
// returns-borrow inference. Handing the inference's wider answer to this site
// frees a freshly-returned scrutinee AT the match and then reads it: the shape
// in std/json's json_get_object underflows its refcount (#9539). The two sites
// converge when the lowering moves with the rule, not before.
func (c *checker) isOwnedScrutinee(tag ast.Expr, inferred func(ast.Expr) bool) bool {
	if call, ok := tag.(*ast.Call); ok {
		id, isIdent := call.Callee.(*ast.Ident)
		if !isIdent {
			return false
		}
		if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
			return true
		}
		sig, known := c.info.FuncSigs[id.Name]
		if !known || sig.Result == nil || !ast.IsPointerType(sig.Result) {
			return false
		}
		return !c.signatureResultBorrows(id.Name)
	}
	return inferred(tag)
}

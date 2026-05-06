// Package optimizer performs simple AST-level cleanup before codegen.
//
// It does two things:
//
//   - constant folding: arithmetic, comparison, logical, bitwise, shift
//     operators applied to literal operands are evaluated at compile time;
//     `+` between two string literals is concatenated;
//   - dead-code elimination: statements after a `return`, `break` or
//     `continue` in the same block are dropped, and `if (true)` /
//     `if (false)` collapse to the live branch (the dead branch is
//     discarded).
//
// The transform is purely structural — it runs after the type checker, so
// every Binary / Unary / Call already has a known type and the existing
// IsStringConcat flag survives folding.
package optimizer

import "github.com/jakechampion/lang/internal/ast"

// Optimize folds constants and trims unreachable statements in every
// function of prog. It mutates the AST in place.
func Optimize(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		fn.Body = foldBlock(fn.Body)
	}
}

func foldBlock(b *ast.Block) *ast.Block {
	if b == nil {
		return nil
	}
	out := b.Stmts[:0]
	for _, s := range b.Stmts {
		s = foldStmt(s)
		if s == nil {
			continue
		}
		out = append(out, s)
		if terminates(s) {
			break // anything after an unconditional exit is unreachable
		}
	}
	b.Stmts = out
	return b
}

func foldStmt(s ast.Stmt) ast.Stmt {
	switch x := s.(type) {
	case *ast.Block:
		return foldBlock(x)
	case *ast.If:
		x.Cond = foldExpr(x.Cond)
		x.Then = foldStmt(x.Then)
		if x.Else != nil {
			x.Else = foldStmt(x.Else)
		}
		// if (true)  => then, if (false) => else (or empty block)
		if bl, ok := x.Cond.(*ast.BoolLit); ok {
			if bl.Value {
				return x.Then
			}
			if x.Else != nil {
				return x.Else
			}
			return &ast.Block{P: x.P}
		}
		return x
	case *ast.While:
		x.Cond = foldExpr(x.Cond)
		// while (false) ... : drop the whole loop.
		if bl, ok := x.Cond.(*ast.BoolLit); ok && !bl.Value {
			return nil
		}
		x.Body = foldStmt(x.Body)
		return x
	case *ast.For:
		if x.Init != nil {
			x.Init = foldStmt(x.Init)
		}
		x.Cond = foldExpr(x.Cond)
		if x.Step != nil {
			x.Step = foldStmt(x.Step)
		}
		x.Body = foldStmt(x.Body)
		return x
	case *ast.Return:
		if x.Value != nil {
			x.Value = foldExpr(x.Value)
		}
		return x
	case *ast.Var:
		x.Init = foldExpr(x.Init)
		return x
	case *ast.ExprStmt:
		x.Expr = foldExpr(x.Expr)
		return x
	}
	return s
}

// terminates reports whether s unconditionally exits the enclosing
// block (so anything after it is dead).
func terminates(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		return true
	case *ast.Block:
		if len(x.Stmts) == 0 {
			return false
		}
		return terminates(x.Stmts[len(x.Stmts)-1])
	case *ast.If:
		return x.Else != nil && terminates(x.Then) && terminates(x.Else)
	}
	return false
}

func foldExpr(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case *ast.Binary:
		x.Left = foldExpr(x.Left)
		x.Right = foldExpr(x.Right)
		return tryFoldBinary(x)
	case *ast.Unary:
		x.Operand = foldExpr(x.Operand)
		return tryFoldUnary(x)
	case *ast.Assign:
		x.Target = foldExpr(x.Target)
		x.Value = foldExpr(x.Value)
		return x
	case *ast.Call:
		x.Callee = foldExpr(x.Callee)
		for i, a := range x.Args {
			x.Args[i] = foldExpr(a)
		}
		return x
	case *ast.Index:
		x.Array = foldExpr(x.Array)
		x.Idx = foldExpr(x.Idx)
		return x
	case *ast.ArrayLit:
		for i, el := range x.Elems {
			x.Elems[i] = foldExpr(el)
		}
		return x
	}
	return e
}

func tryFoldBinary(b *ast.Binary) ast.Expr {
	// "a" + "b" → "ab" (avoids the runtime helper entirely when both
	// operands are known constants).
	if b.IsStringConcat {
		l, lOk := b.Left.(*ast.StringLit)
		r, rOk := b.Right.(*ast.StringLit)
		if lOk && rOk {
			return &ast.StringLit{P: b.P, Value: l.Value + r.Value}
		}
		return b
	}
	if ln, lOk := b.Left.(*ast.NumberLit); lOk {
		if rn, rOk := b.Right.(*ast.NumberLit); rOk {
			switch b.Op {
			case "+":
				return &ast.NumberLit{P: b.P, Value: ln.Value + rn.Value}
			case "-":
				return &ast.NumberLit{P: b.P, Value: ln.Value - rn.Value}
			case "*":
				return &ast.NumberLit{P: b.P, Value: ln.Value * rn.Value}
			case "/":
				if rn.Value != 0 {
					return &ast.NumberLit{P: b.P, Value: ln.Value / rn.Value}
				}
			case "%":
				if rn.Value != 0 {
					return &ast.NumberLit{P: b.P, Value: ln.Value % rn.Value}
				}
			case "&":
				return &ast.NumberLit{P: b.P, Value: ln.Value & rn.Value}
			case "|":
				return &ast.NumberLit{P: b.P, Value: ln.Value | rn.Value}
			case "^":
				return &ast.NumberLit{P: b.P, Value: ln.Value ^ rn.Value}
			case "<<":
				if rn.Value >= 0 && rn.Value < 64 {
					return &ast.NumberLit{P: b.P, Value: ln.Value << rn.Value}
				}
			case ">>":
				if rn.Value >= 0 && rn.Value < 64 {
					return &ast.NumberLit{P: b.P, Value: ln.Value >> rn.Value}
				}
			case "==":
				return &ast.BoolLit{P: b.P, Value: ln.Value == rn.Value}
			case "!=":
				return &ast.BoolLit{P: b.P, Value: ln.Value != rn.Value}
			case "<":
				return &ast.BoolLit{P: b.P, Value: ln.Value < rn.Value}
			case "<=":
				return &ast.BoolLit{P: b.P, Value: ln.Value <= rn.Value}
			case ">":
				return &ast.BoolLit{P: b.P, Value: ln.Value > rn.Value}
			case ">=":
				return &ast.BoolLit{P: b.P, Value: ln.Value >= rn.Value}
			}
		}
	}
	if lb, lOk := b.Left.(*ast.BoolLit); lOk {
		if rb, rOk := b.Right.(*ast.BoolLit); rOk {
			switch b.Op {
			case "&&":
				return &ast.BoolLit{P: b.P, Value: lb.Value && rb.Value}
			case "||":
				return &ast.BoolLit{P: b.P, Value: lb.Value || rb.Value}
			case "==":
				return &ast.BoolLit{P: b.P, Value: lb.Value == rb.Value}
			case "!=":
				return &ast.BoolLit{P: b.P, Value: lb.Value != rb.Value}
			}
		}
		// Short-circuit identities even when only the left is a literal.
		switch b.Op {
		case "&&":
			if lb.Value {
				return b.Right
			}
			return &ast.BoolLit{P: b.P, Value: false}
		case "||":
			if lb.Value {
				return &ast.BoolLit{P: b.P, Value: true}
			}
			return b.Right
		}
	}
	return b
}

func tryFoldUnary(u *ast.Unary) ast.Expr {
	switch u.Op {
	case "-":
		if n, ok := u.Operand.(*ast.NumberLit); ok {
			return &ast.NumberLit{P: u.P, Value: -n.Value}
		}
	case "!":
		if bl, ok := u.Operand.(*ast.BoolLit); ok {
			return &ast.BoolLit{P: u.P, Value: !bl.Value}
		}
	}
	return u
}

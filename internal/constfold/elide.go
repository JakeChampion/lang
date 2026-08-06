package constfold

import "github.com/jakechampion/lang/internal/ast"

// ElideAsserts removes every `assert(...)` check from the program — the
// `-O` release-build behaviour (#4416). The parser desugars an assert to
// an `ast.If` carrying IsAssert, so the pass filters those out of every
// statement list, recursing through nested blocks, lambdas, block
// expressions, and match arms. Run it AFTER checker.Check so an
// ill-typed assert still fails the build under -O; the elision removes
// the condition evaluation along with the check, which is why asserts
// must be side-effect-free.
func ElideAsserts(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		elideBlock(fn.Body)
	}
}

func elideBlock(b *ast.Block) {
	if b == nil {
		return
	}
	kept := b.Stmts[:0]
	for _, st := range b.Stmts {
		if ifs, ok := st.(*ast.If); ok && ifs.IsAssert {
			continue
		}
		elideStmt(st)
		kept = append(kept, st)
	}
	b.Stmts = kept
}

func elideStmt(st ast.Stmt) {
	switch x := st.(type) {
	case *ast.Block:
		elideBlock(x)
	case *ast.If:
		elideExpr(x.Cond)
		elideStmt(x.Then)
		if x.Else != nil {
			elideStmt(x.Else)
		}
	case *ast.While:
		elideExpr(x.Cond)
		elideStmt(x.Body)
	case *ast.Loop:
		elideStmt(x.Body)
	case *ast.For:
		if x.Init != nil {
			elideStmt(x.Init)
		}
		elideExpr(x.Cond)
		if x.Step != nil {
			elideStmt(x.Step)
		}
		elideStmt(x.Body)
	case *ast.ForEach:
		elideExpr(x.Iter)
		elideStmt(x.Body)
	case *ast.Match:
		elideExpr(x.Tag)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				elideExpr(arm.Guard)
			}
			elideBlock(arm.Body)
		}
	case *ast.Return:
		if x.Value != nil {
			elideExpr(x.Value)
		}
	case *ast.Var:
		elideExpr(x.Init)
	case *ast.Destructure:
		elideExpr(x.Init)
	case *ast.ExprStmt:
		elideExpr(x.Expr)
	case *ast.Defer:
		elideExpr(x.Expr)
	case *ast.FuncDecl:
		elideBlock(x.Body)
	}
}

func elideExpr(e ast.Expr) {
	switch x := e.(type) {
	case *ast.Call:
		elideExpr(x.Callee)
		for i := range x.Args {
			elideExpr(x.Args[i])
		}
	case *ast.Binary:
		elideExpr(x.Left)
		elideExpr(x.Right)
	case *ast.Unary:
		elideExpr(x.Operand)
	case *ast.Index:
		elideExpr(x.Array)
		elideExpr(x.Idx)
	case *ast.ArrayLit:
		for i := range x.Elems {
			elideExpr(x.Elems[i])
		}
	case *ast.Assign:
		elideExpr(x.Target)
		elideExpr(x.Value)
	case *ast.IfExpr:
		elideExpr(x.Cond)
		elideExpr(x.Then)
		elideExpr(x.Else)
	case *ast.TryOp:
		elideExpr(x.Inner)
	case *ast.MatchExpr:
		elideExpr(x.Tag)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				elideExpr(arm.Guard)
			}
			elideExpr(arm.Body)
		}
	case *ast.BlockExpr:
		kept := x.Stmts[:0]
		for _, st := range x.Stmts {
			if ifs, ok := st.(*ast.If); ok && ifs.IsAssert {
				continue
			}
			elideStmt(st)
			kept = append(kept, st)
		}
		x.Stmts = kept
		if x.Tail != nil {
			elideExpr(x.Tail)
		}
	case *ast.StructLit:
		for i := range x.Fields {
			elideExpr(x.Fields[i].Value)
		}
	case *ast.FieldAccess:
		elideExpr(x.Target)
	case *ast.Lambda:
		elideBlock(x.Body)
	}
}

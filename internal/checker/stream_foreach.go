package checker

import "github.com/jakechampion/lang/internal/ast"

// lowerStreamForEachProgram replaces every surviving `ast.ForEach` (a lazy
// u8-stream iterand the parser deliberately left un-lowered, see
// docs/STREAM-TYPE-SURFACE.md, L2) with its per-element read-loop expansion
// (ast.DesugarForEachStream). It runs in the checker — after modload has mangled
// cross-module call sites — so the synthesised `f$open` callee tracks the
// (possibly mangled) import name. `streamElem` maps each u8 stream-import name to
// its element type; a ForEach whose iterand is a direct call to one of those
// names is a lazy stream loop. Mirrors the parser's desugarForEach* traversal
// (functions, trait default methods, const initialisers, and for-in nested in
// block-expressions / lambda bodies), but lowers only the stream form.
func lowerStreamForEachProgram(prog *ast.Program, streamElem map[string]ast.Type) {
	for _, fn := range prog.Funcs {
		if fn.Body != nil {
			lowerStreamForEachStmt(fn.Body, streamElem)
		}
	}
	for _, tr := range prog.Traits {
		for i := range tr.Methods {
			if tr.Methods[i].Body != nil {
				lowerStreamForEachStmt(tr.Methods[i].Body, streamElem)
			}
		}
	}
	for _, cn := range prog.Consts {
		lowerStreamForEachExpr(cn.Value, streamElem)
	}
}

// streamForEachElem returns the element type for a ForEach whose iterand is a
// direct call `f(args)` to a u8 stream import in streamElem, or nil otherwise.
func streamForEachElem(fe *ast.ForEach, streamElem map[string]ast.Type) ast.Type {
	call, ok := fe.Iter.(*ast.Call)
	if !ok {
		return nil
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return nil
	}
	return streamElem[id.Name]
}

// lowerStreamForEachStmt recursively lowers stream ForEach nodes reachable from
// s, returning the replacement for s (mutating *ast.Block-typed fields in place,
// returning the swap for plain ast.Stmt fields). Mirrors the parser's
// desugarForEachStmt structure exactly so no statement-containing node is missed.
func lowerStreamForEachStmt(s ast.Stmt, streamElem map[string]ast.Type) ast.Stmt {
	switch x := s.(type) {
	case nil:
		return nil
	case *ast.ForEach:
		x.Body = lowerStreamForEachStmt(x.Body, streamElem)
		lowerStreamForEachExpr(x.Iter, streamElem)
		if elem := streamForEachElem(x, streamElem); elem != nil {
			return ast.DesugarForEachStream(x, elem)
		}
		return x
	case *ast.Block:
		for i := range x.Stmts {
			x.Stmts[i] = lowerStreamForEachStmt(x.Stmts[i], streamElem)
		}
	case *ast.If:
		lowerStreamForEachExpr(x.Cond, streamElem)
		x.Then = lowerStreamForEachStmt(x.Then, streamElem)
		if x.Else != nil {
			x.Else = lowerStreamForEachStmt(x.Else, streamElem)
		}
	case *ast.While:
		lowerStreamForEachExpr(x.Cond, streamElem)
		x.Body = lowerStreamForEachStmt(x.Body, streamElem)
	case *ast.Loop:
		x.Body = lowerStreamForEachStmt(x.Body, streamElem)
	case *ast.For:
		if x.Init != nil {
			x.Init = lowerStreamForEachStmt(x.Init, streamElem)
		}
		lowerStreamForEachExpr(x.Cond, streamElem)
		if x.Step != nil {
			x.Step = lowerStreamForEachStmt(x.Step, streamElem)
		}
		x.Body = lowerStreamForEachStmt(x.Body, streamElem)
	case *ast.Match:
		lowerStreamForEachExpr(x.Tag, streamElem)
		for _, arm := range x.Arms {
			lowerStreamForEachStmt(arm.Body, streamElem)
		}
	case *ast.Var:
		lowerStreamForEachExpr(x.Init, streamElem)
	case *ast.ExprStmt:
		lowerStreamForEachExpr(x.Expr, streamElem)
	case *ast.Return:
		lowerStreamForEachExpr(x.Value, streamElem)
	case *ast.Destructure:
		lowerStreamForEachExpr(x.Init, streamElem)
	}
	return s
}

// lowerStreamForEachExpr lowers stream for-in nested inside a block-expression or
// lambda body within an expression tree.
func lowerStreamForEachExpr(e ast.Expr, streamElem map[string]ast.Type) {
	if e == nil {
		return
	}
	ast.Walk(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BlockExpr:
			for i := range x.Stmts {
				x.Stmts[i] = lowerStreamForEachStmt(x.Stmts[i], streamElem)
			}
		case *ast.Lambda:
			lowerStreamForEachStmt(x.Body, streamElem)
		}
		return true
	})
}

package closureconv

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// BoxMutatedScalarCaptures rewrites captured-and-mutated scalar locals
// (i32-family / bool / f64) into 1-element heap array cells so a closure's
// write to a captured outer scalar is shared by reference — matching the
// interpreter, which defines closures-as-counters semantics (mutable scalar
// captures by reference; #2896). The native pipeline otherwise captures every
// scalar BY VALUE, so a closure's `x = 42` was lost.
//
// For each top-level function that has at least one such capture, the boxed
// local's `var x = E` decl becomes `var x: T[] = [E]`, every read/write of `x`
// (in the function AND inside the closures) becomes `x[0]` / `x[0] = …`, and
// every closure's capture entry for `x` is re-typed `T[]` so the closure pass
// captures the cell POINTER (by reference) instead of the scalar value.
//
// Ordering: runs after the checker (so E049's pointer-capture-write-back rule
// validated the ORIGINAL scalar program — the synthetic `T[]` cell is never
// type-checked) and immediately before ConvertWith (so the closure lift sees
// the boxed cell). It must run after shadowrename so every local name is
// unique — a closure body's reference to a boxed name then unambiguously names
// the captured outer cell, never a same-named inner local. Functions with no
// captured-and-mutated scalar are left byte-identical.
func BoxMutatedScalarCaptures(prog *ast.Program, info *checker.Info) {
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		boxed := collectBoxedScalars(fn.Body)
		if len(boxed) == 0 {
			continue
		}
		boxDecls(fn.Body, boxed, info)
		rewriteBoxedBlock(fn.Body, boxed)
		flipCaptures(fn.Body, boxed)
	}
}

// boxableScalar reports whether a captured scalar's static type is one we box
// into a 1-element cell: any fixed-width integer, a boolean, or a float. Pointer
// captures (string / array / struct / …) stay read-only by E049 and are not
// boxed; types in this AST are value-typed, so the cases match on value kinds.
func boxableScalar(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType:
		return true
	}
	return false
}

// closureParts returns a closure node's captures + body block, or (nil, nil)
// when n is not a closure. The two closure forms — an anonymous `*ast.Lambda`
// and a locally-declared `*ast.FuncDecl` — both carry checker-stamped Captures.
func closureParts(n ast.Node) ([]ast.Param, *ast.Block) {
	switch x := n.(type) {
	case *ast.Lambda:
		return x.Captures, x.Body
	case *ast.FuncDecl:
		if x.IsLocal {
			return x.Captures, x.Body
		}
	}
	return nil, nil
}

// collectBoxedScalars finds the boxed set for one function body: a name maps to
// its scalar element type iff some closure both captures it (as a boxable
// scalar) and writes to it. Returns nil when there is nothing to box.
func collectBoxedScalars(body *ast.Block) map[string]ast.Type {
	var boxed map[string]ast.Type
	forEachClosure(body, func(caps []ast.Param, cbody *ast.Block) {
		if cbody == nil || len(caps) == 0 {
			return
		}
		writes := assignTargetNames(cbody)
		for _, cap := range caps {
			if !writes[cap.Name] || !boxableScalar(cap.Type) {
				continue
			}
			if boxed == nil {
				boxed = map[string]ast.Type{}
			}
			boxed[cap.Name] = cap.Type
		}
	})
	return boxed
}

// assignTargetNames collects the names that appear as a bare-identifier
// assignment target anywhere in body (compound `x += y` already desugars to
// `x = x + y`, so `*ast.Assign` is the only mutation form to scan). It descends
// into nested closures via closureBlocks so a write inside an inner closure is
// still attributed.
func assignTargetNames(body *ast.Block) map[string]bool {
	names := map[string]bool{}
	for _, b := range closureBlocks(body) {
		ast.Walk(b, func(n ast.Node) bool {
			if a, ok := n.(*ast.Assign); ok {
				if id, ok := a.Target.(*ast.Ident); ok {
					names[id.Name] = true
				}
			}
			// Don't let ast.Walk descend into a nested closure body here —
			// closureBlocks already enumerated those as separate blocks.
			if _, cb := closureParts(n); cb != nil {
				return false
			}
			return true
		})
	}
	return names
}

// forEachClosure invokes fn for every closure (Lambda or local FuncDecl) found
// anywhere under body, including closures nested inside other closures.
func forEachClosure(body *ast.Block, fn func(caps []ast.Param, cbody *ast.Block)) {
	for _, b := range closureBlocks(body) {
		ast.Walk(b, func(n ast.Node) bool {
			caps, cbody := closureParts(n)
			if cbody != nil {
				fn(caps, cbody)
				// closureBlocks already enumerated cbody; stop ast.Walk from
				// descending so we don't process its statements as part of b.
				return false
			}
			return true
		})
	}
}

// closureBlocks returns body plus every closure body reachable from it, each as
// a distinct block. ast.Walk does not descend into Lambda/local-FuncDecl
// bodies, so the recursion is explicit here.
func closureBlocks(body *ast.Block) []*ast.Block {
	out := []*ast.Block{body}
	ast.Walk(body, func(n ast.Node) bool {
		if _, cbody := closureParts(n); cbody != nil {
			out = append(out, closureBlocks(cbody)...)
			return false
		}
		return true
	})
	return out
}

// boxDecls turns each `var x = E` whose name is boxed into `var x: T[] = [E]`,
// keeping the same *ast.Var pointer (already registered in info.Locals) so the
// IR's exprType sees the array type; info.VarTypes is updated to match.
func boxDecls(body *ast.Block, boxed map[string]ast.Type, info *checker.Info) {
	for _, b := range closureBlocks(body) {
		ast.Walk(b, func(n ast.Node) bool {
			if v, ok := n.(*ast.Var); ok {
				if elem, isBoxed := boxed[v.Name]; isBoxed {
					arrTy := ast.ArrayType{Elem: elem}
					v.Init = &ast.ArrayLit{P: v.P, Elems: []ast.Expr{v.Init}, ElemType: elem}
					v.Type = arrTy
					if info != nil && info.VarTypes != nil {
						info.VarTypes[v] = arrTy
					}
				}
			}
			if _, cb := closureParts(n); cb != nil {
				return false
			}
			return true
		})
	}
}

// flipCaptures re-types every closure capture entry for a boxed name to the
// cell's `T[]` pointer type, so ConvertWith sizes the env slot as a pointer and
// MakeClosure snapshots the cell address (by reference), not the scalar value.
func flipCaptures(body *ast.Block, boxed map[string]ast.Type) {
	forEachClosure(body, func(caps []ast.Param, _ *ast.Block) {
		for i := range caps {
			if elem, isBoxed := boxed[caps[i].Name]; isBoxed {
				caps[i].Type = ast.ArrayType{Elem: elem}
			}
		}
	})
}

// rewriteBoxedBlock replaces every read/write of a boxed name with an index of
// the cell (`x` → `x[0]`) throughout b, descending into nested closure bodies
// (unlike ast.RewriteProgramExprs, which stops at closures). Replacing the
// Ident uniformly handles both a read (`r + x` → `r + x[0]`) and an assignment
// target (`x = v` → `x[0] = v`, an in-place store through the shared cell).
func rewriteBoxedBlock(b *ast.Block, boxed map[string]ast.Type) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		rewriteBoxedStmt(s, boxed)
	}
}

func rewriteBoxedStmt(s ast.Stmt, boxed map[string]ast.Type) {
	switch x := s.(type) {
	case *ast.Block:
		rewriteBoxedBlock(x, boxed)
	case *ast.If:
		x.Cond = rewriteBoxedExpr(x.Cond, boxed)
		rewriteBoxedStmt(x.Then, boxed)
		if x.Else != nil {
			rewriteBoxedStmt(x.Else, boxed)
		}
	case *ast.IfLet:
		x.Source = rewriteBoxedExpr(x.Source, boxed)
		rewriteBoxedStmt(x.Then, boxed)
		if x.Else != nil {
			rewriteBoxedStmt(x.Else, boxed)
		}
	case *ast.LetElse:
		x.Source = rewriteBoxedExpr(x.Source, boxed)
		if x.Else != nil {
			rewriteBoxedStmt(x.Else, boxed)
		}
	case *ast.While:
		x.Cond = rewriteBoxedExpr(x.Cond, boxed)
		rewriteBoxedStmt(x.Body, boxed)
	case *ast.Loop:
		rewriteBoxedStmt(x.Body, boxed)
	case *ast.For:
		if x.Init != nil {
			rewriteBoxedStmt(x.Init, boxed)
		}
		if x.Cond != nil {
			x.Cond = rewriteBoxedExpr(x.Cond, boxed)
		}
		if x.Step != nil {
			rewriteBoxedStmt(x.Step, boxed)
		}
		rewriteBoxedStmt(x.Body, boxed)
	case *ast.Return:
		if x.Value != nil {
			x.Value = rewriteBoxedExpr(x.Value, boxed)
		}
	case *ast.Defer:
		x.Expr = rewriteBoxedExpr(x.Expr, boxed)
	case *ast.Var:
		if x.Init != nil {
			x.Init = rewriteBoxedExpr(x.Init, boxed)
		}
	case *ast.Destructure:
		x.Init = rewriteBoxedExpr(x.Init, boxed)
	case *ast.ExprStmt:
		// An assignment statement (`x = v;`) is an *ast.Assign wrapped here;
		// rewriteBoxedExpr handles the Assign (target + value).
		x.Expr = rewriteBoxedExpr(x.Expr, boxed)
	case *ast.Switch:
		x.Tag = rewriteBoxedExpr(x.Tag, boxed)
		for ci := range x.Cases {
			for vi := range x.Cases[ci].Values {
				x.Cases[ci].Values[vi] = rewriteBoxedExpr(x.Cases[ci].Values[vi], boxed)
			}
			if x.Cases[ci].Body != nil {
				rewriteBoxedStmt(x.Cases[ci].Body, boxed)
			}
		}
		if x.Default != nil {
			rewriteBoxedStmt(x.Default, boxed)
		}
	case *ast.Match:
		x.Tag = rewriteBoxedExpr(x.Tag, boxed)
		for ai := range x.Arms {
			if x.Arms[ai].Guard != nil {
				x.Arms[ai].Guard = rewriteBoxedExpr(x.Arms[ai].Guard, boxed)
			}
			if x.Arms[ai].Body != nil {
				rewriteBoxedStmt(x.Arms[ai].Body, boxed)
			}
		}
	case *ast.FuncDecl:
		// A locally-declared closure: descend into its body so writes/reads of
		// a captured boxed name inside it become `x[0]` too.
		if x.IsLocal && x.Body != nil {
			rewriteBoxedBlock(x.Body, boxed)
		}
	case *ast.Break, *ast.Continue:
		// leaves
	}
}

func rewriteBoxedExpr(e ast.Expr, boxed map[string]ast.Type) ast.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.Ident:
		if elem, ok := boxed[x.Name]; ok {
			return &ast.Index{
				P:        x.P,
				Array:    &ast.Ident{P: x.P, Name: x.Name},
				Idx:      &ast.NumberLit{P: x.P, Value: 0},
				ElemType: elem,
			}
		}
		return x
	case *ast.NumberLit, *ast.BoolLit, *ast.StringLit, *ast.FloatLit, *ast.CaptureRef:
		return x
	case *ast.CastExpr:
		x.Inner = rewriteBoxedExpr(x.Inner, boxed)
		return x
	case *ast.DowncastExpr:
		x.Inner = rewriteBoxedExpr(x.Inner, boxed)
		return x
	case *ast.FString:
		for i := range x.Parts {
			if x.Parts[i].Expr != nil {
				x.Parts[i].Expr = rewriteBoxedExpr(x.Parts[i].Expr, boxed)
			}
		}
		return x
	case *ast.ArrayLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteBoxedExpr(x.Elems[i], boxed)
		}
		return x
	case *ast.Index:
		x.Array = rewriteBoxedExpr(x.Array, boxed)
		x.Idx = rewriteBoxedExpr(x.Idx, boxed)
		return x
	case *ast.SliceExpr:
		x.Source = rewriteBoxedExpr(x.Source, boxed)
		if x.Low != nil {
			x.Low = rewriteBoxedExpr(x.Low, boxed)
		}
		if x.High != nil {
			x.High = rewriteBoxedExpr(x.High, boxed)
		}
		return x
	case *ast.Call:
		x.Callee = rewriteBoxedExpr(x.Callee, boxed)
		for i := range x.Args {
			x.Args[i] = rewriteBoxedExpr(x.Args[i], boxed)
		}
		return x
	case *ast.Binary:
		// A composite `==`/`!=` (EqCall) or ordering op (CmpCall) carries a
		// desugared method call; rewrite inside it (its operands), mirroring
		// ast.rewriteExprChildren — Left/Right are discarded by the replacement.
		switch {
		case x.EqCall != nil:
			x.EqCall.Callee = rewriteBoxedExpr(x.EqCall.Callee, boxed)
			for i := range x.EqCall.Args {
				x.EqCall.Args[i] = rewriteBoxedExpr(x.EqCall.Args[i], boxed)
			}
		case x.CmpCall != nil:
			x.CmpCall.Callee = rewriteBoxedExpr(x.CmpCall.Callee, boxed)
			for i := range x.CmpCall.Args {
				x.CmpCall.Args[i] = rewriteBoxedExpr(x.CmpCall.Args[i], boxed)
			}
		default:
			x.Left = rewriteBoxedExpr(x.Left, boxed)
			x.Right = rewriteBoxedExpr(x.Right, boxed)
		}
		return x
	case *ast.Unary:
		x.Operand = rewriteBoxedExpr(x.Operand, boxed)
		return x
	case *ast.Assign:
		x.Target = rewriteBoxedExpr(x.Target, boxed)
		x.Value = rewriteBoxedExpr(x.Value, boxed)
		return x
	case *ast.IfExpr:
		x.Cond = rewriteBoxedExpr(x.Cond, boxed)
		x.Then = rewriteBoxedExpr(x.Then, boxed)
		x.Else = rewriteBoxedExpr(x.Else, boxed)
		return x
	case *ast.TryOp:
		x.Inner = rewriteBoxedExpr(x.Inner, boxed)
		return x
	case *ast.StructLit:
		if x.Base != nil {
			x.Base = rewriteBoxedExpr(x.Base, boxed)
		}
		for i := range x.Fields {
			x.Fields[i].Value = rewriteBoxedExpr(x.Fields[i].Value, boxed)
		}
		return x
	case *ast.TupleLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteBoxedExpr(x.Elems[i], boxed)
		}
		return x
	case *ast.MapLit:
		for i := range x.Entries {
			x.Entries[i].Key = rewriteBoxedExpr(x.Entries[i].Key, boxed)
			x.Entries[i].Value = rewriteBoxedExpr(x.Entries[i].Value, boxed)
		}
		return x
	case *ast.FieldAccess:
		x.Target = rewriteBoxedExpr(x.Target, boxed)
		return x
	case *ast.EnumLit:
		for i := range x.Args {
			x.Args[i] = rewriteBoxedExpr(x.Args[i], boxed)
		}
		return x
	case *ast.MatchExpr:
		x.Tag = rewriteBoxedExpr(x.Tag, boxed)
		for i := range x.Arms {
			if x.Arms[i].Guard != nil {
				x.Arms[i].Guard = rewriteBoxedExpr(x.Arms[i].Guard, boxed)
			}
			x.Arms[i].Body = rewriteBoxedExpr(x.Arms[i].Body, boxed)
		}
		return x
	case *ast.BlockExpr:
		for i := range x.Stmts {
			rewriteBoxedStmt(x.Stmts[i], boxed)
		}
		if x.Tail != nil {
			x.Tail = rewriteBoxedExpr(x.Tail, boxed)
		}
		return x
	case *ast.Lambda:
		// Descend into the closure body: a captured boxed name read/written
		// inside it becomes `x[0]`, which ConvertWith then routes through the
		// env (CaptureRef) — i.e. the shared cell.
		if x.Body != nil {
			rewriteBoxedBlock(x.Body, boxed)
		}
		return x
	}
	return e
}

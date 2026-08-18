package closureconv

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// BoxMutatedCaptures rewrites captured mutable locals — scalars (i32-family /
// bool / f64, #2896) and pointer-shaped values (string / array / struct / …,
// #5301) — into 1-element heap array cells so a captured outer local is shared
// BY REFERENCE, matching the interpreter, which defines the semantics. The
// native pipeline otherwise captures by value, so mutation on either side of
// the capture was lost: a closure's `x = 42` did not escape, an outer-scope
// `i = i + 1` was invisible to a closure that read `i`, and an outer-scope
// pointer reassignment (`a = [42, 1]`) left the closure reading the stale
// make-time buffer.
//
// A local is boxed iff it is captured by some closure AND assigned somewhere in
// the function — inside the closure OR in an enclosing scope. Making the box
// depend on assignment ANYWHERE (rather than only inside the closure) is what
// makes capture cohesively by-reference: one shared cell, symmetric in both
// directions, so a loop counter mutated outside is seen by a closure that reads
// it (#4391 follow-up — before this, that read returned a make-time snapshot,
// diverging from the interpreter). A captured scalar never assigned anywhere is
// left unboxed, since by-value and by-reference then coincide.
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
// captured-and-mutated local are left byte-identical.
func BoxMutatedCaptures(prog *ast.Program, info *checker.Info) {
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		boxed := collectBoxedCaptures(fn.Body)
		if len(boxed) == 0 {
			continue
		}
		boxDecls(fn.Body, boxed, info)
		rewriteBoxedBlock(fn.Body, boxed)
		flipCaptures(fn.Body, boxed)
		// Record the cell names so the IR's index-assign copy-on-write gate
		// stores through them in place rather than forking the shared cell (a
		// captured cell always has rc > 1, so CoW would otherwise copy it and
		// break the by-reference sharing this pass exists to provide).
		if info != nil {
			if info.BoxedCells == nil {
				info.BoxedCells = map[string]bool{}
			}
			for name := range boxed {
				info.BoxedCells[name] = true
			}
		}
	}
}

// boxableCapture reports whether a captured local's static type is one we box
// into a 1-element cell: any fixed-width integer, a boolean, a float, or a
// pointer-shaped value (string / array / struct / …). Pointer captures are
// E049-read-only INSIDE the closure, but the ENCLOSING scope can still
// reassign them after the closure is created — the interpreter (the oracle,
// #2896) sees that new binding, so a compiled capture must share the cell by
// reference too (#5301). The boxed pointer rides one cell slot exactly like a
// scalar: `x = v` becomes an in-place `x[0] = v` raw store through the shared
// cell. The superseded element is deliberately NOT released there — a captured
// value is reclaim-ineligible (rc.freeEligible=false skips it at overwrite AND
// exit, keeping the two balanced), so the store safe-leaks the old pointer
// rather than risking an over-release of a value the outer scope still holds.
func boxableCapture(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType:
		return true
	}
	return ast.IsPointerType(t)
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

// collectBoxedCaptures finds the boxed set for one function body: a name maps
// to its cell element type iff some closure captures it (as a boxable type),
// it is assigned SOMEWHERE in the function (inside the closure OR in an
// enclosing scope), and it is a `var`-declared local. Returns nil when there is
// nothing to box.
//
// The "assigned anywhere" test — not just "assigned inside the closure" — is
// what gives cohesive BY-REFERENCE capture (#4391 follow-up): a captured
// mutable scalar is always the same shared cell, so an outer-scope `i = i + 1`
// is visible to a closure that reads `i`, matching the interpreter. Boxing only
// closure-written names left read-captured-but-outer-mutated vars captured
// by-value-at-make-time, so the closure saw a stale snapshot (native returned 0
// where interp returned 10 for a loop-counter read from a closure). A captured
// scalar that is never assigned anywhere is left unboxed — by-value and
// by-reference coincide, so there is no reason to pay the cell indirection.
//
// The `var`-declared guard matters because parameters are reassignable in this
// language but have no `var` decl for boxDecls to turn into a cell; boxing one
// would rewrite its reads/writes to `p[0]` against a scalar slot. Only locals
// with a real declaration are boxable.
func collectBoxedCaptures(body *ast.Block) map[string]ast.Type {
	var boxed map[string]ast.Type
	writes := assignTargetNames(body)
	declared := varDeclaredNames(body)
	forEachClosure(body, func(caps []ast.Param, cbody *ast.Block) {
		if len(caps) == 0 {
			return
		}
		for _, cap := range caps {
			if !writes[cap.Name] || !declared[cap.Name] || !boxableCapture(cap.Type) {
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

// varDeclaredNames collects the names introduced by a `var` declaration
// anywhere in the function (the outer body and every closure body), so
// collectBoxedCaptures can restrict boxing to locals that boxDecls can actually
// turn into a cell — never a parameter.
func varDeclaredNames(body *ast.Block) map[string]bool {
	names := map[string]bool{}
	for _, b := range closureBlocks(body) {
		ast.Walk(b, func(n ast.Node) bool {
			if v, ok := n.(*ast.Var); ok {
				names[v.Name] = true
			}
			if _, cb := closureParts(n); cb != nil {
				return false
			}
			return true
		})
	}
	return names
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
		for _, sub := range x.Nested {
			if sub != nil {
				rewriteBoxedStmt(sub, boxed)
			}
		}
	case *ast.ExprStmt:
		// An assignment statement (`x = v;`) is an *ast.Assign wrapped here;
		// rewriteBoxedExpr handles the Assign (target + value).
		x.Expr = rewriteBoxedExpr(x.Expr, boxed)
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
	case *ast.NumberLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit, *ast.FloatLit, *ast.CaptureRef:
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

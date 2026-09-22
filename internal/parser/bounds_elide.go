package parser

import (
	"slices"

	"github.com/jakechampion/lang/internal/ast"
)

// elideLenBoundedChecks is #4380 lever 3: a purely syntactic pass that marks
// array-index READS `arr[i]` inside a `while (i < arr.len())` / C-style `for`
// loop as Unchecked, so the IR drops the per-iteration bounds check + the
// per-access `len` reload. The `_nc` ("no check") helper variants and the
// Index.Unchecked flag already exist (built for the ForEach desugar); this pass
// widens the flag to the explicit index-loop idiom that parser-shaped stdlib /
// compiler code leans on.
//
// It runs right after desugarForEachProgram — before the checker — so it works
// on plain `ast.Binary` conditions (the checker only rewrites `<` into a CmpCall
// for non-primitive operands, and an array's `.len()` is always i32). No type
// information is consulted: the IR honours Unchecked on the array and string
// paths, so marking a slice / map index is a harmless no-op there.
//
// An access is marked only when `0 <= i < len(arr)` is syntactically provable at
// that point:
//
//   - the loop guard is exactly `i < arr.len()` / `i < len(arr)` (strict `<`,
//     both operands bare idents) — gives the upper bound `i < len` — or
//     `i < n` where an earlier statement of the same block declared
//     `var n = arr.len()` and nothing between it and the loop assigns or
//     re-binds `n` or `arr`;
//   - `i` is initialised to a non-negative integer literal in the For's Init or
//     the statement immediately before the loop, and every assignment to `i` in
//     the body / step is `i = i + <non-negative int literal>` — so `i` is
//     monotonic non-decreasing and never drops below 0 (the bounds check also
//     guards NEGATIVE indices, so the lower bound must be proven too);
//   - `arr` is never reassigned and neither `i` nor `arr` is re-bound (shadowed)
//     anywhere in the body — so `len(arr)` and the binding stay invariant;
//   - the access precedes the first statement that assigns `i` (a read after an
//     increment could reach `len`).
//
// Accesses inside a lambda / local-function / `defer` body are never marked:
// that code runs later, when `i` may have advanced past `len`.
func elideLenBoundedChecks(prog *ast.Program) {
	visit := func(root ast.Node) {
		ast.Walk(root, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Block:
				elideInStmts(x.Stmts)
			case *ast.BlockExpr:
				elideInStmts(x.Stmts)
			}
			return true
		})
	}
	for _, fn := range prog.Funcs {
		if fn.Body != nil {
			visit(fn.Body)
		}
	}
	for _, tr := range prog.Traits {
		for i := range tr.Methods {
			if tr.Methods[i].Body != nil {
				visit(tr.Methods[i].Body)
			}
		}
	}
	for _, cn := range prog.Consts {
		if cn.Value != nil {
			visit(cn.Value)
		}
	}
}

// elideInStmts scans one statement list for While / For loops, passing each the
// statement immediately before it (the source of a while-loop's start value).
// Bodies are themselves Blocks reached separately by the enclosing Walk, so this
// only inspects the direct children here.
func elideInStmts(stmts []ast.Stmt) {
	for p, stmt := range stmts {
		var prev ast.Stmt
		if p > 0 {
			prev = stmts[p-1]
		}
		switch x := stmt.(type) {
		case *ast.While:
			tryElideLoop(nil, stmts[:p], prev, x.Cond, x.Body)
		case *ast.For:
			tryElideLoop(x, stmts[:p], prev, x.Cond, x.Body)
		}
	}
}

// tryElideLoop performs the elision for one loop. forNode is the *ast.For when
// the loop is a C-style for (nil for a while); before is the statements that
// precede the loop in its block, and prev the last of them.
func tryElideLoop(forNode *ast.For, before []ast.Stmt, prev ast.Stmt, cond ast.Expr, body ast.Stmt) {
	idx, arr, lenVar, ok := loopIndexAndArray(cond, before)
	if !ok {
		return
	}
	bodyBlock, ok := body.(*ast.Block)
	if !ok {
		// A brace-less body is a single statement; treat it as a
		// one-statement block so the barrier logic still applies.
		bodyBlock = &ast.Block{P: body.Pos(), Stmts: []ast.Stmt{body}}
	}
	// `arr` must stay the same binding and length throughout, and neither name
	// may be re-bound (shadowed) in the body.
	if assignsIdent(bodyBlock, arr) {
		return
	}
	if bindsIdent(bodyBlock, idx) || bindsIdent(bodyBlock, arr) {
		return
	}
	// A captured length must hold too: the body may neither assign nor
	// re-bind it.
	if lenVar != "" && (assignsIdent(bodyBlock, lenVar) || bindsIdent(bodyBlock, lenVar)) {
		return
	}
	// Every assignment to `idx` (body + step) must be a non-negative increment,
	// so `idx` is monotonic non-decreasing.
	if !allIdxAssignsAreMonotonic(bodyBlock, idx) {
		return
	}
	if forNode != nil && forNode.Step != nil && !allIdxAssignsAreMonotonic(forNode.Step, idx) {
		return
	}
	// `idx` must start at a non-negative literal.
	if !idxStartsNonNegative(forNode, prev, idx) {
		return
	}
	// Mark accesses in the prefix of body statements that precedes the first
	// one assigning `idx`.
	for _, st := range bodyBlock.Stmts {
		if assignsIdent(st, idx) {
			break
		}
		markIndexReads(st, idx, arr)
	}
}

// loopIndexAndArray matches `IDX < ARR.len()` / `IDX < len(ARR)` / `IDX < N`
// with a strict `<` and bare idents, and returns (IDX, ARR, N): N is "" for
// the direct forms, and for the captured form the name of the `var N =
// ARR.len()` declared among `before` (see capturedLenArray).
func loopIndexAndArray(cond ast.Expr, before []ast.Stmt) (idx, arr, lenVar string, ok bool) {
	bin, isBin := cond.(*ast.Binary)
	if !isBin || bin.Op != "<" {
		return "", "", "", false
	}
	left, isIdent := bin.Left.(*ast.Ident)
	if !isIdent {
		return "", "", "", false
	}
	if n, isIdent := bin.Right.(*ast.Ident); isIdent {
		arr, ok = capturedLenArray(n.Name, before)
		lenVar = n.Name
	} else {
		arr, ok = lenCallArray(bin.Right)
	}
	if !ok || arr == left.Name || lenVar == left.Name {
		return "", "", "", false
	}
	return left.Name, arr, lenVar, true
}

// capturedLenArray resolves a loop bound `n` to the array whose length it
// holds: the nearest `var n = ARR.len()` among the statements before the
// loop, with no statement after it assigning or re-binding `n` or `ARR`.
// A declaration inside a nested statement does not count — it may not have
// run — so only a direct `var` of the block qualifies.
func capturedLenArray(n string, before []ast.Stmt) (string, bool) {
	for k := len(before) - 1; k >= 0; k-- {
		if v, ok := before[k].(*ast.Var); ok && v.Name == n {
			arr, ok := lenCallArray(v.Init)
			if !ok {
				return "", false
			}
			for _, st := range before[k+1:] {
				if assignsIdent(st, n) || assignsIdent(st, arr) || bindsIdent(st, n) || bindsIdent(st, arr) {
					return "", false
				}
			}
			return arr, true
		}
		if assignsIdent(before[k], n) || bindsIdent(before[k], n) {
			return "", false
		}
	}
	return "", false
}

// lenCallArray returns the array-ident name of a length expression — `A.len()`
// (method form) or `len(A)` (free-function form) — where A is a bare ident.
func lenCallArray(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.Call)
	if !ok {
		return "", false
	}
	switch callee := call.Callee.(type) {
	case *ast.FieldAccess:
		if callee.Field == "len" && len(call.Args) == 0 {
			if id, ok := callee.Target.(*ast.Ident); ok {
				return id.Name, true
			}
		}
	case *ast.Ident:
		if callee.Name == "len" && len(call.Args) == 1 {
			if id, ok := call.Args[0].(*ast.Ident); ok {
				return id.Name, true
			}
		}
	}
	return "", false
}

// idxStartsNonNegative proves `idx`'s value at loop entry is a non-negative
// integer literal: from the For's Init, else from the statement immediately
// before the loop (`var i = 0` / `i = 0`). Nothing runs between that statement
// and the loop guard, so the literal is the entry value.
func idxStartsNonNegative(forNode *ast.For, prev ast.Stmt, idx string) bool {
	if forNode != nil && forNode.Init != nil {
		if v, ok := initValueOfIdent(forNode.Init, idx); ok {
			return isNonNegIntLit(v)
		}
	}
	if prev != nil {
		if v, ok := initValueOfIdent(prev, idx); ok {
			return isNonNegIntLit(v)
		}
	}
	return false
}

// initValueOfIdent returns the RHS value of a `var idx = v` or `idx = v`
// statement, if s is exactly that.
func initValueOfIdent(s ast.Stmt, idx string) (ast.Expr, bool) {
	switch x := s.(type) {
	case *ast.Var:
		if x.Name == idx {
			return x.Init, true
		}
	case *ast.ExprStmt:
		if asn, ok := x.Expr.(*ast.Assign); ok {
			if id, ok := asn.Target.(*ast.Ident); ok && id.Name == idx {
				return asn.Value, true
			}
		}
	}
	return nil, false
}

// isNonNegIntLit reports whether e is a non-negative integer literal. A `-1`
// parses as a Unary over a literal, so only a bare NumberLit >= 0 qualifies.
func isNonNegIntLit(e ast.Expr) bool {
	lit, ok := e.(*ast.NumberLit)
	return ok && lit.Value >= 0
}

// allIdxAssignsAreMonotonic reports whether every assignment to `idx` reachable
// from n is `idx = idx + <non-negative int literal>` — i.e. keeps idx monotonic
// non-decreasing. Any other assignment shape fails (returns false).
func allIdxAssignsAreMonotonic(n ast.Node, idx string) bool {
	ok := true
	ast.Walk(n, func(nd ast.Node) bool {
		if !ok {
			return false
		}
		asn, isAsn := nd.(*ast.Assign)
		if !isAsn {
			return true
		}
		id, isIdent := asn.Target.(*ast.Ident)
		if !isIdent || id.Name != idx {
			return true
		}
		if !isMonotonicIncrement(asn.Value, idx) {
			ok = false
			return false
		}
		return true
	})
	return ok
}

// isMonotonicIncrement reports whether v is `idx + <non-negative int literal>`.
func isMonotonicIncrement(v ast.Expr, idx string) bool {
	bin, ok := v.(*ast.Binary)
	if !ok || bin.Op != "+" {
		return false
	}
	left, ok := bin.Left.(*ast.Ident)
	if !ok || left.Name != idx {
		return false
	}
	return isNonNegIntLit(bin.Right)
}

// assignsIdent reports whether any assignment target `name` appears in n's
// subtree (compound `+=` etc. have already been desugared to Assign at parse).
func assignsIdent(n ast.Node, name string) bool {
	found := false
	ast.Walk(n, func(nd ast.Node) bool {
		if found {
			return false
		}
		if asn, ok := nd.(*ast.Assign); ok {
			if id, ok := asn.Target.(*ast.Ident); ok && id.Name == name {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// bindsIdent reports whether any binding construct in n's subtree introduces
// (declares / shadows) `name`: a local `var`, tuple destructure, lambda / local
// function parameter, or a match / if-let / let-else pattern binding.
func bindsIdent(n ast.Node, name string) bool {
	found := false
	ast.Walk(n, func(nd ast.Node) bool {
		if found {
			return false
		}
		switch x := nd.(type) {
		case *ast.Var:
			if x.Name == name {
				found = true
			}
		case *ast.Destructure:
			for _, nm := range x.Names {
				if nm == name {
					found = true
				}
			}
		case *ast.Lambda:
			for _, p := range x.Params {
				if p.Name == name {
					found = true
				}
			}
		case *ast.FuncDecl:
			for _, p := range x.Params {
				if p.Name == name {
					found = true
				}
			}
		case *ast.Match:
			for _, a := range x.Arms {
				if slices.Contains(a.Binders(), name) {
					found = true
				}
			}
		case *ast.MatchExpr:
			for _, a := range x.Arms {
				if slices.Contains(a.Binders(), name) {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// markIndexReads sets Unchecked on every `arr[idx]` Index node in s's subtree
// that executes in-place during the current iteration. It does NOT descend into
// lambda / local-function / defer bodies: those run later (when idx may have
// advanced), so an access inside them is not provably in bounds.
func markIndexReads(s ast.Node, idx, arr string) {
	ast.Walk(s, func(nd ast.Node) bool {
		switch x := nd.(type) {
		case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
			return false // deferred/hoisted execution — do not mark inside
		case *ast.Index:
			if arrID, ok := x.Array.(*ast.Ident); ok && arrID.Name == arr {
				if idxID, ok := x.Idx.(*ast.Ident); ok && idxID.Name == idx {
					x.Unchecked = true
				}
			}
		}
		return true
	})
}

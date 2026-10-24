package parser

import (
	"slices"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

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

func tryElideLoop(forNode *ast.For, before []ast.Stmt, prev ast.Stmt, cond ast.Expr, body ast.Stmt) {
	idx, arr, lenVar, ok := loopIndexAndArray(cond, before)
	if !ok {
		return
	}
	bodyBlock, ok := body.(*ast.Block)
	if !ok {
		bodyBlock = &ast.Block{P: body.Pos(), Stmts: []ast.Stmt{body}}
	}
	if assignsIdent(bodyBlock, arr) {
		return
	}
	if bindsIdent(bodyBlock, idx) || bindsIdent(bodyBlock, arr) {
		return
	}
	if lenVar != "" && (assignsIdent(bodyBlock, lenVar) || bindsIdent(bodyBlock, lenVar)) {
		return
	}
	if !allIdxAssignsAreMonotonic(bodyBlock, idx) {
		return
	}
	if forNode != nil && forNode.Step != nil && !allIdxAssignsAreMonotonic(forNode.Step, idx) {
		return
	}
	if !idxStartsNonNegative(forNode, prev, idx) {
		return
	}
	for _, st := range bodyBlock.Stmts {
		if assignsIdent(st, idx) {
			break
		}
		markIndexReads(st, idx, arr)
	}
}

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

func isNonNegIntLit(e ast.Expr) bool {
	lit, ok := e.(*ast.NumberLit)
	return ok && lit.Value >= 0
}

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

func markIndexReads(s ast.Node, idx, arr string) {
	ast.Walk(s, func(nd ast.Node) bool {
		switch x := nd.(type) {
		case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
			return false
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

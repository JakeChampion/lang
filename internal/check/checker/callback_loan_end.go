package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// endedCallbackLoans proves a loan end in a straight-line body. Every use of
// each capturing callback must be a direct call before the owner's last read.
func endedCallbackLoans(node ast.Node) map[string]bool {
	body, ok := node.(*ast.Block)
	if !ok || body == nil {
		return nil
	}
	locals := map[string]int{}
	callbacks := map[*ast.Lambda]string{}
	for _, stmt := range body.Stmts {
		switch s := stmt.(type) {
		case *ast.Var:
			locals[s.Name]++
			if lm, ok := s.Init.(*ast.Lambda); ok {
				callbacks[lm] = s.Name
			}
		case *ast.Return, *ast.ExprStmt:
		default:
			return nil
		}
	}
	if len(callbacks) == 0 {
		return nil
	}
	valid := true
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Lambda:
			if callbacks[x] == "" {
				valid = false
			}
		case *ast.Assign, *ast.If, *ast.IfExpr, *ast.Match, *ast.MatchExpr,
			*ast.While, *ast.For, *ast.ForEach, *ast.Defer, *ast.BlockExpr, *ast.FuncDecl:
			valid = false
		}
		return true
	})
	if !valid {
		return nil
	}
	order := IdentOrderOf(body)
	calls := map[*ast.Ident]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.Lambda); ok {
			return false
		}
		if call, ok := n.(*ast.Call); ok && call.Method == nil {
			if id, ok := call.Callee.(*ast.Ident); ok {
				calls[id] = true
			}
		}
		return true
	})
	ended, blocked := map[string]bool{}, map[string]bool{}
	for lm, callback := range callbacks {
		uses := order.byName[callback]
		for owner := range lambdaFreeNames(lm) {
			if locals[owner] != 1 {
				continue
			}
			ended[owner] = true
			if locals[callback] != 1 {
				blocked[owner] = true
			}
			for _, use := range uses {
				if !calls[use] || order.Idx[use] >= order.last[owner] {
					blocked[owner] = true
				}
			}
		}
	}
	for owner := range blocked {
		delete(ended, owner)
	}
	return ended
}

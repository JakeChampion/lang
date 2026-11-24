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

// directCallbackLoans proves that a directly bound callback finishes all its
// calls in its declaring block before the next access to the captured owner.
// In a loop the binding and this interval start again on every iteration.
func directCallbackLoans(body *ast.Block, escaping map[string]bool) map[string]bool {
	type binding struct {
		name  string
		after []ast.Stmt
	}
	callbacks := map[*ast.Lambda]binding{}
	locals := map[string]int{}
	lambdas := 0
	unsupported := false
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Defer, *ast.FuncDecl:
			unsupported = true
		case *ast.Lambda:
			if x.Once {
				unsupported = true
				return false
			}
			lambdas++
			ast.Walk(x.Body, func(inner ast.Node) bool {
				switch inner.(type) {
				case *ast.Lambda, *ast.Defer, *ast.FuncDecl:
					unsupported = true
				}
				return !unsupported
			})
			return false
		case *ast.Var:
			locals[x.Name]++
		case *ast.Block:
			for i, stmt := range x.Stmts {
				if v, ok := stmt.(*ast.Var); ok {
					if lm, ok := v.Init.(*ast.Lambda); ok {
						callbacks[lm] = binding{v.Name, x.Stmts[i+1:]}
					}
				}
			}
		}
		return true
	})
	if unsupported || lambdas == 0 || len(callbacks) != lambdas {
		return nil
	}
	order := IdentOrderOf(body)
	ended, blocked := map[string]bool{}, map[string]bool{}
	for lm, callback := range callbacks {
		for owner := range lambdaFreeNames(lm) {
			if !escaping[owner] || locals[owner] != 1 || blocked[owner] {
				continue
			}
			ended[owner] = true
			calls := directCallsBeforeOwner(callback.after, owner)
			if locals[callback.name] != 1 || len(order.byName[callback.name]) == 0 {
				blocked[owner] = true
			}
			for _, use := range order.byName[callback.name] {
				if !calls[use] || order.Idx[use] >= order.last[owner] {
					blocked[owner] = true
					break
				}
			}
		}
	}
	for name := range blocked {
		delete(ended, name)
	}
	return ended
}

func directCallsBeforeOwner(stmts []ast.Stmt, owner string) map[*ast.Ident]bool {
	calls := map[*ast.Ident]bool{}
	for _, stmt := range stmts {
		switch stmt.(type) {
		case *ast.Var, *ast.Return, *ast.ExprStmt:
		default:
			return calls
		}
		if StmtReferencesName(stmt, owner) {
			return calls
		}
		// Stop before an expression introduces a different control-flow region.
		linear := true
		ast.Walk(stmt, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.Lambda, *ast.BlockExpr, *ast.IfExpr, *ast.MatchExpr:
				linear = false
			}
			return linear
		})
		if !linear {
			return calls
		}
		ast.Walk(stmt, func(n ast.Node) bool {
			if c, ok := n.(*ast.Call); ok && c.Method == nil {
				if id, ok := c.Callee.(*ast.Ident); ok {
					calls[id] = true
				}
			}
			return true
		})
	}
	return calls
}

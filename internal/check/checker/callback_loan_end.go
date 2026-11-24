package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// endedCallbackLoans proves a loan end in a straight-line body. Every use of
// each capturing callback must be a direct call before the first move of the
// owner that follows the lambda.
func endedCallbackLoans(node ast.Node) map[string]bool {
	body, ok := node.(*ast.Block)
	if !ok || body == nil {
		return nil
	}
	locals := map[string]int{}
	callbacks := map[*ast.Lambda]string{}
	assigns := map[*ast.Assign]bool{}
	for _, stmt := range body.Stmts {
		switch s := stmt.(type) {
		case *ast.Var:
			locals[s.Name]++
			if lm, ok := s.Init.(*ast.Lambda); ok {
				callbacks[lm] = s.Name
			}
		case *ast.ExprStmt:
			if assign, ok := s.Expr.(*ast.Assign); ok {
				if _, ok := assign.Target.(*ast.Ident); !ok {
					return nil
				}
				assigns[assign] = true
			}
		case *ast.Return:
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
		case *ast.Assign:
			if !assigns[x] {
				valid = false
			}
		case *ast.If, *ast.IfExpr, *ast.Match, *ast.MatchExpr,
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
			bound := loanBound(order, loanMoves(order, assigns, owner), lm, owner)
			ended[owner] = true
			if locals[callback] != 1 {
				blocked[owner] = true
			}
			for _, use := range uses {
				if !calls[use] || order.Idx[use] >= bound {
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

// loanMoves gives the indexes of the occurrences that can end a value of the
// owner: each assignment, the last occurrence before an assignment whose value
// does not read the owner, and the last occurrence of all.
func loanMoves(order IdentOrder, assigns map[*ast.Assign]bool, owner string) []int {
	moves := []int{order.last[owner]}
	for assign := range assigns {
		target := assign.Target.(*ast.Ident)
		if target.Name != owner {
			continue
		}
		at := order.Idx[target]
		moves = append(moves, at)
		if maxIdentIndex(order, assign.Value, owner) > 0 {
			continue
		}
		before := 0
		for _, id := range order.byName[owner] {
			if i := order.Idx[id]; i < at && i > before {
				before = i
			}
		}
		if before > 0 {
			moves = append(moves, before)
		}
	}
	return moves
}

// loanBound gives the first move of the owner after the lambda reads it. The
// last occurrence of the owner is the result when no move follows the lambda.
func loanBound(order IdentOrder, moves []int, lm *ast.Lambda, owner string) int {
	read := maxIdentIndex(order, lm, owner)
	bound := order.last[owner]
	for _, move := range moves {
		if move > read && move < bound {
			bound = move
		}
	}
	return bound
}

func maxIdentIndex(order IdentOrder, node ast.Node, name string) int {
	last := 0
	ast.Walk(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name && order.Idx[id] > last {
			last = order.Idx[id]
		}
		return true
	})
	return last
}

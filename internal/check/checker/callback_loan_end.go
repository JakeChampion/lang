package checker

import (
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

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

// lentPathMoved reports a consumed field of the lender that the loan covers:
// the field that the loan names, or each field when the loan has the whole
// lender. A wide loan of a named field covers only that field.
func lentPathMoved(moved map[string]ast.Position, loan callbackLoan) (ast.Position, bool) {
	head, _, _ := strings.Cut(loan.path, "/")
	field, named := strings.CutPrefix(head, "f:")
	if named {
		at, consumed := moved[loan.param.Name+"."+field]
		return at, consumed
	}
	if head != "" && !loan.wide {
		return ast.Position{}, false
	}
	prefix := loan.param.Name + "."
	var first ast.Position
	found := false
	for path, at := range moved {
		if strings.HasPrefix(path, prefix) && (!found || positionBefore(at, first)) {
			first, found = at, true
		}
	}
	return first, found
}

// movedOperands gives each name that value can move into the local that it
// initializes: the value, a part of a literal, or the result of a branch.
func movedOperands(value ast.Expr) []*ast.Ident {
	switch v := value.(type) {
	case *ast.Ident:
		return []*ast.Ident{v}
	case *ast.StructLit:
		var out []*ast.Ident
		for _, field := range v.Fields {
			out = append(out, movedOperands(field.Value)...)
		}
		return out
	case *ast.TupleLit:
		var out []*ast.Ident
		for _, elem := range v.Elems {
			out = append(out, movedOperands(elem)...)
		}
		return out
	case *ast.ArrayLit:
		var out []*ast.Ident
		for _, elem := range v.Elems {
			out = append(out, movedOperands(elem)...)
		}
		return out
	case *ast.Call:
		if !v.IsVariantCall {
			return nil
		}
		var out []*ast.Ident
		for _, arg := range v.Args {
			out = append(out, movedOperands(arg)...)
		}
		return out
	case *ast.IfExpr:
		return append(movedOperands(v.Then), movedOperands(v.Else)...)
	case *ast.MatchExpr:
		var out []*ast.Ident
		for _, arm := range v.Arms {
			out = append(out, movedOperands(arm.Body)...)
		}
		return out
	case *ast.BlockExpr:
		return movedOperands(v.Tail)
	}
	return nil
}

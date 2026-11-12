package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// The transfer ends the payload's only use, before any intervening lender access.
func matchPayloadRebinds(info *Info, body *ast.Block, occurrences map[string]int, escaping map[string]bool) map[*ast.Ident]string {
	out := map[*ast.Ident]string{}
	ast.Walk(body, func(n ast.Node) bool {
		m, ok := n.(*ast.Match)
		if !ok {
			return true
		}
		tag, ok := m.Tag.(*ast.Call)
		if !ok {
			return true
		}
		guarded := map[string]bool{}
		for _, arm := range m.Arms {
			rebindNames(arm.Guard, guarded)
		}
		for _, arm := range m.Arms {
			if arm.Body == nil {
				continue
			}
			names, _ := arm.OwnBindings()
			if len(names) != 1 {
				continue
			}
			binders := map[string]bool{}
			for _, name := range names {
				binders[name] = true
			}
			prior := map[string]bool{}
			for _, st := range arm.Body.Stmts {
				if es, ok := st.(*ast.ExprStmt); ok {
					if a, ok := es.Expr.(*ast.Assign); ok && a.Op == "" {
						target, targetOK := a.Target.(*ast.Ident)
						value, valueOK := a.Value.(*ast.Ident)
						if targetOK && valueOK && binders[value.Name] && occurrences[value.Name] == 1 && !escaping[value.Name] && !escaping[target.Name] && !guarded[target.Name] && !prior[target.Name] && rebindSource(info, tag, target.Name) {
							out[value] = target.Name
						}
					}
				}
				rebindNames(st, prior)
			}
		}
		return true
	})
	return out
}

func rebindSource(info *Info, call *ast.Call, target string) bool {
	if _, named := call.Callee.(*ast.Ident); !named || info == nil || info.CallBorrowsBeside == nil || info.CallBorrowsBeside(call, target) {
		return false
	}
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && id.Name == target {
			continue
		}
		if StmtReferencesName(arg, target) {
			return false
		}
	}
	return true
}

func rebindNames(node ast.Node, names map[string]bool) {
	if node == nil {
		return
	}
	ast.Walk(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			names[id.Name] = true
		}
		return true
	})
}

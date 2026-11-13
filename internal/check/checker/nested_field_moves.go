package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// A returned record can replace the complete field that owns a local alias.
func nestedFieldOwnMoveArgs(fn *ast.FuncDecl, value ast.Expr, owned, borrowed map[string]bool, ownFuncs map[string][]bool) []*ast.FieldAccess {
	if parts, ok := nestedFieldTransferParts(value); ok {
		var out []*ast.FieldAccess
		for _, element := range parts {
			out = append(out, nestedFieldOwnMoveArgs(fn, element, owned, borrowed, ownFuncs)...)
		}
		return out
	}
	top, ok := value.(*ast.StructLit)
	if !ok {
		return nil
	}
	root, ok := top.Base.(*ast.Ident)
	if !ok || !root.Local || !owned[root.Name] || borrowed[root.Name] || !ownershipBindingUnchanged(fn, root.Name) || identifierReads(fn.Body, root.Name) != 2 {
		return nil
	}
	var out []*ast.FieldAccess
	for _, field := range top.Fields {
		inner, ok := field.Value.(*ast.StructLit)
		if !ok {
			continue
		}
		alias, ok := inner.Base.(*ast.Ident)
		if !ok || !alias.Local || !nestedFieldAlias(fn, alias.Name, root.Name, field.Name) || identifierReads(fn.Body, alias.Name) != identifierReads(inner, alias.Name) {
			continue
		}
		out = append(out, SupersededFieldOwnMoveArgs(inner, alias.Name, ownFuncs)...)
	}
	return out
}

func nestedFieldTransferParts(value ast.Expr) ([]ast.Expr, bool) {
	switch value := value.(type) {
	case *ast.TupleLit:
		return value.Elems, true
	case *ast.Call:
		if value.IsVariantCall {
			return value.Args, true
		}
	}
	return nil, false
}

func nestedFieldAlias(fn *ast.FuncDecl, alias, root, field string) bool {
	found := false

	declarations, assigned := 0, false
	ast.Walk(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Var:
			if n.Name == alias {
				declarations++
				if value, ok := n.Init.(*ast.FieldAccess); ok && value.Field == field {
					owner, ok := value.Target.(*ast.Ident)
					found = ok && owner.Local && owner.Name == root
				}
			}
		case *ast.Assign:
			if name, ok := n.Target.(*ast.Ident); ok && name.Name == alias {
				assigned = true
			}
		}
		return true
	})
	return found && declarations == 1 && !assigned
}

func identifierReads(node ast.Node, name string) int {
	count := 0
	ast.Walk(node, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && id.Name == name {
			count++
		}
		return true
	})
	return count
}

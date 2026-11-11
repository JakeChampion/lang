package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// A field of an unchanged owned input retains the input's value contract.
func originalOwnedFieldSource(fn *ast.FuncDecl, value ast.Expr) string {
	field, ok := value.(*ast.FieldAccess)
	if !ok {
		return ""
	}
	owner, ok := field.Target.(*ast.Ident)
	if !ok {
		return ""
	}
	original := fn.Receiver != nil && fn.Receiver.Own && fn.Receiver.Name == owner.Name
	for _, param := range fn.Params {
		original = original || param.Own && param.Name == owner.Name
	}
	if original && ownershipBindingUnchanged(fn, owner.Name) {
		return owner.Name
	}
	return ""
}

func ownershipBindingUnchanged(fn *ast.FuncDecl, name string) bool {
	unchanged := true
	ast.Walk(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Assign:
			if target, named := n.Target.(*ast.Ident); named && target.Name == name {
				unchanged = false
			}
		case *ast.Var:
			unchanged = unchanged && n.Name != name
		case *ast.Destructure:
			for _, binding := range n.Names {
				unchanged = unchanged && binding != name
			}
			unchanged = unchanged && n.AtName != name
		}
		return unchanged
	})
	return unchanged
}

package checker

import (
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func (c *checker) resultPendingCleanup(value ast.Expr, pending map[string]ast.Position) bool {
	if len(pending) == 0 || c.resultCopyParts[value] {
		return false
	}
	var parts []ast.Expr
	switch e := value.(type) {
	case *ast.StructLit:
		parts = append(parts, e.Base)
		for _, field := range e.Fields {
			parts = append(parts, field.Value)
		}
	case *ast.TupleLit:
		parts = e.Elems
	case *ast.ArrayLit:
		parts = e.Elems
	case *ast.MapLit:
		for _, entry := range e.Entries {
			parts = append(parts, entry.Key, entry.Value)
		}
	case *ast.EnumLit:
		parts = e.Args
	case *ast.Call:
		if _, variant := c.info.EnumConstructions[e]; variant {
			parts = e.Args
		}
	}
	for _, part := range parts {
		if c.resultPendingCleanup(part, pending) {
			return true
		}
	}
	path := ""
	switch e := value.(type) {
	case *ast.Ident:
		path = e.Name
	case *ast.FieldAccess:
		field := enclosingField(e)
		if root, ok := field.Target.(*ast.Ident); ok {
			if _, deferred := pending[root.Name]; deferred {
				return true
			}
			path = root.Name + "." + field.Field
		}
	}
	if _, deferred := pending[path]; path != "" && deferred {
		return true
	}
	for field := range pending {
		if path != "" && strings.HasPrefix(field, path+".") {
			return true
		}
	}
	return false
}

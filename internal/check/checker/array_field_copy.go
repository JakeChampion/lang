package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// Primitive COW arrays cannot retain a resource alias. Views are not arrays.
func primitiveArrayCopy(t ast.Type) bool {
	array, ok := t.(ast.ArrayType)
	if !ok {
		return false
	}
	for {
		t = array.Elem
		array, ok = t.(ast.ArrayType)
		if !ok {
			return definitelyScalar(t)
		}
	}
}

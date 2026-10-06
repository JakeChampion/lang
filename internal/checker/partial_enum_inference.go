package checker

import "github.com/jakechampion/lang/internal/ast"

func unboundEnumArg(t ast.Type) bool {
	_, ok := t.(ast.UnboundType)
	return ok
}

// mergeEnumInference combines information from two values of the same enum.
// An absent argument supplies no constraint; known arguments must still agree.
func mergeEnumInference(a, b ast.EnumType) (ast.EnumType, bool) {
	if a.Name != b.Name {
		return ast.EnumType{}, false
	}
	if len(a.Args) == 0 {
		return b, true
	}
	if len(b.Args) == 0 {
		return a, true
	}
	if len(a.Args) != len(b.Args) {
		return ast.EnumType{}, false
	}
	args := make([]ast.Type, len(a.Args))
	for i, x := range a.Args {
		y := b.Args[i]
		switch {
		case unboundEnumArg(x):
			args[i] = y
		case unboundEnumArg(y), ast.Equal(x, y):
			args[i] = x
		default:
			args[i] = unifyIfArms(x, y)
			if args[i] == nil {
				return ast.EnumType{}, false
			}
		}
	}
	return ast.EnumType{Name: a.Name, Args: args}, true
}

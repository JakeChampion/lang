package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// ownedBesideView reports two arm types that differ only where one has an
// owned `string` or `T[]` and the other has a `str` or `[T]` view. A join of
// the two is E031: the value would be owned on one path and borrowed on the
// other. compiler/checker.fern's owned_beside_view holds the same rule.
func ownedBesideView(a, b ast.Type) bool {
	return shiftsView(a, b) || shiftsView(b, a)
}

func shiftsView(from, to ast.Type) bool {
	switch f := from.(type) {
	case ast.StrType:
		_, ok := to.(ast.StringType)
		return ok
	case ast.SliceType:
		switch t := to.(type) {
		case ast.ArrayType:
			return true
		case ast.SliceType:
			return shiftsView(f.Elem, t.Elem)
		}
	case ast.ArrayType:
		if t, ok := to.(ast.ArrayType); ok {
			return shiftsView(f.Elem, t.Elem)
		}
	case ast.TupleType:
		if t, ok := to.(ast.TupleType); ok {
			return anyShiftsView(f.Elems, t.Elems)
		}
	case ast.StructType:
		if t, ok := to.(ast.StructType); ok && f.Name == t.Name {
			return anyShiftsView(f.Args, t.Args)
		}
	case ast.EnumType:
		if t, ok := to.(ast.EnumType); ok && f.Name == t.Name {
			return anyShiftsView(f.Args, t.Args)
		}
	}
	return false
}

func anyShiftsView(from, to []ast.Type) bool {
	if len(from) != len(to) {
		return false
	}
	for i := range from {
		if shiftsView(from[i], to[i]) {
			return true
		}
	}
	return false
}

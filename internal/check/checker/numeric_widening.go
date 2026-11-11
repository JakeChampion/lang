package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// losslessNumericWiden permits only scalar conversions that preserve every
// source value. Pointer widths need a target and keep their separate rules.
func losslessNumericWiden(from, to ast.Type) bool {
	if a, ok := from.(ast.NumberType); ok {
		b, ok := to.(ast.NumberType)
		if !ok || a.Polymorphic || b.Polymorphic || a.IsPointerWidth() || b.IsPointerWidth() {
			return false
		}
		if a.IsSigned() == b.IsSigned() {
			return a.NormalWidth() < b.NormalWidth()
		}
		return !a.IsSigned() && b.IsSigned() && a.NormalWidth() < b.NormalWidth()
	}
	if a, ok := from.(ast.FloatType); ok {
		b, ok := to.(ast.FloatType)
		return ok && !a.Polymorphic && !b.Polymorphic && a.NormalWidth() < b.NormalWidth()
	}
	return false
}

func (c *checker) postNumericType(e ast.Expr, prior ast.Type) ast.Type {
	if cast := c.numericCasts[e]; cast != nil {
		return cast.Target
	}
	return prior
}

func scalarNumericJoin(a, b ast.Type) ast.Type {
	if losslessNumericWiden(a, b) {
		return b
	}
	if losslessNumericWiden(b, a) {
		return a
	}
	if _, ok := a.(ast.NumberType); ok {
		if _, ok := b.(ast.NumberType); ok {
			if t, ok := commonIntegerWidth(a, b); ok {
				return t
			}
		}
	}
	return nil
}

// nestedArrayJoin is the array type that a and b share when their innermost
// element types have a numeric join, or nil.
func nestedArrayJoin(a, b ast.Type) ast.Type {
	at, aOK := a.(ast.ArrayType)
	bt, bOK := b.(ast.ArrayType)
	if !aOK || !bOK {
		return nil
	}
	inner := scalarNumericJoin(at.Elem, bt.Elem)
	if inner == nil {
		inner = nestedArrayJoin(at.Elem, bt.Elem)
	}
	if inner == nil {
		return nil
	}
	return ast.ArrayType{Elem: inner}
}

// nestedArrayLiteralJoin is the type that the last element in types shares
// with the element type so far, when both are array types. Each element that
// gets another type must be an array literal, because only a literal can
// take a wider element type. The result is nil when there is no such type.
func nestedArrayLiteralJoin(elems []ast.Expr, types []ast.Type, so ast.Type) ast.Type {
	joined := nestedArrayJoin(so, types[len(types)-1])
	if joined == nil {
		return nil
	}
	for i, t := range types {
		if t != nil && !ast.Equal(t, joined) && !arrayLiteralTree(elems[i], joined) {
			return nil
		}
	}
	return joined
}

// arrayLiteralTree is true for an array literal whose elements are array
// literals down to the innermost element type of want.
func arrayLiteralTree(e ast.Expr, want ast.Type) bool {
	lit, ok := e.(*ast.ArrayLit)
	at, array := want.(ast.ArrayType)
	if !ok || !array {
		return false
	}
	if _, nested := at.Elem.(ast.ArrayType); !nested {
		return true
	}
	for _, el := range lit.Elems {
		if !arrayLiteralTree(el, at.Elem) {
			return false
		}
	}
	return true
}

var (
	i32Type = ast.NumberType{Width: 32, Signed: true}
	i64Type = ast.NumberType{Width: 64, Signed: true}
)

// wideLiteralElem gives an unsettled integer type in t the type i64 when a
// literal of es fits in neither i32 nor the narrower integer type that
// beside has there. The join of the two types then widens the value beside
// the literal.
func (c *checker) wideLiteralElem(t, beside ast.Type, es ...ast.Expr) ast.Type {
	switch tt := t.(type) {
	case ast.NumberType:
		bn, ok := beside.(ast.NumberType)
		if !ok || !tt.Polymorphic || !losslessNumericWiden(bn, i64Type) {
			return t
		}
		for _, e := range es {
			if c.unsettledIntLitExceeds(e, false, i32Type) && c.unsettledIntLitExceeds(e, false, bn) {
				return i64Type
			}
		}
	case ast.ArrayType:
		if bt, ok := beside.(ast.ArrayType); ok {
			return ast.ArrayType{Elem: c.wideLiteralElem(tt.Elem, bt.Elem, arrayElemExprs(es)...)}
		}
	}
	return t
}

func (c *checker) settleArrayLiteral(al *ast.ArrayLit, want ast.Type) {
	if !c.elemSettleable(al.ElemType, want) {
		return
	}
	for _, el := range al.Elems {
		c.settleNumeric(el, want)
		if got := c.postSettleType(el, al.ElemType); got != nil && !c.assignable(want, got) {
			return
		}
	}
	al.ElemType = want
}

func settledBranchJoin(a, b ast.Type) ast.Type {
	if joined := unifyIfArms(a, b); joined != nil {
		return joined
	}
	return scalarNumericJoin(a, b)
}

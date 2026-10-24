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

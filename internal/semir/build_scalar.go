package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// The first source-loop scalar surface is wrapping i32 arithmetic and scalar
// comparisons. Other integer widths, floats, overloaded operators and
// short-circuit effects require their own explicit verified contracts.
func scalarOp(kind ssa.OpKind) bool {
	switch kind {
	case ssa.OpAdd, ssa.OpSub, ssa.OpMul, ssa.OpEq, ssa.OpNe,
		ssa.OpLt, ssa.OpLe, ssa.OpGt, ssa.OpGe:
		return true
	}
	return false
}

func scalarArithmetic(kind ssa.OpKind) bool {
	return kind == ssa.OpAdd || kind == ssa.OpSub || kind == ssa.OpMul
}

func sourceScalarKind(op string) ssa.OpKind {
	switch op {
	case "+":
		return ssa.OpAdd
	case "-":
		return ssa.OpSub
	case "*":
		return ssa.OpMul
	case "==":
		return ssa.OpEq
	case "!=":
		return ssa.OpNe
	case "<":
		return ssa.OpLt
	case "<=":
		return ssa.OpLe
	case ">":
		return ssa.OpGt
	case ">=":
		return ssa.OpGe
	default:
		return ssa.OpInvalid
	}
}

func (b *builder) scalarBinary(n *ast.Binary) (ssa.Value, error) {
	kind := sourceScalarKind(n.Op)
	if !scalarOp(kind) || n.IsFloat || n.IsStringConcat || n.IsStringCmp || n.IsStringOrd ||
		n.EqCall != nil || n.CmpCall != nil || n.ArithCall != nil {
		return ssa.Value{}, b.errorAt(n.P, "unsupported scalar binary contract: "+n.Op)
	}
	args, types, ended, err := b.exprs([]ast.Expr{n.Left, n.Right})
	if err != nil || ended {
		return ssa.Value{}, err
	}
	i32 := ast.Equal(types[0], ast.NumberType{})
	boolean := ast.Equal(types[0], ast.BoolType{}) && (kind == ssa.OpEq || kind == ssa.OpNe)
	if !ast.Equal(types[0], types[1]) || (!i32 && !boolean) {
		return ssa.Value{}, b.errorAt(n.P, "scalar binary requires matching i32 operands or boolean equality")
	}
	var typ ast.Type = ast.BoolType{}
	if scalarArithmetic(kind) {
		typ = types[0]
	}
	return b.fn.addOp(b.current, kind, typ, n.P, args...), nil
}

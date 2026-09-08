package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// matchValue produces ordered decisions directly from the checked match. It
// never desugars arms into parser temporaries or chooses ownership from syntax.
func (b *builder) matchValue(n *ast.MatchExpr) (ssa.Value, error) {
	tag, err := b.expr(n.Tag)
	if err != nil || tag.ended {
		return ssa.Value{}, err
	}
	typ := b.fn.values[tag.value.ID].typ
	if !ast.Equal(typ, ast.NumberType{}) && !ast.Equal(typ, ast.BoolType{}) {
		return ssa.Value{}, b.errorAt(n.P, "match scrutinee requires an implemented semantic pattern contract")
	}
	if err := b.scalarMatchArms(n); err != nil {
		return ssa.Value{}, err
	}
	var ends []*ssa.Block
	var values []ssa.Value
	for _, arm := range n.Arms {
		var next *ssa.Block
		if !arm.IsWildcard {
			pattern, err := b.expr(arm.Literal)
			if err != nil {
				return ssa.Value{}, err
			}
			if pattern.ended || !ast.Equal(b.fn.values[pattern.value.ID].typ, typ) {
				return ssa.Value{}, b.errorAt(arm.P, "literal pattern differs from the checked scrutinee type")
			}
			cond := b.fn.addOp(b.current, ssa.OpEq, ast.BoolType{}, arm.P, tag.value, pattern.value)
			body := b.fn.graph.NewBlock()
			next = b.fn.graph.NewBlock()
			b.fn.graph.SetBrIf(b.current, cond, body, next)
			b.current = body
			if err := b.seal(body); err != nil {
				return ssa.Value{}, err
			}
		}
		result, err := b.matchArmValue(arm, tag.value, next)
		if err != nil {
			return ssa.Value{}, err
		}
		if !result.ended {
			ends, values = append(ends, b.current), append(values, result.value)
		}
		// Both the pattern-false and guard-false edges are now known. Arm
		// bindings have left scope, but their outer-binding updates survive
		// through the existing sealed-block SSA construction.
		b.current = next
		if next != nil {
			if err := b.seal(next); err != nil {
				return ssa.Value{}, err
			}
		}
	}
	return b.joinValues(ends, values, n.P)
}

func (b *builder) matchArmValue(arm *ast.MatchExprArm, tag ssa.Value, next *ssa.Block) (exprResult, error) {
	b.pushScope()
	defer b.popScope()
	if arm.AtBinding != "" {
		if err := b.bind(arm.AtBinding, b.fn.values[tag.ID].typ, arm.P, tag); err != nil {
			return exprResult{}, err
		}
	}
	if arm.Guard != nil {
		guard, err := b.expr(arm.Guard)
		if err != nil || guard.ended {
			return guard, err
		}
		if !ast.Equal(b.fn.values[guard.value.ID].typ, ast.BoolType{}) {
			return exprResult{}, b.errorAt(arm.P, "match guard is not boolean")
		}
		body := b.fn.graph.NewBlock()
		b.fn.graph.SetBrIf(b.current, guard.value, body, next)
		b.current = body
		if err := b.seal(body); err != nil {
			return exprResult{}, err
		}
	}
	return b.expr(arm.Body)
}

// This bounded scalar surface has the same explicit final wildcard required
// by the native checker. Other pattern contracts must be implemented, not
// silently treated as scalar equality or an unconditional final arm.
func (b *builder) scalarMatchArms(n *ast.MatchExpr) error {
	if n.StructMatch != "" || len(n.Arms) == 0 {
		return b.errorAt(n.P, "unsupported or empty scalar match contract")
	}
	for i, arm := range n.Arms {
		if arm == nil {
			return b.errorAt(n.P, "missing checked match arm")
		}
		if arm.VariantName != "" || arm.VariantModule != "" || arm.EnumName != "" ||
			len(arm.Bindings) != 0 || len(arm.BindingTypes) != 0 || arm.TupleElems != nil ||
			len(arm.Payloads) != 0 || arm.NamedFields || len(arm.FieldNames) != 0 || arm.RangeHi != nil {
			return b.errorAt(arm.P, "unsupported semantic match pattern contract")
		}
		if arm.IsWildcard {
			if i != len(n.Arms)-1 || arm.Guard != nil || arm.Literal != nil || arm.AtBinding != "" {
				return b.errorAt(arm.P, "scalar match requires a final unguarded wildcard")
			}
			continue
		}
		switch literal := arm.Literal.(type) {
		case *ast.NumberLit, *ast.BoolLit:
		case *ast.Unary:
			if _, number := literal.Operand.(*ast.NumberLit); literal.Op != "-" || !number {
				return b.errorAt(arm.P, "unsupported semantic match literal contract")
			}
		default:
			return b.errorAt(arm.P, "unsupported semantic match literal contract")
		}
	}
	if !n.Arms[len(n.Arms)-1].IsWildcard {
		return b.errorAt(n.P, "scalar match requires a final unguarded wildcard")
	}
	return nil
}

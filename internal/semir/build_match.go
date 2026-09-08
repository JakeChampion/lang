package semir

import (
	"slices"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// matchValue produces ordered decisions directly from the checked match. It
// never desugars arms into parser temporaries or chooses ownership from syntax.
func (b *builder) matchValue(n *ast.MatchExpr) (ssa.Value, error) {
	return b.matchFlow(n, true)
}

func (b *builder) matchFlow(n *ast.MatchExpr, wantValue bool) (ssa.Value, error) {
	tag, err := b.expr(n.Tag)
	if err != nil || tag.ended {
		return ssa.Value{}, err
	}
	typ := b.fn.values[tag.value.ID].typ
	refutable, err := b.matchArms(n, typ)
	if err != nil {
		return ssa.Value{}, err
	}
	var ends []*ssa.Block
	var values []ssa.Value
	emit := b.expr
	if !wantValue {
		emit = b.effectResult
	}
	for i, arm := range n.Arms {
		var next *ssa.Block
		if refutable[i] {
			next = b.fn.graph.NewBlock()
		}
		var bindings []matchBinding
		if arm.TupleElems != nil {
			bindings, err = b.tuplePattern(arm.TupleElems, tag.value, next, arm.P)
		} else if !arm.IsWildcard {
			err = b.matchLiteral(arm.Literal, tag.value, next, arm.P)
		}
		if err != nil {
			return ssa.Value{}, err
		}
		result, err := b.matchArm(arm, tag.value, next, bindings, emit)
		if err != nil {
			return ssa.Value{}, err
		}
		if !result.ended {
			ends = append(ends, b.current)
			if wantValue {
				values = append(values, result.value)
			}
		}
		// Both the pattern-false and guard-false edges are now known. Arm
		// bindings have left scope, but their outer-binding updates survive
		// through the existing sealed-block SSA construction.
		b.current = next
		if next != nil {
			if len(next.Preds) == 0 {
				// An irrefutable pattern's guard can terminate before it
				// produces a boolean. No failure path reaches later arms.
				b.fn.graph.Blocks = slices.DeleteFunc(b.fn.graph.Blocks, func(block *ssa.Block) bool { return block == next })
				b.current = nil
				break
			}
			if err := b.seal(next); err != nil {
				return ssa.Value{}, err
			}
		}
	}
	if !wantValue {
		return ssa.Value{}, b.joinControl(ends)
	}
	return b.joinValues(ends, values, n.P)
}

func (b *builder) matchArm(arm *ast.MatchExprArm, tag ssa.Value, next *ssa.Block, bindings []matchBinding, emit func(ast.Expr) (exprResult, error)) (exprResult, error) {
	b.pushScope()
	defer b.popScope()
	if arm.AtBinding != "" {
		if err := b.bind(arm.AtBinding, b.fn.values[tag.ID].typ, arm.P, tag); err != nil {
			return exprResult{}, err
		}
	}
	for _, binding := range bindings {
		if err := b.bind(binding.name, b.fn.values[binding.value.ID].typ, arm.P, binding.value); err != nil {
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
	return emit(arm.Body)
}

func (b *builder) matchLiteral(literal ast.Expr, tag ssa.Value, next *ssa.Block, pos ast.Position) error {
	pattern, err := b.expr(literal)
	if err != nil {
		return err
	}
	if pattern.ended || !ast.Equal(b.fn.values[pattern.value.ID].typ, b.fn.values[tag.ID].typ) {
		return b.errorAt(pos, "literal pattern differs from the checked scrutinee type")
	}
	cond := b.fn.addOp(b.current, ssa.OpEq, ast.BoolType{}, pos, tag, pattern.value)
	body := b.fn.graph.NewBlock()
	b.fn.graph.SetBrIf(b.current, cond, body, next)
	b.current = body
	return b.seal(body)
}

func (b *builder) matchArms(n *ast.MatchExpr, typ ast.Type) ([]bool, error) {
	if tuple, ok := typ.(ast.TupleType); ok {
		return b.tupleMatchArms(n, tuple)
	}
	if !ast.Equal(typ, ast.NumberType{}) && !ast.Equal(typ, ast.BoolType{}) {
		return nil, b.errorAt(n.P, "match scrutinee requires an implemented semantic pattern contract")
	}
	if err := b.scalarMatchArms(n); err != nil {
		return nil, err
	}
	refutable := make([]bool, len(n.Arms))
	for i, arm := range n.Arms {
		refutable[i] = !arm.IsWildcard
	}
	return refutable, nil
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

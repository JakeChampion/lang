package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

type matchBinding struct {
	name  string
	value ssa.Value
}

// tuplePattern keeps projections visible to lifetime analysis. Partial
// bindings are installed only after every test succeeds, never on failure
// edges or in the outer scope used to evaluate literal patterns.
func (b *builder) tuplePattern(elems []ast.TuplePatElem, tag ssa.Value, next *ssa.Block, pos ast.Position) ([]matchBinding, error) {
	typ := b.fn.values[tag.ID].typ.source.(ast.TupleType) // Validated by tuplePatternContract.
	var bindings []matchBinding
	for i, elem := range elems {
		if elem.IsWildcard {
			continue
		}
		field := b.fn.addOp(b.current, ssa.OpTupleGet, typ.Elems[i], pos, tag)
		b.current.Ops[len(b.current.Ops)-1].Imm = int64(i)
		switch {
		case elem.Nested != nil:
			nested, err := b.tuplePattern(elem.Nested, field, next, pos)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, nested...)
		case elem.Literal != nil:
			if err := b.matchLiteral(elem.Literal, field, next, pos); err != nil {
				return nil, err
			}
		default:
			bindings = append(bindings, matchBinding{name: elem.Name, value: field})
		}
	}
	return bindings, nil
}

func (b *builder) tupleMatchArms(n *ast.MatchExpr, typ ast.TupleType) ([]bool, error) {
	if n.StructMatch != "" || len(n.Arms) == 0 {
		return nil, b.errorAt(n.P, "unsupported or empty tuple match contract")
	}
	refutable := make([]bool, len(n.Arms))
	for i, arm := range n.Arms {
		if arm == nil {
			return nil, b.errorAt(n.P, "missing checked match arm")
		}
		if arm.VariantName != "" || arm.VariantModule != "" || arm.EnumName != "" ||
			len(arm.Bindings) != 0 || len(arm.Payloads) != 0 || arm.NamedFields ||
			len(arm.FieldNames) != 0 || arm.Literal != nil || arm.RangeHi != nil {
			return nil, b.errorAt(arm.P, "unsupported semantic tuple match pattern contract")
		}
		if arm.IsWildcard {
			if arm.TupleElems != nil || len(arm.BindingTypes) != 0 || arm.AtBinding != "" {
				return nil, b.errorAt(arm.P, "inconsistent tuple wildcard contract")
			}
		} else {
			var err error
			refutable[i], err = b.tuplePatternContract(arm.TupleElems, arm.BindingTypes, typ, arm.P)
			if err != nil {
				return nil, err
			}
		}
		refutable[i] = refutable[i] || arm.Guard != nil
		if !refutable[i] && i != len(n.Arms)-1 {
			return nil, b.errorAt(arm.P, "tuple match has an arm after an irrefutable pattern")
		}
	}
	if refutable[len(refutable)-1] {
		return nil, b.errorAt(n.P, "tuple match requires a final unguarded irrefutable pattern")
	}
	return refutable, nil
}

func (b *builder) tuplePatternContract(elems []ast.TuplePatElem, types []ast.Type, typ ast.TupleType, pos ast.Position) (bool, error) {
	if elems == nil || len(elems) != len(typ.Elems) || len(types) != len(elems) {
		return false, b.errorAt(pos, "inconsistent checked tuple pattern shape")
	}
	refutable := false
	for i, elem := range elems {
		if b.fn.resolvedType(types[i], false) != nil || !ast.Equal(types[i], typ.Elems[i]) {
			return false, b.errorAt(pos, "tuple pattern metadata differs from its semantic field type")
		}
		if elem.VariantName != "" || elem.VariantModule != "" || elem.IsStruct ||
			len(elem.VariantBindings) != 0 || len(elem.VariantBindingTypes) != 0 ||
			len(elem.VariantFieldNames) != 0 || len(elem.VariantPayloads) != 0 ||
			elem.RangeHi != nil || elem.AtBinding != "" {
			return false, b.errorAt(pos, "unsupported semantic tuple element contract")
		}
		forms := 0
		if elem.Name != "" {
			forms++
		}
		if elem.IsWildcard {
			forms++
		}
		if elem.Literal != nil {
			forms++
		}
		if elem.Nested != nil {
			forms++
		}
		if forms != 1 || (elem.Nested == nil && len(elem.NestedTypes) != 0) {
			return false, b.errorAt(pos, "inconsistent checked tuple element form")
		}
		switch {
		case elem.Nested != nil:
			nestedType, ok := typ.Elems[i].(ast.TupleType)
			if !ok {
				return false, b.errorAt(pos, "nested tuple pattern requires a semantic tuple field")
			}
			nested, err := b.tuplePatternContract(elem.Nested, elem.NestedTypes, nestedType, pos)
			if err != nil {
				return false, err
			}
			refutable = refutable || nested
		case elem.Literal != nil:
			if !ast.Equal(typ.Elems[i], ast.NumberType{}) && !ast.Equal(typ.Elems[i], ast.BoolType{}) {
				return false, b.errorAt(pos, "tuple literal requires an implemented semantic comparison contract")
			}
			switch literal := elem.Literal.(type) {
			case *ast.NumberLit, *ast.BoolLit:
			case *ast.Unary:
				if _, number := literal.Operand.(*ast.NumberLit); literal.Op != "-" || !number {
					return false, b.errorAt(pos, "unsupported semantic tuple literal contract")
				}
			default:
				return false, b.errorAt(pos, "unsupported semantic tuple literal contract")
			}
			refutable = true
		}
	}
	return refutable, nil
}

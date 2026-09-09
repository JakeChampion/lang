package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

func (b *builder) enumValue(expr ast.Expr, construction checker.EnumConstruction) (ssa.Value, error) {
	bad := func() (ssa.Value, error) {
		return ssa.Value{}, b.errorAt(expr.Pos(), "inconsistent checked enum construction contract")
	}
	if err := b.fn.program.importNominalTypes(construction.Type, b.info); err != nil {
		return ssa.Value{}, b.errorAt(expr.Pos(), err.Error())
	}
	if err := b.fn.resolvedType(construction.Type, false); err != nil {
		return ssa.Value{}, b.errorAt(expr.Pos(), err.Error())
	}
	e := b.fn.program.enum(construction.Type)
	index := construction.VariantIndex
	if index < 0 || index >= len(e.variants) {
		return bad()
	}
	variant := e.variants[index]
	var args []ast.Expr
	var name, qualifier string
	switch n := expr.(type) {
	case *ast.Call:
		id, ok := n.Callee.(*ast.Ident)
		if !ok || !n.IsVariantCall || n.DynTrait != "" || len(n.ArgNames) != 0 {
			return bad()
		}
		name, qualifier, args = id.Name, id.EnumName, n.Args
	case *ast.Ident:
		if _, local := b.lookup(n.Name); local && n.EnumName == "" {
			return bad()
		}
		name, qualifier = n.Name, n.EnumName
	case *ast.FieldAccess:
		id, ok := n.Target.(*ast.Ident)
		if !ok {
			return bad()
		}
		name, qualifier = n.Field, id.Name
	default:
		return bad()
	}
	if name != variant.name || qualifier != e.typ.Name || len(args) != variant.count || len(construction.Payloads) != variant.count {
		return bad()
	}
	for i, typ := range construction.Payloads {
		if !ast.Equal(typ, e.fields[variant.first+i].typ) {
			return bad()
		}
	}
	values, _, ended, err := b.exprs(args)
	if err != nil || ended {
		return ssa.Value{}, err
	}
	result := b.fn.addOp(b.current, ssa.OpSumMake, e.typ, expr.Pos(), values...)
	b.current.Ops[len(b.current.Ops)-1].Imm = int64(index)
	return result, nil
}

func (b *builder) enumMatchArms(n *ast.MatchExpr, e *enumContract) ([]bool, error) {
	if n.StructMatch != "" || len(n.Arms) == 0 {
		return nil, b.errorAt(n.P, "unsupported or empty enum match contract")
	}
	covered, count := make([]bool, len(e.variants)), 0
	refutable := make([]bool, len(n.Arms))
	for i, arm := range n.Arms {
		if arm == nil || arm.NamedFields || len(arm.FieldNames) != 0 || arm.TupleElems != nil || arm.Literal != nil || arm.RangeHi != nil || arm.AltCont {
			return nil, b.errorAt(n.P, "unsupported semantic enum pattern contract")
		}
		if arm.IsWildcard {
			if arm.VariantName != "" || arm.EnumName != "" || len(arm.Bindings) != 0 || len(arm.BindingTypes) != 0 || len(arm.Payloads) != 0 {
				return nil, b.errorAt(arm.P, "inconsistent enum wildcard contract")
			}
			refutable[i] = arm.Guard != nil
		} else {
			index := arm.VariantIndex
			if arm.EnumName != e.typ.Name || index < 0 || index >= len(e.variants) || e.variants[index].name != arm.VariantName {
				return nil, b.errorAt(arm.P, "enum pattern differs from its nominal variant identity")
			}
			variant := e.variants[index]
			if len(arm.Bindings) != variant.count || len(arm.BindingTypes) != variant.count || len(arm.Payloads) != 0 && len(arm.Payloads) != variant.count {
				return nil, b.errorAt(arm.P, "inconsistent checked enum payload pattern")
			}
			for j, typ := range arm.BindingTypes {
				if !ast.Equal(typ, e.fields[variant.first+j].typ) || len(arm.Payloads) != 0 && arm.Payloads[j] != nil {
					return nil, b.errorAt(arm.P, "unsupported or inconsistent enum payload pattern")
				}
			}
			if covered[index] {
				return nil, b.errorAt(arm.P, "enum arm follows an unguarded covering variant")
			}
			refutable[i] = count != len(e.variants)-1 || arm.Guard != nil
			if arm.Guard == nil {
				covered[index], count = true, count+1
			}
		}
		if !refutable[i] && i != len(n.Arms)-1 {
			return nil, b.errorAt(arm.P, "enum arm follows an irrefutable pattern")
		}
	}
	if refutable[len(refutable)-1] {
		return nil, b.errorAt(n.P, "enum match lacks complete unguarded variant coverage")
	}
	return refutable, nil
}

func (b *builder) enumPattern(arm *ast.MatchExprArm, container ssa.Value, next *ssa.Block) []matchBinding {
	e := b.fn.program.enum(b.fn.values[container.ID].typ.source)
	variant := e.variants[arm.VariantIndex]
	if next != nil {
		condition := b.fn.addOp(b.current, ssa.OpSumIs, ast.BoolType{}, arm.P, container)
		b.current.Ops[len(b.current.Ops)-1].Imm = int64(arm.VariantIndex)
		body := b.fn.graph.NewBlock()
		b.fn.graph.SetBrIf(b.current, condition, body, next)
		b.current = body
	}
	var bindings []matchBinding
	for i, name := range arm.Bindings {
		if name == "" || name == "_" {
			continue
		}
		index := variant.first + i
		field := b.fn.addOp(b.current, ssa.OpSumGet, e.fields[index].typ, arm.P, container)
		b.current.Ops[len(b.current.Ops)-1].Imm = int64(index)
		bindings = append(bindings, matchBinding{name: name, value: field})
	}
	return bindings
}

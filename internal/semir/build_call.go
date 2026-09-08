package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

// Effect contexts may discard a value or execute a resultless direct call.
// Value contexts still go through expr and must produce a value or terminate.
func (b *builder) effectExpr(expr ast.Expr) error {
	if call, ok := expr.(*ast.Call); ok {
		_, err := b.call(call, true)
		return err
	}
	_, err := b.expr(expr)
	return err
}

func (b *builder) call(n *ast.Call, allowVoid bool) (ssa.Value, error) {
	if intrinsic, ok := b.info.IntrinsicCalls[n]; ok {
		if intrinsic.Kind != checker.IntrinsicArrayAppend || intrinsic.Signature == nil {
			return ssa.Value{}, b.errorAt(n.P, "unsupported or unresolved intrinsic contract")
		}
		args, types, ended, err := b.exprs(n.Args)
		if err != nil || ended {
			return ssa.Value{}, err
		}
		sig := intrinsic.Signature
		if len(args) != len(sig.Params) {
			return ssa.Value{}, b.errorAt(n.P, "intrinsic argument count differs from checked contract")
		}
		for i, typ := range types {
			if !ast.Equal(typ, sig.Params[i]) {
				return ssa.Value{}, b.errorAt(n.P, "intrinsic argument type differs from checked contract")
			}
		}
		return b.fn.addOp(b.current, ssa.OpArrayAppend, sig.Result, n.P, args...), nil
	}
	ident, direct := n.Callee.(*ast.Ident)
	if !direct || n.IsVariantCall || n.DynTrait != "" || len(n.TypeArgs) != 0 || len(n.ArgNames) != 0 {
		return ssa.Value{}, b.errorAt(n.P, "unsupported semantic call form")
	}
	if _, local := b.lookup(ident.Name); local {
		return ssa.Value{}, b.errorAt(n.P, "indirect calls are not implemented in the typed pilot")
	}
	id, ok := b.fn.program.byName[ident.Name]
	if !ok {
		return ssa.Value{}, b.errorAt(n.P, "callee is outside the typed program: "+ident.Name)
	}
	callee := b.fn.program.funcs[id-1]
	_, void := callee.result.(ast.VoidType)
	if void && !allowVoid {
		return ssa.Value{}, b.errorAt(n.P, "void call cannot supply a semantic value")
	}
	args, _, ended, err := b.exprs(n.Args)
	if err != nil || ended {
		return ssa.Value{}, err
	}
	if void {
		op := b.fn.addEffect(b.current, ssa.OpSemanticCall, n.P, args...)
		op.Imm = id
		return ssa.Value{}, nil
	}
	value := b.fn.addOp(b.current, ssa.OpSemanticCall, callee.result, n.P, args...)
	b.current.Ops[len(b.current.Ops)-1].Imm = id
	return value, nil
}

package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

// Effect contexts join control and binding state, not a discarded result.
// Conditions, guards and arguments remain value contexts through expr.
func (b *builder) effectExpr(expr ast.Expr) error {
	switch n := expr.(type) {
	case *ast.Call:
		_, err := b.call(n, true)
		return err
	case *ast.BlockExpr:
		b.pushScope()
		defer b.popScope()
		for _, stmt := range n.Stmts {
			if err := b.stmt(stmt); err != nil {
				return err
			}
		}
		if b.current == nil || n.Tail == nil {
			return nil
		}
		return b.effectExpr(n.Tail)
	case *ast.IfExpr:
		cond, err := b.expr(n.Cond)
		if err != nil || cond.ended {
			return err
		}
		_, err = b.branchFlow(cond.value, n.P, false,
			func() (exprResult, error) { return b.effectResult(n.Then) },
			func() (exprResult, error) { return b.effectResult(n.Else) })
		return err
	case *ast.MatchExpr:
		_, err := b.matchFlow(n, false)
		return err
	}
	_, err := b.expr(expr)
	return err
}

func (b *builder) effectResult(expr ast.Expr) (exprResult, error) {
	err := b.effectExpr(expr)
	return exprResult{ended: b.current == nil}, err
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

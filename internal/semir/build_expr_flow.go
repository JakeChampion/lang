package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// ended means evaluation has no continuation: return/break/continue already
// emitted its edge. No fake SSA value represents never. Every consumer checks
// ended before using value or evaluating another operand.
type exprResult struct {
	value ssa.Value
	ended bool
}

func (b *builder) expr(e ast.Expr) (exprResult, error) {
	if b.current == nil {
		return exprResult{}, fmt.Errorf("semir: attempted expression evaluation after a terminating edge")
	}
	value, err := b.exprValue(e)
	if err != nil {
		return exprResult{}, err
	}
	ended := b.current == nil
	if value.IsValid() == ended {
		return exprResult{}, b.errorAt(e.Pos(), "expression value disagrees with its continuation")
	}
	return exprResult{value, ended}, nil
}

func (b *builder) blockValue(n *ast.BlockExpr) (ssa.Value, error) {
	b.pushScope()
	defer b.popScope()
	for _, stmt := range n.Stmts {
		if err := b.stmt(stmt); err != nil {
			return ssa.Value{}, err
		}
	}
	if b.current == nil {
		return ssa.Value{}, nil
	}
	if n.Tail == nil {
		return ssa.Value{}, b.errorAt(n.P, "value block falls through without a value")
	}
	result, err := b.expr(n.Tail)
	return result.value, err
}

func (b *builder) ifValue(n *ast.IfExpr) (ssa.Value, error) {
	cond, err := b.expr(n.Cond)
	if err != nil || cond.ended {
		return ssa.Value{}, err
	}
	return b.branchValue(cond.value, n.P,
		func() (exprResult, error) { return b.expr(n.Then) },
		func() (exprResult, error) { return b.expr(n.Else) })
}

func (b *builder) shortCircuit(n *ast.Binary) (ssa.Value, error) {
	left, err := b.expr(n.Left)
	if err != nil || left.ended {
		return ssa.Value{}, err
	}
	right := func() (exprResult, error) {
		result, err := b.expr(n.Right)
		if err == nil && !result.ended && !ast.Equal(b.fn.values[result.value.ID].typ, ast.BoolType{}) {
			err = b.errorAt(n.Right.Pos(), "short-circuit operand is not boolean")
		}
		return result, err
	}
	constant := func() (exprResult, error) {
		value := b.fn.addOp(b.current, ssa.OpConstBool, ast.BoolType{}, n.P)
		if n.Op == "||" {
			b.current.Ops[len(b.current.Ops)-1].Imm = 1
		}
		return exprResult{value: value}, nil
	}
	yes, no := right, constant
	if n.Op == "||" {
		yes, no = constant, right
	}
	return b.branchValue(left.value, n.P, yes, no)
}

func (b *builder) booleanNot(n *ast.Unary) (ssa.Value, error) {
	if n.Op != "!" {
		return ssa.Value{}, b.errorAt(n.P, "unsupported unary contract: "+n.Op)
	}
	result, err := b.expr(n.Operand)
	if err != nil || result.ended {
		return ssa.Value{}, err
	}
	if !ast.Equal(b.fn.values[result.value.ID].typ, ast.BoolType{}) {
		return ssa.Value{}, b.errorAt(n.P, "logical negation requires boolean")
	}
	return b.fn.addOp(b.current, ssa.OpNot, ast.BoolType{}, n.P, result.value), nil
}

func (b *builder) branchValue(cond ssa.Value, pos ast.Position, yes, no func() (exprResult, error)) (ssa.Value, error) {
	if !ast.Equal(b.fn.values[cond.ID].typ, ast.BoolType{}) {
		return ssa.Value{}, b.errorAt(pos, "value branch condition is not boolean")
	}
	blocks := []*ssa.Block{b.fn.graph.NewBlock(), b.fn.graph.NewBlock()}
	b.fn.graph.SetBrIf(b.current, cond, blocks[0], blocks[1])
	var ends []*ssa.Block
	var values []ssa.Value
	var typ ast.Type
	for i, arm := range []func() (exprResult, error){yes, no} {
		b.current = blocks[i]
		if err := b.seal(b.current); err != nil {
			return ssa.Value{}, err
		}
		b.pushScope()
		result, err := arm()
		b.popScope()
		if err != nil {
			return ssa.Value{}, err
		}
		if result.ended {
			continue
		}
		armType := b.fn.values[result.value.ID].typ
		if typ != nil && !ast.Equal(typ, armType) {
			return ssa.Value{}, b.errorAt(pos, "value join needs an explicit checked coercion contract")
		}
		typ = armType
		ends, values = append(ends, b.current), append(values, result.value)
	}
	b.current = nil
	if len(ends) == 0 {
		return ssa.Value{}, nil
	}
	b.current = b.fn.graph.NewBlock()
	for _, end := range ends {
		b.fn.graph.SetBr(end, b.current)
	}
	if err := b.seal(b.current); err != nil {
		return ssa.Value{}, err
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return b.fn.addPhi(b.current, typ, pos, values...), nil
}

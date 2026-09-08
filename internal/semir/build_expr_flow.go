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

func (b *builder) scalarUnary(n *ast.Unary) (ssa.Value, error) {
	if n.Op != "!" && n.Op != "-" {
		return ssa.Value{}, b.errorAt(n.P, "unsupported unary contract: "+n.Op)
	}
	result, err := b.expr(n.Operand)
	if err != nil || result.ended {
		return ssa.Value{}, err
	}
	kind := ssa.OpNot
	var typ ast.Type = ast.BoolType{}
	if n.Op == "-" {
		kind, typ = ssa.OpNeg, ast.NumberType{}
	}
	if !ast.Equal(b.fn.values[result.value.ID].typ, typ) {
		return ssa.Value{}, b.errorAt(n.P, "unary operation requires boolean ! or wrapping i32 -")
	}
	return b.fn.addOp(b.current, kind, typ, n.P, result.value), nil
}

func (b *builder) branchValue(cond ssa.Value, pos ast.Position, yes, no func() (exprResult, error)) (ssa.Value, error) {
	return b.branchFlow(cond, pos, true, yes, no)
}

func (b *builder) branchFlow(cond ssa.Value, pos ast.Position, wantValue bool, yes, no func() (exprResult, error)) (ssa.Value, error) {
	if !ast.Equal(b.fn.values[cond.ID].typ, ast.BoolType{}) {
		return ssa.Value{}, b.errorAt(pos, "value branch condition is not boolean")
	}
	blocks := []*ssa.Block{b.fn.graph.NewBlock(), b.fn.graph.NewBlock()}
	b.fn.graph.SetBrIf(b.current, cond, blocks[0], blocks[1])
	var ends []*ssa.Block
	var values []ssa.Value
	for i, arm := range []func() (exprResult, error){yes, no} {
		b.current = blocks[i]
		b.pushScope()
		result, err := arm()
		b.popScope()
		if err != nil {
			return ssa.Value{}, err
		}
		if result.ended {
			continue
		}
		ends = append(ends, b.current)
		if wantValue {
			values = append(values, result.value)
		}
	}
	if !wantValue {
		return ssa.Value{}, b.joinControl(ends)
	}
	return b.joinValues(ends, values, pos)
}

// joinValues is shared by source constructs that select a value. Only live
// edges participate; the unit planner secures reference-bearing phi inputs.
func (b *builder) joinValues(ends []*ssa.Block, values []ssa.Value, pos ast.Position) (ssa.Value, error) {
	if len(ends) != len(values) {
		return ssa.Value{}, b.errorAt(pos, "value join requires one value per live edge")
	}
	var typ ast.Type
	for _, value := range values {
		if !value.IsValid() {
			return ssa.Value{}, b.errorAt(pos, "value join cannot use an effect-only result")
		}
		armType := b.fn.values[value.ID].typ
		if typ != nil && !ast.Equal(typ, armType) {
			return ssa.Value{}, b.errorAt(pos, "value join needs an explicit checked coercion contract")
		}
		typ = armType
	}
	if err := b.joinControl(ends); err != nil {
		return ssa.Value{}, err
	}
	if b.current == nil {
		return ssa.Value{}, nil
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return b.fn.addPhi(b.current, typ, pos, values...), nil
}

// joinControl merges live paths and their binding identities without creating
// a result value. Value-producing constructs add their typed result afterwards.
func (b *builder) joinControl(ends []*ssa.Block) error {
	b.current = nil
	if len(ends) == 0 {
		return nil
	}
	b.current = b.fn.graph.NewBlock()
	for _, end := range ends {
		b.fn.graph.SetBr(end, b.current)
	}
	return nil
}

// Package semir represents typed semantic values before reference-count and
// target-layout lowering. It reuses SSA's CFG, dominance and def-use machinery,
// but keeps the graph private: a semantic function is not a low-level ssa.Func
// that an optimizer or emitter can accidentally accept.
//
// This is an experimental migration boundary, not yet the production lowering
// route. Unsupported types and operations fail verification, never acquire an
// invented scalar representation or positive ownership fact.
package semir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// ParamMode describes entry ownership, not uniqueness. A counted parameter may
// share its allocation with any number of other counted owners.
type ParamMode uint8

const (
	ParamInvalid ParamMode = iota
	ParamValue
	ParamBorrow
	ParamCounted
)

// BindingID identifies a lexical declaration, independently of spelling and
// independently of SSA value identity. Two bindings can name the same value;
// a mutable binding can name different values on different control-flow edges.
type BindingID uint32

type binding struct {
	name string
	typ  ast.Type
	pos  ast.Position
}

type valueInfo struct {
	typ ast.Type
	pos ast.Position
}

// Func deliberately does not expose its SSA graph. Semantic transformations
// live in this package; the eventual lowering boundary must verify and emit a
// separate, fully lowered function rather than handing this graph to codegen.
type Func struct {
	program  *Program
	contract funcContract
	graph    *ssa.Func
	result   ast.Type
	values   map[int32]valueInfo
	// Effect-only operations have source origins but no semantic value.
	effectPositions map[*ssa.Op]ast.Position
	modes           []ParamMode
	bindings        []binding
	cleanups        []*cleanupRegion
	boundaries      []*cleanupBoundary
	cleanupExits    []*cleanupExit
}

func newFunc(name string, result ast.Type) *Func {
	return &Func{graph: ssa.NewFunc(name), result: result, values: make(map[int32]valueInfo)}
}

func (f *Func) addParam(typ ast.Type, mode ParamMode, pos ast.Position) ssa.Value {
	v := f.graph.AddParam()
	f.values[v.ID] = valueInfo{typ: typ, pos: pos}
	f.modes = append(f.modes, mode)
	return v
}

func (f *Func) addBinding(name string, typ ast.Type, pos ast.Position) BindingID {
	f.bindings = append(f.bindings, binding{name: name, typ: typ, pos: pos})
	return BindingID(len(f.bindings))
}

func (f *Func) addOp(b *ssa.Block, kind ssa.OpKind, typ ast.Type, pos ast.Position, args ...ssa.Value) ssa.Value {
	v := f.graph.AddOp(b, kind, args...)
	f.values[v.ID] = valueInfo{typ: typ, pos: pos}
	return v
}

func (f *Func) addPhi(b *ssa.Block, typ ast.Type, pos ast.Position, args ...ssa.Value) ssa.Value {
	v := f.graph.AddPhi(b, args...)
	f.values[v.ID] = valueInfo{typ: typ, pos: pos}
	return v
}

func (f *Func) addEffect(b *ssa.Block, kind ssa.OpKind, pos ast.Position, args ...ssa.Value) *ssa.Op {
	op := f.graph.AddOpNoResult(b, kind, args...)
	if f.effectPositions == nil {
		f.effectPositions = make(map[*ssa.Op]ast.Position)
	}
	f.effectPositions[op] = pos
	return op
}

// A projection is containment, not identity aliasing or an acquired reference.
// Its container and dynamic index are real Op.Args, so SSA def-use and
// dominance see every lifetime dependency. Field is used only for tuples.
type projectionInfo struct {
	Container ssa.Value
	Index     ssa.Value
	Field     int64
}

func projection(op *ssa.Op) (projectionInfo, bool) {
	switch op.Kind {
	case ssa.OpArrayGet:
		if len(op.Args) == 2 {
			return projectionInfo{Container: op.Args[0], Index: op.Args[1]}, true
		}
	case ssa.OpTupleGet:
		if len(op.Args) == 1 {
			return projectionInfo{Container: op.Args[0], Field: op.Imm}, true
		}
	}
	return projectionInfo{}, false
}

package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// ARM64Program is the experimental ARM64 SSA runtime's single-word-string
// ABI, not the flat ARM64 backend's two-word ABI. Functions are freshly lowered
// machine SSA, never the semantic graphs. Positions preserves source origins
// without pretending they are indices into a legacy ir.Func operation list.
type ARM64Program struct {
	Functions map[string]*ssa.Func
	Positions map[*ssa.Op]ast.Position
	// Symbols maps source entry names to disjoint machine symbols. A user
	// function named like an RC runtime helper must not intercept that helper.
	Symbols map[string]string
}

// LowerARM64SSA verifies the whole module's unit plans before lowering any
// concrete RC. Only the typed pilot surface is accepted. This entry point does
// not select the pipeline or call ir.LowerWith; the CLI opts in explicitly.
func LowerARM64SSA(p *Program) (*ARM64Program, error) {
	plans, err := planProgramUnits(p)
	if err != nil {
		return nil, err
	}
	l := &armLowerer{
		out:     &ARM64Program{Functions: make(map[string]*ssa.Func), Positions: make(map[*ssa.Op]ast.Position), Symbols: make(map[string]string)},
		helpers: make(map[string]string),
		names:   make(map[*Func]string),
	}
	for i, f := range p.funcs {
		name := fmt.Sprintf("__semir_fn_%d", i)
		l.names[f], l.out.Symbols[f.graph.Name] = name, name
		l.out.Functions[name] = ssa.NewFunc(name)
	}
	for _, f := range p.funcs {
		if err := l.function(plans[f]); err != nil {
			return nil, err
		}
	}
	for name, f := range l.out.Functions {
		if err := ssa.Verify(f); err != nil {
			return nil, fmt.Errorf("semir ARM64 lowering %s: %w", name, err)
		}
	}
	return l.out, nil
}

type armLowerer struct {
	out     *ARM64Program
	helpers map[string]string
	names   map[*Func]string
}

type armBuilder struct {
	f   *ssa.Func
	b   *ssa.Block
	pos ast.Position
	l   *armLowerer
}

func (b *armBuilder) op(kind ssa.OpKind, width int8, addr bool, args ...ssa.Value) ssa.Value {
	v := b.f.AddOp(b.b, kind, args...)
	op := b.b.Ops[len(b.b.Ops)-1]
	op.Width, op.Addr = width, addr
	b.l.out.Positions[op] = b.pos
	return v
}

func (b *armBuilder) constant(n int64) ssa.Value {
	v := b.op(ssa.OpConstInt, 64, false)
	b.b.Ops[len(b.b.Ops)-1].Imm = n
	return v
}

func (b *armBuilder) call(name string, width int8, addr bool, args ...ssa.Value) ssa.Value {
	v := b.op(ssa.OpCall, width, addr, args...)
	b.b.Ops[len(b.b.Ops)-1].Str = name
	return v
}

func (b *armBuilder) offset(ptr ssa.Value, offset int64) ssa.Value {
	return b.op(ssa.OpAdd, 64, true, ptr, b.constant(offset))
}

func (b *armBuilder) load(ptr ssa.Value, offset int64, typ ast.Type) ssa.Value {
	kind := ssa.OpLoad
	if n, ok := typ.(ast.NumberType); ok {
		switch n.NormalWidth() {
		case 8:
			kind = ssa.OpLoad8U
			if n.IsSigned() {
				kind = ssa.OpLoad8S
			}
		case 16:
			kind = ssa.OpLoad16U
			if n.IsSigned() {
				kind = ssa.OpLoad16S
			}
		case 32:
			kind = ssa.OpLoad32U
		}
	} else if _, ok := typ.(ast.BoolType); ok {
		kind = ssa.OpLoad32U
	}
	w, addr := armValueShape(typ)
	v := b.op(kind, w, addr, ptr)
	b.b.Ops[len(b.b.Ops)-1].Imm = offset
	return v
}

func (b *armBuilder) store(ptr ssa.Value, offset int64, value ssa.Value, typ ast.Type) {
	kind := ssa.OpStore
	if n, ok := typ.(ast.NumberType); ok {
		switch n.NormalWidth() {
		case 8:
			kind = ssa.OpStore8
		case 16:
			kind = ssa.OpStore16
		case 32:
			kind = ssa.OpStore32
		}
	} else if _, ok := typ.(ast.BoolType); ok {
		kind = ssa.OpStore32
	}
	op := b.f.AddOpNoResult(b.b, kind, ptr, value)
	op.Imm = offset
	b.l.out.Positions[op] = b.pos
}

func armValueShape(typ ast.Type) (int8, bool) {
	if referenceBearing(typ) {
		return 64, true
	}
	if n, ok := typ.(ast.NumberType); ok {
		if n.Width == ast.WidthPtr || n.NormalWidth() == 64 || (n.NormalWidth() == 32 && !n.IsSigned()) {
			return 64, false
		}
	}
	return 32, false
}

func armParam(f *ssa.Func, typ ast.Type) ssa.Value {
	w, addr := armValueShape(typ)
	f.ParamWidths = append(f.ParamWidths, w)
	f.ParamAddrs = append(f.ParamAddrs, addr)
	return f.AddParam()
}

func (l *armLowerer) function(plan *functionUnits) error {
	src := plan.function
	f := l.out.Functions[l.names[src]]
	f.ReturnWidth, f.ReturnAddr = armValueShape(src.result)
	values := make(map[int32]ssa.Value)
	// Availability is an unboxed sum in machine IR. The primary lane is its
	// presence flag; the payload lane is meaningful only when that flag is true.
	payloads := make(map[int32]ssa.Value)
	blocks := make(map[*ssa.Block]*ssa.Block)
	edges := make(map[flowEdge]*ssa.Block)
	for _, v := range src.graph.Params {
		values[v.ID] = armParam(f, src.values[v.ID].typ.source)
	}
	for _, block := range src.graph.Blocks {
		if !plan.lifetime.reachable[block] {
			continue
		}
		blocks[block] = f.NewBlock()
		for _, op := range block.Ops {
			if op.Result.IsValid() {
				values[op.Result.ID] = f.NewValue()
				if src.values[op.Result.ID].typ.form == availabilityForm {
					payloads[op.Result.ID] = f.NewValue()
				}
			}
		}
	}
	f.Entry = blocks[src.graph.Entry]
	var laneDemand map[int32]uint8
	if len(payloads) != 0 {
		laneDemand = availabilityLaneDemand(src)
	}
	for _, block := range src.graph.Blocks {
		if blocks[block] == nil {
			continue
		}
		for _, to := range block.Succs() {
			edge := flowEdge{block, to}
			if edges[edge] == nil {
				edges[edge] = f.NewBlock()
			}
		}
	}
	fixups := make(map[int32]ssa.Value)
	for _, block := range src.graph.Blocks {
		if blocks[block] == nil {
			continue
		}
		b := &armBuilder{f: f, b: blocks[block], l: l}
		for _, v := range plan.entry[block] {
			b.drop(values[v.ID], src.values[v.ID].typ.source)
		}
		for _, op := range block.Ops {
			b.pos = src.values[op.Result.ID].pos
			if !op.Result.IsValid() {
				b.pos = src.effectPositions[op]
			}
			args := make([]ssa.Value, len(op.Args))
			for i, a := range op.Args {
				args[i] = values[a.ID]
			}
			if op.Kind != ssa.OpPhi {
				b.acquire(plan.ops[op], values)
			}
			var v ssa.Value
			var err error
			payload := payloads[op.Result.ID]
			typ := src.values[op.Result.ID].typ
			lanes := laneDemand[op.Result.ID]
			switch op.Kind {
			case ssa.OpStateAbsent, ssa.OpStatePresent:
				// Direct Present/Get pairs need only the payload lane. There
				// is no machine flag use unless Has or a state phi consumes it.
				if lanes&statePresenceLane != 0 {
					v = b.op(ssa.OpConstBool, 32, false)
				}
				if op.Kind == ssa.OpStatePresent {
					if v.IsValid() {
						b.b.Ops[len(b.b.Ops)-1].Imm = 1
					}
				}
				payloads[op.Result.ID] = ssa.Value{}
				if lanes&statePayloadLane != 0 && op.Kind == ssa.OpStatePresent {
					payloads[op.Result.ID] = args[0]
				} else if lanes&statePayloadLane != 0 {
					// This is an inactive machine lane, not a semantic T payload.
					width, addr := armValueShape(typ.source)
					payloads[op.Result.ID] = b.op(ssa.OpConstInt, width, addr)
				}
			case ssa.OpStateHas:
				v = args[0]
			case ssa.OpStateGet:
				v = payloads[op.Args[0].ID]
			case ssa.OpPhi:
				if typ.form == availabilityForm {
					if lanes&statePresenceLane != 0 {
						v = b.phi(ast.BoolType{})
					}
					payloads[op.Result.ID] = ssa.Value{}
					if lanes&statePayloadLane != 0 {
						payloads[op.Result.ID] = b.phi(typ.source)
					}
				} else {
					v = b.phi(typ.source)
				}
			default:
				v, err = b.semanticOp(src, op, args)
			}
			if err != nil {
				return err
			}
			if payload.IsValid() && payloads[op.Result.ID].IsValid() {
				fixups[payload.ID] = payloads[op.Result.ID]
			}
			if op.Result.IsValid() {
				if v.IsValid() {
					fixups[values[op.Result.ID].ID] = v
				}
				values[op.Result.ID] = v
			}
			for _, drop := range plan.ops[op].drops {
				b.drop(values[drop.ID], src.values[drop.ID].typ.source)
			}
		}
		switch block.Term.Kind {
		case ssa.TermRet:
			step := plan.returns[block]
			b.acquire(step, values)
			for _, v := range step.drops {
				b.drop(values[v.ID], src.values[v.ID].typ.source)
			}
			f.SetRet(b.b, values[block.Term.Value.ID])
		case ssa.TermBr:
			f.SetBr(b.b, edges[flowEdge{block, block.Term.Target}])
		case ssa.TermBrIf:
			f.SetBrIf(b.b, values[block.Term.Cond.ID], edges[flowEdge{block, block.Term.True}], edges[flowEdge{block, block.Term.False}])
		}
	}
	for _, block := range src.graph.Blocks {
		if blocks[block] == nil {
			continue
		}
		for _, succ := range block.Succs() {
			edge := flowEdge{block, succ}
			b := &armBuilder{f: f, b: edges[edge], l: l}
			if b.b.Term.Kind != ssa.TermInvalid {
				continue
			}
			b.acquire(plan.edges[edge], values)
			for _, v := range plan.edges[edge].drops {
				b.drop(values[v.ID], src.values[v.ID].typ.source)
			}
			f.SetBr(b.b, blocks[succ])
		}
	}
	// Phi operands correspond to the actual split predecessor edges, not
	// the source block order or a map's iteration order.
	var physicalPhis map[int32]*ssa.Op
	if len(payloads) != 0 {
		physicalPhis = make(map[int32]*ssa.Op)
		for _, block := range blocks {
			for _, op := range block.Ops {
				if op.Kind != ssa.OpPhi {
					break
				}
				physicalPhis[op.Result.ID] = op
			}
		}
	}
	for old, block := range blocks {
		for i, op := range old.Ops {
			if op.Kind != ssa.OpPhi {
				continue
			}
			var args, payloadArgs []ssa.Value
			for _, pred := range block.Preds {
				for j, oldPred := range old.Preds {
					if edges[flowEdge{oldPred, old}] == pred {
						args = append(args, values[op.Args[j].ID])
						if payloads[op.Result.ID].IsValid() {
							payloadArgs = append(payloadArgs, payloads[op.Args[j].ID])
						}
						break
					}
				}
			}
			if physicalPhis == nil {
				block.Ops[i].Args = args
			} else {
				if phi := physicalPhis[values[op.Result.ID].ID]; phi != nil {
					phi.Args = args
				}
				if phi := physicalPhis[payloads[op.Result.ID].ID]; phi != nil {
					phi.Args = payloadArgs
				}
			}
		}
	}
	// State construction/extraction aliases can cross forward block references.
	// Canonicalize the finite alias graph once before rewriting operands.
	for id := range fixups {
		if len(payloads) == 0 {
			break // Ordinary one-lane lowering already has direct fixups.
		}
		at := fixups[id]
		path := []int32{id}
		for {
			next, exists := fixups[at.ID]
			if !exists {
				break
			}
			if len(path) > len(fixups) {
				return fmt.Errorf("semir %s: cyclic physical value aliases", src.graph.Name)
			}
			path = append(path, at.ID)
			at = next
		}
		for _, old := range path {
			fixups[old] = at
		}
	}
	rewrite := func(v ssa.Value) ssa.Value {
		if n, ok := fixups[v.ID]; ok {
			return n
		}
		return v
	}
	for _, block := range f.Blocks {
		for _, op := range block.Ops {
			for i, arg := range op.Args {
				op.Args[i] = rewrite(arg)
			}
		}
		block.Term.Value = rewrite(block.Term.Value)
		block.Term.Cond = rewrite(block.Term.Cond)
	}
	return nil
}

func (b *armBuilder) acquire(step unitStep, values map[int32]ssa.Value) {
	for _, supply := range step.supplies {
		if supply.mode == unitRetain {
			b.call("__fern_rc_inc", 64, true, values[supply.value.ID])
		}
	}
	// Moves are SSA value flow, not physical RC operations. copyElements is
	// implemented inside the typed append helper before storing each child.
}

func (b *armBuilder) semanticOp(src *Func, op *ssa.Op, args []ssa.Value) (ssa.Value, error) {
	typ := src.values[op.Result.ID].typ.source
	w, addr := armValueShape(typ)
	if scalarOp(op.Kind) || op.Kind == ssa.OpNot || op.Kind == ssa.OpNeg {
		return b.op(op.Kind, w, addr, args...), nil
	}
	switch op.Kind {
	case ssa.OpConstInt, ssa.OpConstBool, ssa.OpConstString:
		v := b.op(op.Kind, w, addr)
		last := b.b.Ops[len(b.b.Ops)-1]
		last.Imm, last.Str = op.Imm, op.Str
		return v, nil
	case ssa.OpSemanticCall:
		callee, err := src.callee(op)
		if err != nil {
			return ssa.Value{}, err
		}
		if !op.Result.IsValid() {
			call := b.f.AddOpNoResult(b.b, ssa.OpCall, args...)
			call.Str = b.l.names[callee]
			b.l.out.Positions[call] = b.pos
			return ssa.Value{}, nil
		}
		return b.call(b.l.names[callee], w, addr, args...), nil
	case ssa.OpArrayMake:
		elem := typ.(ast.ArrayType).Elem
		stride := armElementBytes(elem)
		if int64(len(args)) > (maxArmAllocation-16)/stride {
			return ssa.Value{}, fmt.Errorf("semir: array literal allocation too large")
		}
		data := b.array(b.constant(int64(len(args))), stride)
		for i, v := range args {
			b.store(data, int64(i)*stride, v, elem)
		}
		return data, nil
	case ssa.OpArrayGet:
		stride := armElementBytes(typ)
		index := args[1]
		n := src.values[op.Args[1].ID].typ.source.(ast.NumberType)
		if n.NormalWidth() <= 32 && n.Width != ast.WidthPtr {
			kind := ssa.OpExtendU
			if n.IsSigned() {
				kind = ssa.OpExtendS
			}
			index = b.op(kind, 64, false, index)
		}
		ptr := b.call(b.l.indexHelper(stride), 64, true, args[0], index)
		return b.load(ptr, 0, typ), nil
	case ssa.OpArrayAppend:
		return b.call(b.l.appendHelper(typ.(ast.ArrayType).Elem), 64, true, args...), nil
	case ssa.OpTupleMake:
		tuple := typ.(ast.TupleType)
		offsets, size := armTupleLayout(tuple)
		if size > maxArmAllocation-8 {
			return ssa.Value{}, fmt.Errorf("semir: tuple allocation too large")
		}
		base := b.op(ssa.OpAlloc, 64, true, b.constant(size+8))
		b.store(base, 0, b.constant(1), armI32)
		b.store(base, 4, b.constant(size), armI32)
		data := b.offset(base, 8)
		for i, v := range args {
			b.store(data, offsets[i], v, tuple.Elems[i])
		}
		return data, nil
	case ssa.OpTupleGet:
		offsets, _ := armTupleLayout(src.values[op.Args[0].ID].typ.source.(ast.TupleType))
		return b.load(args[0], offsets[op.Imm], typ), nil
	default:
		return ssa.Value{}, fmt.Errorf("semir ARM64: unsupported operation %s", op.Kind)
	}
}

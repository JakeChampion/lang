package ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ir"
)

// LiftFromIR converts a legacy ir.Func into SSA form. The
// supported subset grows incrementally; each follow-up PR
// extends the switch + adds tests for the newly handled ops.
//
// Supported (cumulative):
//
//   Phase 1:
//   - OpConstI32 / OpConstI64 → OpConstInt
//   - OpAdd / OpSub / OpMul → matching SSA op
//   - OpReturn → ret <value>
//   - OpReturnVoid → ret
//
//   Phase 2:
//   - Function params — minted via AddParam, addressed by
//     OpLoadLocal at slot indices [0, len(in.Params))
//   - OpLoadLocal for param slots only (non-param locals
//     still rejected — they need phi insertion)
//
//   Phase 3a:
//   - OpDivS / OpRemS → OpDiv / OpRem
//   - OpAnd / OpOr / OpXor → matching SSA op
//   - OpShl / OpShrS → OpShl / OpShr
//   - OpNot → OpNot
//   - OpEq / OpNe / OpLtS / OpLeS / OpGtS / OpGeS → matching SSA cmp
//
//   Phase 4:
//   - OpCallDirect → OpCall with Str = callee name, Args = the
//     popped arguments.
//
//   Phase 5:
//   - OpConstStr → OpConstString with Str = string literal.
//
//   Phase 6:
//   - OpConstF32 / OpConstF64 → OpConstFloat (F64 carries the value)
//   - OpFAdd / OpFSub / OpFMul / OpFDiv → matching SSA float op
//   - OpFNeg → OpFNeg
//   - OpFEq / OpFNe / OpFLt / OpFLe / OpFGt / OpFGe → matching SSA fcmp
//
//   Phase 7:
//   - OpStoreLocal / OpTeeLocal / OpLoadLocal for the non-param
//     slot range.
//   - OpDrop pops the top stack value without emitting an SSA op.
//
//   Phase 8a:
//   - OpIf / OpElse / OpEnd for if/else control flow, BlockTypeVoid
//     only. Creates a diamond CFG (then, else, post); phi nodes
//     synthesised at the merge for any local slot whose value
//     differs between the two arms. Nested ifs are fine — the
//     scope stack handles them. OpReturn inside either arm is
//     also fine — the arm just doesn't flow into the merge.
//
//   Phase 8b:
//   - OpIf with BlockTypeI32 / BlockTypeI64 / BlockTypeF32 /
//     BlockTypeF64 — the if is an expression. Both arms push
//     exactly one value before their closing OpElse/OpEnd; the
//     two values are merged via a phi at postB and pushed back
//     onto the operand stack. Requires both arms (no
//     OpElse-less form for non-void blocks).
//
//   Phase 9:
//   - OpBlock (BlockTypeVoid only) opens a forward-only labelled
//     scope; OpEnd closes it. Without an OpBr inside, the lift
//     emits `br fall-through-block` and switches cur to it —
//     functionally a no-op CFG-wise but establishes the
//     scope-stack machinery that OpBr/OpBrIf will use to find
//     their target. Non-void OpBlock + OpBr/OpBrIf land in
//     follow-up PRs.
//
//   Phase 9b:
//   - OpBr to an enclosing OpBlock scope. The current block's
//     terminator becomes `br target.postB`; the slot snapshot
//     + (for non-void scopes) the popped stack-top become a
//     branch source on the target scope, merged via phi at
//     scope close. cur is set to nil after the OpBr — subsequent
//     ops up to the matching OpEnd are unreachable and skipped
//     by the per-handler `if cur == nil` guard.
//
//   Phase 10:
//   - OpBrIf to an enclosing OpBlock scope. The current block's
//     terminator becomes `brif cond, target.postB, fallthrough`;
//     a new fallthrough block becomes the active cur. The branch
//     source captures slots at the OpBrIf site for the merge phi
//     at scope close.
//
//   Phase 10c:
//   - OpBr / OpBrIf may target an enclosing OpIf scope (in
//     addition to OpBlock). The endIfScope merge already
//     iterates brSources so the only change is dropping the
//     "OpBlock only" reject path.
//
// Anything else returns an `unsupported op` error. OpBlock /
// OpLoop / OpBr / OpBrIf, indirect calls, and the conversion
// ops land in follow-up PRs.
//
// The legacy IR is a stack-machine encoding: every Op consumes
// its operand-stack inputs and pushes its result. The lift
// maintains a runtime stack of SSA Values mirroring that
// shape — pop N for an N-arg op, push the new Result.
func LiftFromIR(in *ir.Func) (*Func, error) {
	if in == nil {
		return nil, fmt.Errorf("ssa.LiftFromIR: nil func")
	}

	l := &lifter{
		in:  in,
		out: NewFunc(in.Name),
	}

	// Slots: a flat array indexed by OpLoadLocal/OpStoreLocal's
	// I32 immediate. Slots [0, len(Params)) are parameters,
	// minted up front via AddParam. Slots [len(Params),
	// len(Params)+len(Locals)+len(ScratchTypes)) are non-param
	// locals + scratches — these start uninitialised (the
	// initial Value is the zero sentinel) and get filled in by
	// OpStoreLocal / OpTeeLocal as the lift walks the op list.
	// Reading an uninitialised slot is a hard error.
	totalSlots := len(in.Params) + len(in.Locals) + len(in.ScratchTypes)
	l.slots = make([]Value, totalSlots)
	for i := range in.Params {
		l.slots[i] = l.out.AddParam()
	}

	l.cur = l.out.NewBlock()

	for i, op := range in.Ops {
		if err := l.handle(i, op); err != nil {
			return nil, err
		}
		// Bail early only when both the function is terminated
		// (cur == nil) AND every scope is closed (no more
		// OpElse/OpEnd to process). Otherwise keep walking so
		// scope-closers can run.
		if l.cur == nil && len(l.scopes) == 0 {
			return l.out, nil
		}
	}

	if len(l.scopes) > 0 {
		return nil, fmt.Errorf("ssa.LiftFromIR: %d unclosed scope(s) at end of function", len(l.scopes))
	}
	// Implicit void return at function end — only if execution
	// can still reach here. If cur is nil, every path already
	// terminated via OpReturn/OpReturnVoid.
	if l.cur != nil {
		l.out.SetRet(l.cur, Value{})
	}
	return l.out, nil
}

type lifter struct {
	in  *ir.Func
	out *Func

	// Active block: where AddOp emits next. Changes on
	// OpIf/OpElse/OpEnd. nil after OpReturn/OpReturnVoid.
	cur *Block

	// Operand stack — mirrors the legacy IR's stack-machine
	// shape. Values pushed/popped by each Op.
	stack []Value

	// Slot array — addressed by OpLoadLocal/OpStoreLocal/OpTeeLocal.
	slots []Value

	// Control-flow scope stack. Top is the innermost open
	// OpIf / (future: OpBlock / OpLoop).
	scopes []scope
}

// scope describes one open control-flow construct.
type scope struct {
	kind ir.OpKind // ir.OpIf or ir.OpBlock (ir.OpLoop in later PRs).

	thenB *Block
	elseB *Block
	postB *Block

	// Slot snapshots used to build the merge phis.
	preSlots  []Value // slots state entering the scope
	thenSlots []Value // captured at OpElse; slots after the then arm
	sawElse   bool

	// Value semantics. If blockType != BlockTypeVoid, the if is
	// an expression: each arm must push one value at its end,
	// merged via a phi at postB. thenStackTop is captured at
	// OpElse; the else's top is read at OpEnd.
	blockType    int32
	thenStackTop Value
	stackHeight  int // entry stack height (used to slice off the arm's pushed value)

	// Branch sources — every OpBr/OpBrIf targeting this scope
	// adds one. Merged with the fall-through (and, for OpIf,
	// with the arm-end states) at scope close.
	brSources []brSource
}

// brSource captures the state at one branch site into a scope.
type brSource struct {
	block    *Block  // the block whose terminator branches to scope.postB
	slots    []Value // slot snapshot at the branch site
	stackTop Value   // for non-void scopes, the popped stack-top value
}

// mergeSource is one input to a phi merge at a scope's postB.
// Tracks per-slot snapshot + (for value-producing scopes) the
// stack-top value that the merge phi needs.
type mergeSource struct {
	slots    []Value
	stackTop Value
}

func (l *lifter) handle(i int, op ir.Op) error {
	// OpEnd / OpElse manage the scope stack — they must run even
	// when cur is nil (an OpBr or OpReturn earlier in the arm).
	// Every other op handler bails when cur is nil.
	if l.cur == nil {
		switch op.Kind {
		case ir.OpEnd, ir.OpElse:
		default:
			return nil
		}
	}
	switch op.Kind {
	case ir.OpConstI32:
		v := l.out.AddOp(l.cur, OpConstInt)
		l.cur.Ops[len(l.cur.Ops)-1].Imm = int64(op.I32)
		l.stack = append(l.stack, v)
	case ir.OpConstI64:
		v := l.out.AddOp(l.cur, OpConstInt)
		l.cur.Ops[len(l.cur.Ops)-1].Imm = op.I64
		l.stack = append(l.stack, v)
	case ir.OpConstStr:
		v := l.out.AddOp(l.cur, OpConstString)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str
		l.stack = append(l.stack, v)
	case ir.OpConstF32:
		v := l.out.AddOp(l.cur, OpConstFloat)
		l.cur.Ops[len(l.cur.Ops)-1].F64 = float64(op.F32)
		l.stack = append(l.stack, v)
	case ir.OpConstF64:
		v := l.out.AddOp(l.cur, OpConstFloat)
		l.cur.Ops[len(l.cur.Ops)-1].F64 = op.F64
		l.stack = append(l.stack, v)
	case ir.OpLoadLocal:
		idx := int(op.I32)
		if idx < 0 || idx >= len(l.slots) {
			return fmt.Errorf("ssa.LiftFromIR: OpLoadLocal at op[%d] slot %d out of range (have %d slots)",
				i, idx, len(l.slots))
		}
		v := l.slots[idx]
		if !v.IsValid() {
			return fmt.Errorf("ssa.LiftFromIR: OpLoadLocal at op[%d] reads uninitialised slot %d", i, idx)
		}
		l.stack = append(l.stack, v)
	case ir.OpStoreLocal:
		idx := int(op.I32)
		if idx < 0 || idx >= len(l.slots) {
			return fmt.Errorf("ssa.LiftFromIR: OpStoreLocal at op[%d] slot %d out of range (have %d slots)",
				i, idx, len(l.slots))
		}
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpStoreLocal at op[%d] needs 1 operand", i)
		}
		l.slots[idx] = l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
	case ir.OpTeeLocal:
		idx := int(op.I32)
		if idx < 0 || idx >= len(l.slots) {
			return fmt.Errorf("ssa.LiftFromIR: OpTeeLocal at op[%d] slot %d out of range (have %d slots)",
				i, idx, len(l.slots))
		}
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpTeeLocal at op[%d] needs 1 operand", i)
		}
		l.slots[idx] = l.stack[len(l.stack)-1]
	case ir.OpAdd, ir.OpSub, ir.OpMul,
		ir.OpDivS, ir.OpRemS,
		ir.OpAnd, ir.OpOr, ir.OpXor,
		ir.OpShl, ir.OpShrS,
		ir.OpEq, ir.OpNe,
		ir.OpLtS, ir.OpLeS, ir.OpGtS, ir.OpGeS,
		ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv,
		ir.OpFEq, ir.OpFNe, ir.OpFLt, ir.OpFLe, ir.OpFGt, ir.OpFGe:
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: %v at op[%d] needs 2 operands, stack has %d",
				op.Kind, i, len(l.stack))
		}
		rhs := l.stack[len(l.stack)-1]
		lhs := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		kind := mapBinaryArith(op.Kind)
		v := l.out.AddOp(l.cur, kind, lhs, rhs)
		l.stack = append(l.stack, v)
	case ir.OpNot:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpNot at op[%d] needs 1 operand", i)
		}
		arg := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		v := l.out.AddOp(l.cur, OpNot, arg)
		l.stack = append(l.stack, v)
	case ir.OpFNeg:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpFNeg at op[%d] needs 1 operand", i)
		}
		arg := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		v := l.out.AddOp(l.cur, OpFNeg, arg)
		l.stack = append(l.stack, v)
	case ir.OpDrop:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpDrop at op[%d] needs 1 operand", i)
		}
		l.stack = l.stack[:len(l.stack)-1]
	case ir.OpCallDirect:
		argc := int(op.I32)
		if len(l.stack) < argc {
			return fmt.Errorf("ssa.LiftFromIR: OpCallDirect at op[%d] needs %d args, stack has %d",
				i, argc, len(l.stack))
		}
		args := append([]Value(nil), l.stack[len(l.stack)-argc:]...)
		l.stack = l.stack[:len(l.stack)-argc]
		result := l.out.AddOp(l.cur, OpCall, args...)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str
		l.stack = append(l.stack, result)
	case ir.OpIf:
		switch op.I32 {
		case ir.BlockTypeVoid,
			ir.BlockTypeI32, ir.BlockTypeI64,
			ir.BlockTypeF32, ir.BlockTypeF64:
		default:
			return fmt.Errorf("ssa.LiftFromIR: OpIf at op[%d] unknown BlockType %d", i, op.I32)
		}
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpIf at op[%d] needs cond", i)
		}
		cond := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		thenB := l.out.NewBlock()
		elseB := l.out.NewBlock()
		postB := l.out.NewBlock()
		l.out.SetBrIf(l.cur, cond, thenB, elseB)
		l.scopes = append(l.scopes, scope{
			kind:        ir.OpIf,
			thenB:       thenB,
			elseB:       elseB,
			postB:       postB,
			preSlots:    append([]Value(nil), l.slots...),
			blockType:   op.I32,
			stackHeight: len(l.stack),
		})
		l.cur = thenB
	case ir.OpBlock:
		if op.I32 != ir.BlockTypeVoid {
			return fmt.Errorf("ssa.LiftFromIR: OpBlock at op[%d] non-void BlockType %d not yet supported", i, op.I32)
		}
		postB := l.out.NewBlock()
		l.scopes = append(l.scopes, scope{
			kind:        ir.OpBlock,
			postB:       postB,
			preSlots:    append([]Value(nil), l.slots...),
			blockType:   op.I32,
			stackHeight: len(l.stack),
		})
	case ir.OpElse:
		if len(l.scopes) == 0 {
			return fmt.Errorf("ssa.LiftFromIR: OpElse at op[%d] with no open scope", i)
		}
		top := &l.scopes[len(l.scopes)-1]
		if top.kind != ir.OpIf {
			return fmt.Errorf("ssa.LiftFromIR: OpElse at op[%d] doesn't match an if scope", i)
		}
		if top.sawElse {
			return fmt.Errorf("ssa.LiftFromIR: OpElse at op[%d] is the second else for this if", i)
		}
		top.sawElse = true
		if l.cur != nil {
			// Then-arm fell through. For value-producing ifs,
			// snapshot the pushed value before resetting.
			if top.blockType != ir.BlockTypeVoid {
				if len(l.stack) != top.stackHeight+1 {
					return fmt.Errorf("ssa.LiftFromIR: OpElse at op[%d] then-arm produced %d values, want 1",
						i, len(l.stack)-top.stackHeight)
				}
				top.thenStackTop = l.stack[len(l.stack)-1]
				l.stack = l.stack[:len(l.stack)-1]
			}
			l.out.SetBr(l.cur, top.postB)
			top.thenSlots = append([]Value(nil), l.slots...)
		} else {
			// Then-arm exited via OpBr/OpReturn — no fall-through
			// merge source. Leave thenSlots nil as sentinel.
			top.thenSlots = nil
		}
		l.slots = append([]Value(nil), top.preSlots...)
		l.cur = top.elseB
	case ir.OpEnd:
		if len(l.scopes) == 0 {
			return fmt.Errorf("ssa.LiftFromIR: OpEnd at op[%d] with no open scope", i)
		}
		top := l.scopes[len(l.scopes)-1]
		l.scopes = l.scopes[:len(l.scopes)-1]
		switch top.kind {
		case ir.OpIf:
			if err := l.endIfScope(top); err != nil {
				return err
			}
		case ir.OpBlock:
			l.endBlockScope(top)
		default:
			return fmt.Errorf("ssa.LiftFromIR: OpEnd at op[%d] for unsupported scope kind %v", i, top.kind)
		}
	case ir.OpBr:
		depth := int(op.I32)
		if depth < 0 || depth >= len(l.scopes) {
			return fmt.Errorf("ssa.LiftFromIR: OpBr at op[%d] depth %d out of range (have %d scopes)",
				i, depth, len(l.scopes))
		}
		target := &l.scopes[len(l.scopes)-1-depth]
		if target.kind != ir.OpBlock && target.kind != ir.OpIf {
			return fmt.Errorf("ssa.LiftFromIR: OpBr at op[%d] targets scope kind %v; only OpBlock/OpIf supported",
				i, target.kind)
		}
		var stackTop Value
		if target.blockType != ir.BlockTypeVoid {
			if len(l.stack) < 1 {
				return fmt.Errorf("ssa.LiftFromIR: OpBr at op[%d] needs 1 value (target is non-void scope)", i)
			}
			stackTop = l.stack[len(l.stack)-1]
		}
		target.brSources = append(target.brSources, brSource{
			block:    l.cur,
			slots:    append([]Value(nil), l.slots...),
			stackTop: stackTop,
		})
		l.out.SetBr(l.cur, target.postB)
		l.cur = nil
	case ir.OpBrIf:
		depth := int(op.I32)
		if depth < 0 || depth >= len(l.scopes) {
			return fmt.Errorf("ssa.LiftFromIR: OpBrIf at op[%d] depth %d out of range (have %d scopes)",
				i, depth, len(l.scopes))
		}
		target := &l.scopes[len(l.scopes)-1-depth]
		if target.kind != ir.OpBlock && target.kind != ir.OpIf {
			return fmt.Errorf("ssa.LiftFromIR: OpBrIf at op[%d] targets scope kind %v; only OpBlock/OpIf supported",
				i, target.kind)
		}
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpBrIf at op[%d] needs cond", i)
		}
		cond := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		var stackTop Value
		if target.blockType != ir.BlockTypeVoid {
			if len(l.stack) < 1 {
				return fmt.Errorf("ssa.LiftFromIR: OpBrIf at op[%d] needs 1 value (target is non-void scope)", i)
			}
			stackTop = l.stack[len(l.stack)-1]
		}
		fallthroughB := l.out.NewBlock()
		target.brSources = append(target.brSources, brSource{
			block:    l.cur,
			slots:    append([]Value(nil), l.slots...),
			stackTop: stackTop,
		})
		l.out.SetBrIf(l.cur, cond, target.postB, fallthroughB)
		l.cur = fallthroughB
	case ir.OpReturn:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpReturn at op[%d] needs 1 operand", i)
		}
		l.out.SetRet(l.cur, l.stack[len(l.stack)-1])
		l.cur = nil
	case ir.OpReturnVoid:
		l.out.SetRet(l.cur, Value{})
		l.cur = nil
	default:
		return fmt.Errorf("ssa.LiftFromIR: unsupported op %v at index %d", op.Kind, i)
	}
	return nil
}

// endIfScope finalises an OpIf scope. Merges every "still
// alive" arm-end + every OpBr/OpBrIf into the scope at postB,
// emitting phis for slots that differ across sources.
//
// "Source order" matches postB.Preds order, which matches
// SetBr call order:
//  1. SetBr(thenExit, postB) at OpElse time (if then arm fell
//     through).
//  2. brSources from OpBr/OpBrIf during the scope.
//  3. SetBr(elseExit, postB) at OpEnd time (if else arm fell
//     through; or for no-OpElse: SetBr(elseB, postB)).
//
// For value-producing ifs (blockType != Void) both arms must
// fall through; the per-source stack-tops merge into a phi
// pushed back onto the stack.
func (l *lifter) endIfScope(top scope) error {
	if !top.sawElse && top.blockType != ir.BlockTypeVoid {
		return fmt.Errorf("ssa.LiftFromIR: OpIf with BlockType %d requires OpElse", top.blockType)
	}

	var sources []mergeSource

	// Source 1: then-arm fall-through.
	thenAlive := false
	if top.sawElse {
		// SetBr was emitted at OpElse time iff cur was alive there;
		// detect that via top.thenSlots != nil.
		if top.thenSlots != nil {
			thenAlive = true
			sources = append(sources, mergeSource{
				slots:    top.thenSlots,
				stackTop: top.thenStackTop,
			})
		}
	} else {
		// No OpElse: then-arm body is l.cur. If alive, SetBr now.
		if l.cur != nil {
			thenAlive = true
			l.out.SetBr(l.cur, top.postB)
			sources = append(sources, mergeSource{
				slots: append([]Value(nil), l.slots...),
			})
		}
	}

	// Sources 2…N: any OpBr/OpBrIf into postB.
	for _, br := range top.brSources {
		sources = append(sources, mergeSource{slots: br.slots, stackTop: br.stackTop})
	}

	// Source N+1: else-arm fall-through.
	elseAlive := false
	if top.sawElse {
		if l.cur != nil {
			elseAlive = true
			var elseTop Value
			if top.blockType != ir.BlockTypeVoid {
				if len(l.stack) != top.stackHeight+1 {
					return fmt.Errorf("ssa.LiftFromIR: OpEnd: else-arm produced %d values, want 1",
						len(l.stack)-top.stackHeight)
				}
				elseTop = l.stack[len(l.stack)-1]
				l.stack = l.stack[:len(l.stack)-1]
			}
			l.out.SetBr(l.cur, top.postB)
			sources = append(sources, mergeSource{
				slots:    append([]Value(nil), l.slots...),
				stackTop: elseTop,
			})
		}
	} else {
		// No OpElse: the else-side is the still-empty elseB.
		l.out.SetBr(top.elseB, top.postB)
		elseAlive = true
		sources = append(sources, mergeSource{slots: top.preSlots})
	}

	if top.blockType != ir.BlockTypeVoid && (!thenAlive || !elseAlive) {
		return fmt.Errorf("ssa.LiftFromIR: value-producing OpIf needs both arms to fall through")
	}

	// Value-producing if: phi the per-source stack-tops.
	if top.blockType != ir.BlockTypeVoid {
		args := make([]Value, len(sources))
		for j, s := range sources {
			args[j] = s.stackTop
		}
		phi := l.out.AddPhi(top.postB, args...)
		l.stack = append(l.stack, phi)
	}

	if len(sources) > 0 {
		l.mergeSlotsViaPhi(top.postB, sources)
	}

	l.cur = top.postB
	return nil
}

// mergeSlotsViaPhi looks at every slot index across the merge
// sources; if any source differs in that slot, emit a phi at
// `postB` with args in source order; otherwise the slot takes
// the common value.
func (l *lifter) mergeSlotsViaPhi(postB *Block, sources []mergeSource) {
	for i := range l.slots {
		var seen Value
		varies := false
		for j, s := range sources {
			var v Value
			if i < len(s.slots) {
				v = s.slots[i]
			}
			if j == 0 {
				seen = v
				continue
			}
			if v != seen {
				varies = true
				break
			}
		}
		if !varies {
			if i < len(sources[0].slots) {
				l.slots[i] = sources[0].slots[i]
			}
			continue
		}
		args := make([]Value, len(sources))
		for j, s := range sources {
			if i < len(s.slots) {
				args[j] = s.slots[i]
			}
		}
		phiable := true
		for _, a := range args {
			if !a.IsValid() {
				phiable = false
				break
			}
		}
		if !phiable {
			for _, a := range args {
				if a.IsValid() {
					l.slots[i] = a
					break
				}
			}
			continue
		}
		l.slots[i] = l.out.AddPhi(postB, args...)
	}
}

// endBlockScope closes an OpBlock scope. Merges the fall-through
// (if l.cur is still alive) with every OpBr/OpBrIf branch source
// recorded during the scope. Source order matches Preds order:
// brSources were SetBr'd at their OpBr sites earlier in time;
// the fall-through (if alive) is SetBr'd now and appended last.
func (l *lifter) endBlockScope(top scope) {
	var sources []mergeSource
	for _, br := range top.brSources {
		sources = append(sources, mergeSource{slots: br.slots, stackTop: br.stackTop})
	}
	if l.cur != nil {
		l.out.SetBr(l.cur, top.postB)
		sources = append(sources, mergeSource{slots: append([]Value(nil), l.slots...)})
	}

	if len(sources) == 0 {
		// Whole scope exited via OpReturn — postB is unreachable.
		l.cur = top.postB
		return
	}

	l.mergeSlotsViaPhi(top.postB, sources)
	l.cur = top.postB
}

func mapBinaryArith(k ir.OpKind) OpKind {
	switch k {
	case ir.OpAdd:
		return OpAdd
	case ir.OpSub:
		return OpSub
	case ir.OpMul:
		return OpMul
	case ir.OpDivS:
		return OpDiv
	case ir.OpRemS:
		return OpRem
	case ir.OpAnd:
		return OpAnd
	case ir.OpOr:
		return OpOr
	case ir.OpXor:
		return OpXor
	case ir.OpShl:
		return OpShl
	case ir.OpShrS:
		return OpShr
	case ir.OpEq:
		return OpEq
	case ir.OpNe:
		return OpNe
	case ir.OpLtS:
		return OpLt
	case ir.OpLeS:
		return OpLe
	case ir.OpGtS:
		return OpGt
	case ir.OpGeS:
		return OpGe
	case ir.OpFAdd:
		return OpFAdd
	case ir.OpFSub:
		return OpFSub
	case ir.OpFMul:
		return OpFMul
	case ir.OpFDiv:
		return OpFDiv
	case ir.OpFEq:
		return OpFEq
	case ir.OpFNe:
		return OpFNe
	case ir.OpFLt:
		return OpFLt
	case ir.OpFLe:
		return OpFLe
	case ir.OpFGt:
		return OpFGt
	case ir.OpFGe:
		return OpFGe
	}
	return OpInvalid
}

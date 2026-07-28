package ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// LiftFromIR converts a legacy ir.Func into SSA form. The
// supported subset grows incrementally; each follow-up PR
// extends the switch + adds tests for the newly handled ops.
//
// Supported (cumulative):
//
//	Phase 1:
//	- OpConstI32 / OpConstI64 → OpConstInt
//	- OpAdd / OpSub / OpMul → matching SSA op
//	- OpReturn → ret <value>
//	- OpReturnVoid → ret
//
//	Phase 2:
//	- Function params — minted via AddParam, addressed by
//	  OpLoadLocal at slot indices [0, len(in.Params))
//	- OpLoadLocal for param slots only (non-param locals
//	  still rejected — they need phi insertion)
//
//	Phase 3a:
//	- OpDivS / OpRemS → OpDiv / OpRem
//	- OpAnd / OpOr / OpXor → matching SSA op
//	- OpShl / OpShrS → OpShl / OpShr
//	- OpNot → OpNot
//	- OpEq / OpNe / OpLtS / OpLeS / OpGtS / OpGeS → matching SSA cmp
//
//	Phase 4:
//	- OpCallDirect → OpCall with Str = callee name, Args = the
//	  popped arguments.
//
//	Phase 5:
//	- OpConstStr → OpConstString with Str = string literal.
//
//	Phase 6:
//	- OpConstF32 / OpConstF64 → OpConstFloat (F64 carries the value)
//	- OpFAdd / OpFSub / OpFMul / OpFDiv → matching SSA float op
//	- OpFNeg → OpFNeg
//	- OpFEq / OpFNe / OpFLt / OpFLe / OpFGt / OpFGe → matching SSA fcmp
//
//	Phase 7:
//	- OpStoreLocal / OpTeeLocal / OpLoadLocal for the non-param
//	  slot range.
//	- OpDrop pops the top stack value without emitting an SSA op.
//
//	Phase 8a:
//	- OpIf / OpElse / OpEnd for if/else control flow, BlockTypeVoid
//	  only. Creates a diamond CFG (then, else, post); phi nodes
//	  synthesised at the merge for any local slot whose value
//	  differs between the two arms. Nested ifs are fine — the
//	  scope stack handles them. OpReturn inside either arm is
//	  also fine — the arm just doesn't flow into the merge.
//
//	Phase 8b:
//	- OpIf with BlockTypeI32 / BlockTypeI64 / BlockTypeF32 /
//	  BlockTypeF64 — the if is an expression. Both arms push
//	  exactly one value before their closing OpElse/OpEnd; the
//	  two values are merged via a phi at postB and pushed back
//	  onto the operand stack. Requires both arms (no
//	  OpElse-less form for non-void blocks).
//
//	Phase 9:
//	- OpBlock (BlockTypeVoid only) opens a forward-only labelled
//	  scope; OpEnd closes it. Without an OpBr inside, the lift
//	  emits `br fall-through-block` and switches cur to it —
//	  functionally a no-op CFG-wise but establishes the
//	  scope-stack machinery that OpBr/OpBrIf will use to find
//	  their target. Non-void OpBlock + OpBr/OpBrIf land in
//	  follow-up PRs.
//
//	Phase 9b:
//	- OpBr to an enclosing OpBlock scope. The current block's
//	  terminator becomes `br target.postB`; the slot snapshot
//	  + (for non-void scopes) the popped stack-top become a
//	  branch source on the target scope, merged via phi at
//	  scope close. cur is set to nil after the OpBr — subsequent
//	  ops up to the matching OpEnd are unreachable and skipped
//	  by the per-handler `if cur == nil` guard.
//
//	Phase 10:
//	- OpBrIf to an enclosing OpBlock scope. The current block's
//	  terminator becomes `brif cond, target.postB, fallthrough`;
//	  a new fallthrough block becomes the active cur. The branch
//	  source captures slots at the OpBrIf site for the merge phi
//	  at scope close.
//
//	Phase 10c:
//	- OpBr / OpBrIf may target an enclosing OpIf scope (in
//	  addition to OpBlock). The endIfScope merge already
//	  iterates brSources so the only change is dropping the
//	  "OpBlock only" reject path.
//
//	Phase 11:
//	- OpLoop (BlockTypeVoid only) opens a backward-only labelled
//	  scope. The lift mints a `header` block and emits br cur →
//	  header at the OpLoop, then eagerly creates a phi at the
//	  header for every initialised slot (Args[0] = the pre-loop
//	  value). Loads inside the loop see the phi; stores update
//	  the slot to a new Value. OpBr / OpBrIf with this scope as
//	  target branches to header (not postB) — each back-edge
//	  appends the current slot Value to every header phi's Args.
//	  OpEnd terminates the loop body with br to postB; cur =
//	  postB. TrivialPhis later prunes any phi whose Args reduce
//	  to a single distinct Value.
//
//	Phase 12:
//	- OpLoad / OpStore memory access. The IR's bit-width metadata
//	  is dropped on the lift (SSA OpLoad/OpStore are width-
//	  agnostic for now). OpLoad pushes the result Value; OpStore
//	  emits a side-effect-only Op with no Result. DCE keeps both
//	  since they're impure.
//
//	Phase 13:
//	- Integer width conversions:
//	    OpExtendI32S → OpExtendS
//	    OpExtendI32U → OpExtendU
//	    OpWrapI64    → OpTrunc
//
//	Phase 14:
//	- Float width + int↔float conversions:
//	    OpFPromoteF32 → OpFPromote
//	    OpFDemoteF64  → OpFDemote
//	    OpFConvertI32/I64 → OpIToFS or OpIToFU (per Unsigned)
//	    OpITruncF32/F64   → OpFToIS or OpFToIU (per Unsigned)
//
//	Phase 15:
//	- Bit reinterpret ops (same-width float ↔ int):
//	    OpReinterpretI32F32 → OpReinterpretF32ToI32
//	    OpReinterpretF32I32 → OpReinterpretI32ToF32
//	    OpReinterpretI64F64 → OpReinterpretF64ToI64
//	    OpReinterpretF64I64 → OpReinterpretI64ToF64
//
//	Phase 16:
//	- OpConstFunc → OpMakeClosure with zero captures (a static
//	  {fn_idx, env_ptr=0} cell), Str = target name.
//	- OpCallIndirect → OpCallIndirect with Args[0] = popped
//	  callee index, Args[1..] = the popped argument values.
//	  IR convention: the callee idx is the top of stack at
//	  the OpCallIndirect site (pushed after the args).
//
//	Phase 17:
//	- Option / Result constructors (i32 payload variants):
//	    OpMakeSomeI32 → pop payload; push (const_int 0, payload)
//	    OpMakeNoneI32 → push (const_int 1, const_int 0)
//	    OpMakeOkI32   → pop payload; push (const_int 0, payload)
//	    OpMakeErrI32  → pop payload; push (const_int 1, payload)
//	  These leave 2 values on the operand stack (tag, payload).
//
//	Phase 18:
//	- OpMatchTag — pops a heap-pointer scrutinee and pushes the
//	  i32 variant tag stored at [ptr+0]. Lifts to ssa.OpLoad
//	  (the backend lowering already treats it as a load at
//	  offset 0).
//	- OpCallClosureDirect — defunctionalised closure direct
//	  call. Args layout: (args..., env_ptr); I32 = arg count
//	  including env_ptr. Lifts to ssa.OpCall with Str = callee.
//
//	Phase 19:
//	- OpMakeClosure → ssa.OpMakeClosure with Str = target name,
//	  Args = the N captures (per op.I32).
//	- OpMakeEnv → ssa.OpMakeEnv with Args = the N captures.
//
//	Phase 20:
//	- OpReturnPair → SSA TermRetPair. Pops (tag, payload),
//	  terminates the active block with the pair return.
//
//	Phase 21:
//	- OpCallDirectPair → SSA OpCallPair with Str = callee,
//	  Args = popped arguments, Result + Result2 = the
//	  (tag, payload) pair pushed back onto the stack.
//
//	Phase 22:
//	- Sub-i32 load/store variants:
//	    OpLoadByte → OpLoad8U
//	    OpStoreI8  → OpStore8
//	  Load variants take (addr); push result. Stores take
//	  (addr, val); no result.
//
//	Phase 23:
//	- Float memory access:
//	    OpFLoad  → OpLoadF
//	    OpFStore → OpStoreF
//
//	Phase 24:
//	- OpAlloc → ssa.OpAlloc (Args[0] = size; impure).
//	- OpEnumSentinel → ssa.OpEnumSentinel with Imm = the tag
//	  value (pure — CSE can dedupe).
//
// After Phase 24 the lift covers every real IR op kind. The
// remaining OpKinds in ir.OpKind are OpInvalid (the zero
// sentinel) — never emitted by a well-formed builder.
//
// Anything else returns an `unsupported op` error. OpBlock /
// OpLoop / OpBr / OpBrIf, indirect calls, and the conversion
// ops land in follow-up PRs.
//
// The legacy IR is a stack-machine encoding: every Op consumes
// its operand-stack inputs and pushes its result. The lift
// maintains a runtime stack of SSA Values mirroring that
// shape — pop N for an N-arg op, push the new Result.
// ssaHelperName maps a runtime-helper name the legacy IR emits onto the
// equivalent this backend family actually provides.
//
// Only __fern_str_append needs it (#5637). On the reclaiming backends that
// helper grows a uniquely-held accumulator in place, CONSUMING its left
// operand — the IR suppresses the release that would otherwise pair with it.
// The SSA backends allocate from a bump heap that never reclaims, so a plain
// __str_concat is a correct implementation of the same contract: the result
// bytes are identical, and the consumed operand is simply left behind, which
// is what this heap does with every dead allocation anyway. Emitting the real
// helper here would buy nothing (there are no size classes to have slack in)
// and would require a third copy of it in each SSA backend.
func ssaHelperName(name string) string {
	if name == "__fern_str_append" {
		return "__str_concat"
	}
	return name
}

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
	// Reading a slot that's never been stored materialises a
	// lazy const_int 0 default (legacy IR semantics — locals
	// start at 0 / 0.0; the bit pattern 0 also serves as
	// +0.0 in IEEE-754 so float locals work without per-slot
	// type info).
	totalSlots := len(in.Params) + len(in.Locals) + len(in.ScratchTypes)
	l.slots = make([]Value, totalSlots)
	l.out.ParamWidths = make([]int8, 0, len(in.Params))
	l.out.ParamFloats = make([]bool, 0, len(in.Params))
	for i, p := range in.Params {
		l.slots[i] = l.out.AddParam()
		l.out.ParamWidths = append(l.out.ParamWidths, widthOfAstType(p.Type))
		l.out.ParamFloats = append(l.out.ParamFloats, isFloatAstType(p.Type))
	}
	l.out.ReturnWidth = widthOfAstType(in.ReturnType)
	l.out.ReturnFloat = isFloatAstType(in.ReturnType)

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

// isFloatAstType reports whether `t` is a FloatType (f32/f64).
// Returns false for nil, ints, bools, and pointer-shaped types.
func isFloatAstType(t ast.Type) bool {
	switch t.(type) {
	case ast.FloatType, *ast.FloatType:
		return true
	}
	return false
}

// widthOfAstType returns 32 for i32-shaped types (i32, bool,
// void return, pointer-shaped — string/array/struct on wasm32),
// 64 for i64 types. Floats currently report their bit width
// too — backends decide what to do with that. Returns 0 for
// nil (= void return).
//
// Handles both pointer and value variants of NumberType/FloatType
// since the parser/checker mix the two shapes in practice.
func widthOfAstType(t ast.Type) int8 {
	switch tt := t.(type) {
	case nil:
		return 0
	case ast.NumberType:
		if tt.Width == 64 {
			return 64
		}
		return 32
	case *ast.NumberType:
		if tt.Width == 64 {
			return 64
		}
		return 32
	case ast.FloatType:
		if tt.Width == 64 {
			return 64
		}
		return 32
	case *ast.FloatType:
		if tt.Width == 64 {
			return 64
		}
		return 32
	default:
		return 32 // bool / void / pointer-shaped → i32 stack slot
	}
}

// AnnotateCallWidths sets each OpCall's result Width from the callee's
// ReturnWidth, for callees present in funcs. A backend sign-extends an i32-width
// call result back into the full register (the AArch64/SysV ABI only defines the
// low 32 bits of an i32 return), but a 64-bit return (i64 or an f64 whose high
// bits are its exponent) must skip that mask or it is truncated to garbage. The
// IR call op carries no return width, so this is resolved once per whole module
// after lifting: look up the callee's ReturnWidth and, when it is 64, mark the
// call. Callees absent from the map (runtime helpers emitted by the backend) are
// left unchanged — their i32/pointer returns need no 64-bit annotation. Call
// this after lifting all functions of a module and before emit.
func AnnotateCallWidths(funcs map[string]*Func) {
	for _, f := range funcs {
		for _, b := range f.Blocks {
			for _, op := range b.Ops {
				if op.Kind != OpCall {
					continue
				}
				if callee, ok := funcs[op.Str]; ok && callee.ReturnWidth == 64 {
					op.Width = 64
				}
			}
		}
	}
}

type lifter struct {
	in  *ir.Func
	out *Func

	// undef is a lazily-created `const 0` in the entry block, used to fill phi
	// args on unreachable predecessor edges where a slot is undefined (e.g. the
	// impossible arm of an exhaustive match). Defined in entry, it dominates
	// every block, so it is valid as any phi's incoming value.
	undef Value

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
	kind ir.OpKind // ir.OpIf / ir.OpBlock / ir.OpLoop.

	thenB  *Block
	elseB  *Block
	postB  *Block
	header *Block // OpLoop only — branch target for back-edges.

	// For OpLoop only: per-slot phi nodes inserted at the header.
	// Indexed parallel to l.slots; nil entries for uninitialised
	// slots at loop entry.
	loopPhis []*Op

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
		l.cur.Ops[len(l.cur.Ops)-1].Width = 64
		l.stack = append(l.stack, v)
	case ir.OpConstStr:
		v := l.out.AddOp(l.cur, OpConstString)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str
		l.stack = append(l.stack, v)
	case ir.OpConstF32:
		v := l.out.AddOp(l.cur, OpConstFloat)
		l.cur.Ops[len(l.cur.Ops)-1].F64 = float64(op.F32)
		l.cur.Ops[len(l.cur.Ops)-1].Width = 32
		l.stack = append(l.stack, v)
	case ir.OpConstF64:
		v := l.out.AddOp(l.cur, OpConstFloat)
		l.cur.Ops[len(l.cur.Ops)-1].F64 = op.F64
		l.cur.Ops[len(l.cur.Ops)-1].Width = 64
		l.stack = append(l.stack, v)
	case ir.OpLoadLocal:
		idx := int(op.I32)
		if idx < 0 || idx >= len(l.slots) {
			return fmt.Errorf("ssa.LiftFromIR: OpLoadLocal at op[%d] slot %d out of range (have %d slots)",
				i, idx, len(l.slots))
		}
		v := l.slots[idx]
		if !v.IsValid() {
			// Materialise a default-zero on demand — matches the
			// legacy IR's "locals start at zero" semantics. We
			// don't pre-emit zero ops at function entry because
			// most slots are stored before they're read; emitting
			// here keeps the entry block uncluttered.
			v = l.out.AddOp(l.cur, OpConstInt)
			l.cur.Ops[len(l.cur.Ops)-1].Imm = 0
			l.slots[idx] = v
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
		// Some IR kinds have signed/unsigned variants flagged via
		// op.Unsigned. mapBinaryArith returns the signed kind by
		// default; switch to the unsigned variant if requested.
		if op.Unsigned {
			kind = mapUnsignedVariant(kind)
		}
		v := l.out.AddOp(l.cur, kind, lhs, rhs)
		// Propagate width from the IR op so backends can choose
		// between i32 and i64 opcodes. Floats carry width in their
		// kind (OpFAdd etc.); Width stays 0 for them.
		if op.Width == 64 {
			l.cur.Ops[len(l.cur.Ops)-1].Width = 64
		}
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
	case ir.OpExtendI32S, ir.OpExtendI32U, ir.OpWrapI64,
		ir.OpFPromoteF32, ir.OpFDemoteF64,
		ir.OpFConvertI32, ir.OpFConvertI64,
		ir.OpITruncF32, ir.OpITruncF64,
		ir.OpReinterpretI32F32, ir.OpReinterpretF32I32,
		ir.OpReinterpretI64F64, ir.OpReinterpretF64I64:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: %v at op[%d] needs 1 operand", op.Kind, i)
		}
		arg := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		var kind OpKind
		switch op.Kind {
		case ir.OpExtendI32S:
			kind = OpExtendS
		case ir.OpExtendI32U:
			kind = OpExtendU
		case ir.OpWrapI64:
			kind = OpTrunc
		case ir.OpFPromoteF32:
			kind = OpFPromote
		case ir.OpFDemoteF64:
			kind = OpFDemote
		case ir.OpFConvertI32, ir.OpFConvertI64:
			if op.Unsigned {
				kind = OpIToFU
			} else {
				kind = OpIToFS
			}
		case ir.OpITruncF32, ir.OpITruncF64:
			if op.Unsigned {
				kind = OpFToIU
			} else {
				kind = OpFToIS
			}
		case ir.OpReinterpretI32F32:
			kind = OpReinterpretF32ToI32
		case ir.OpReinterpretF32I32:
			kind = OpReinterpretI32ToF32
		case ir.OpReinterpretI64F64:
			kind = OpReinterpretF64ToI64
		case ir.OpReinterpretF64I64:
			kind = OpReinterpretI64ToF64
		}
		v := l.out.AddOp(l.cur, kind, arg)
		// Propagate a 64-bit destination width to the float→int conversions so the
		// backend does not narrow the result back to i32 with its maskFix. Without
		// this, `x as i64` on a value that needs the high 32 bits (e.g. the
		// `(frac * 10^15) as i64` step in float-to-string) was sign-extended from
		// bit 31 and silently truncated. i32 destinations keep Width 0 (maskFix
		// sxtw is correct there).
		if op.Width == 64 && (kind == OpFToIS || kind == OpFToIU) {
			l.cur.Ops[len(l.cur.Ops)-1].Width = 64
		}
		l.stack = append(l.stack, v)
	case ir.OpDrop:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpDrop at op[%d] needs 1 operand", i)
		}
		l.stack = l.stack[:len(l.stack)-1]
	case ir.OpLoad:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpLoad at op[%d] needs addr operand", i)
		}
		addr := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		// The IR's OpLoad is a 4-byte (i32-word) load by default; pointer-width
		// values carry Width == WidthPtr (or an explicit 64). Mirror the stack
		// machine: full 8-byte load for pointer width, 4-byte otherwise.
		kind := OpLoad32U
		if op.Width == 64 || op.Width == ir.WidthPtr {
			kind = OpLoad
		}
		v := l.out.AddOp(l.cur, kind, addr)
		l.stack = append(l.stack, v)
	case ir.OpStore:
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: OpStore at op[%d] needs (addr, value) operands", i)
		}
		val := l.stack[len(l.stack)-1]
		addr := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		kind := OpStore32
		if op.Width == 64 || op.Width == ir.WidthPtr {
			kind = OpStore
		}
		l.out.AddOpNoResult(l.cur, kind, addr, val)
	case ir.OpLoadByte:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: %v at op[%d] needs addr operand", op.Kind, i)
		}
		addr := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		v := l.out.AddOp(l.cur, OpLoad8U, addr)
		l.stack = append(l.stack, v)
	case ir.OpStoreI8:
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: %v at op[%d] needs (addr, value) operands", op.Kind, i)
		}
		val := l.stack[len(l.stack)-1]
		addr := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		l.out.AddOpNoResult(l.cur, OpStore8, addr, val)
	case ir.OpFLoad:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpFLoad at op[%d] needs addr operand", i)
		}
		addr := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		v := l.out.AddOp(l.cur, OpLoadF, addr)
		l.stack = append(l.stack, v)
	case ir.OpFStore:
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: OpFStore at op[%d] needs (addr, value) operands", i)
		}
		val := l.stack[len(l.stack)-1]
		addr := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		l.out.AddOpNoResult(l.cur, OpStoreF, addr, val)
	case ir.OpStrEq:
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: OpStrEq at op[%d] needs 2 operands", i)
		}
		b := l.stack[len(l.stack)-1]
		a := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		v := l.out.AddOp(l.cur, OpCall, a, b)
		l.cur.Ops[len(l.cur.Ops)-1].Str = "__str_eq"
		l.stack = append(l.stack, v)
	case ir.OpStrConcat:
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: OpStrConcat at op[%d] needs 2 operands", i)
		}
		b := l.stack[len(l.stack)-1]
		a := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		v := l.out.AddOp(l.cur, OpCall, a, b)
		l.cur.Ops[len(l.cur.Ops)-1].Str = "__str_concat"
		l.stack = append(l.stack, v)
	case ir.OpStrLen:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpStrLen at op[%d] needs 1 operand", i)
		}
		arg := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		v := l.out.AddOp(l.cur, OpCall, arg)
		l.cur.Ops[len(l.cur.Ops)-1].Str = "__str_len"
		l.stack = append(l.stack, v)
	case ir.OpAlloc:
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpAlloc at op[%d] needs size operand", i)
		}
		size := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		v := l.out.AddOp(l.cur, OpAlloc, size)
		l.stack = append(l.stack, v)
	case ir.OpEnumSentinel:
		v := l.out.AddOp(l.cur, OpEnumSentinel)
		l.cur.Ops[len(l.cur.Ops)-1].Imm = int64(op.I32)
		l.stack = append(l.stack, v)
	case ir.OpCallDirect, ir.OpRcInc, ir.OpRcDec, ir.OpRcIsUnique:
		// The dedicated rc ops (#4402 opt 2) carry the runtime
		// helper's name in Str and argc in I32, exactly like the
		// OpCallDirect they replaced — lift them as the same
		// one-result OpCall so SSA passes keep seeing the calls
		// they saw before the kinds split.
		argc := int(op.I32)
		if len(l.stack) < argc {
			return fmt.Errorf("ssa.LiftFromIR: %s at op[%d] needs %d args, stack has %d",
				op.Kind, i, argc, len(l.stack))
		}
		args := append([]Value(nil), l.stack[len(l.stack)-argc:]...)
		l.stack = l.stack[:len(l.stack)-argc]
		result := l.out.AddOp(l.cur, OpCall, args...)
		l.cur.Ops[len(l.cur.Ops)-1].Str = ssaHelperName(op.Str)
		l.stack = append(l.stack, result)
	case ir.OpCallIndirect:
		argc := int(op.I32)
		// Layout on the stack: [args..., callee_idx]. Pop callee
		// first, then argc args.
		if len(l.stack) < argc+1 {
			return fmt.Errorf("ssa.LiftFromIR: OpCallIndirect at op[%d] needs %d args + callee, stack has %d",
				i, argc, len(l.stack))
		}
		callee := l.stack[len(l.stack)-1]
		args := append([]Value(nil), l.stack[len(l.stack)-1-argc:len(l.stack)-1]...)
		l.stack = l.stack[:len(l.stack)-argc-1]
		all := append([]Value{callee}, args...)
		result := l.out.AddOp(l.cur, OpCallIndirect, all...)
		l.stack = append(l.stack, result)
	case ir.OpConstFunc:
		// A bare function value is a zero-capture closure: it produces the
		// same {fn_idx, env_ptr=0} cell an OpMakeClosure with no captures
		// would (see internal/ir/inline_zero_capture.go, which rewrites one
		// to the other). Lift it to OpMakeClosure with zero captures so it
		// derefs identically to a real closure through OpCallIndirect
		// (docs/SSA-CLOSURE-DISPATCH.md); fn_idx is resolved from op.Str via
		// the module's function-index table, not the stale op.I32.
		result := l.out.AddOp(l.cur, OpMakeClosure)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str
		l.stack = append(l.stack, result)
	case ir.OpConstVtable:
		// () → vtable address. The IR names the (trait-set, concrete) pair in
		// Str/Str2; pack both into the SSA op's single Str field (the backend
		// splits on '/' for the .rodata label), mirroring the native emitter.
		result := l.out.AddOp(l.cur, OpConstVtable)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str + "/" + op.Str2()
		l.stack = append(l.stack, result)
	case ir.OpBoxDyn:
		// [data, vtable] (vtable on top) → cell pointer. Args = [data, vtable].
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: OpBoxDyn at op[%d] needs [data, vtable], stack has %d", i, len(l.stack))
		}
		vtable := l.stack[len(l.stack)-1]
		data := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		result := l.out.AddOp(l.cur, OpBoxDyn, data, vtable)
		l.stack = append(l.stack, result)
	case ir.OpCallDyn:
		// [data, args..., vtable] (vtable on top) → result | (). op.Sig() is the
		// receiver-first method signature (Params[0] = receiver/data), so the
		// number of call-arg values is len(Sig.Params); the vtable sits above
		// them. Args = [data, args..., vtable]; Imm = the method slot; Width =
		// the result width. Pushes a result iff the method is non-void.
		if op.Sig() == nil {
			return fmt.Errorf("ssa.LiftFromIR: OpCallDyn at op[%d] missing Sig", i)
		}
		argc := len(op.Sig().Params)
		if len(l.stack) < argc+1 {
			return fmt.Errorf("ssa.LiftFromIR: OpCallDyn at op[%d] needs %d args + vtable, stack has %d", i, argc, len(l.stack))
		}
		vtable := l.stack[len(l.stack)-1]
		callArgs := append([]Value(nil), l.stack[len(l.stack)-1-argc:len(l.stack)-1]...)
		l.stack = l.stack[:len(l.stack)-argc-1]
		all := append(callArgs, vtable)
		result := l.out.AddOp(l.cur, OpCallDyn, all...)
		o := l.cur.Ops[len(l.cur.Ops)-1]
		o.Imm = int64(op.I32) // method slot
		if op.Sig().Result != nil {
			o.Width = widthOfAstType(op.Sig().Result)
			l.stack = append(l.stack, result)
		}
	case ir.OpMakeSomeI32, ir.OpMakeOkI32:
		// (payload) → (tag=0, payload)
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: %v at op[%d] needs payload operand", op.Kind, i)
		}
		payload := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		tag := l.out.AddOp(l.cur, OpConstInt)
		l.cur.Ops[len(l.cur.Ops)-1].Imm = 0
		l.stack = append(l.stack, tag, payload)
	case ir.OpMakeErrI32:
		// (payload) → (tag=1, payload)
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpMakeErrI32 at op[%d] needs payload operand", i)
		}
		payload := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		tag := l.out.AddOp(l.cur, OpConstInt)
		l.cur.Ops[len(l.cur.Ops)-1].Imm = 1
		l.stack = append(l.stack, tag, payload)
	case ir.OpMakeNoneI32:
		// () → (tag=1, payload=0)
		tag := l.out.AddOp(l.cur, OpConstInt)
		l.cur.Ops[len(l.cur.Ops)-1].Imm = 1
		payload := l.out.AddOp(l.cur, OpConstInt)
		l.cur.Ops[len(l.cur.Ops)-1].Imm = 0
		l.stack = append(l.stack, tag, payload)
	case ir.OpMatchTag:
		// (ptr) → (i32 tag at [ptr+0]). A 4-byte load (the tag is an i32).
		if len(l.stack) < 1 {
			return fmt.Errorf("ssa.LiftFromIR: OpMatchTag at op[%d] needs ptr operand", i)
		}
		addr := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		v := l.out.AddOp(l.cur, OpLoad32U, addr)
		l.stack = append(l.stack, v)
	case ir.OpCallDirectPair:
		// Pair-returning direct call. I32 = arg count; pushes
		// (tag, payload) back onto the stack.
		argc := int(op.I32)
		if len(l.stack) < argc {
			return fmt.Errorf("ssa.LiftFromIR: OpCallDirectPair at op[%d] needs %d args, stack has %d",
				i, argc, len(l.stack))
		}
		args := append([]Value(nil), l.stack[len(l.stack)-argc:]...)
		l.stack = l.stack[:len(l.stack)-argc]
		tag, payload := l.out.AddCallPair(l.cur, args...)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str
		l.stack = append(l.stack, tag, payload)
	case ir.OpCallClosureDirect:
		// (args..., env_ptr) — I32 is the total arg count
		// including env_ptr. Lift like OpCallDirect.
		argc := int(op.I32)
		if len(l.stack) < argc {
			return fmt.Errorf("ssa.LiftFromIR: OpCallClosureDirect at op[%d] needs %d args, stack has %d",
				i, argc, len(l.stack))
		}
		args := append([]Value(nil), l.stack[len(l.stack)-argc:]...)
		l.stack = l.stack[:len(l.stack)-argc]
		result := l.out.AddOp(l.cur, OpCall, args...)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str
		l.stack = append(l.stack, result)
	case ir.OpMakeClosure:
		// (cap_0 ... cap_{n-1}) → i32 closure ptr.
		capc := int(op.I32)
		if len(l.stack) < capc {
			return fmt.Errorf("ssa.LiftFromIR: OpMakeClosure at op[%d] needs %d captures, stack has %d",
				i, capc, len(l.stack))
		}
		caps := append([]Value(nil), l.stack[len(l.stack)-capc:]...)
		l.stack = l.stack[:len(l.stack)-capc]
		result := l.out.AddOp(l.cur, OpMakeClosure, caps...)
		l.cur.Ops[len(l.cur.Ops)-1].Str = op.Str
		l.cur.Ops[len(l.cur.Ops)-1].CaptureSlots = op.CaptureSlots()
		l.stack = append(l.stack, result)
	case ir.OpMakeEnv:
		// (cap_0 ... cap_{n-1}) → i32 env ptr.
		capc := int(op.I32)
		if len(l.stack) < capc {
			return fmt.Errorf("ssa.LiftFromIR: OpMakeEnv at op[%d] needs %d captures, stack has %d",
				i, capc, len(l.stack))
		}
		caps := append([]Value(nil), l.stack[len(l.stack)-capc:]...)
		l.stack = l.stack[:len(l.stack)-capc]
		result := l.out.AddOp(l.cur, OpMakeEnv, caps...)
		l.cur.Ops[len(l.cur.Ops)-1].CaptureSlots = op.CaptureSlots()
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
	case ir.OpLoop:
		if op.I32 != ir.BlockTypeVoid {
			return fmt.Errorf("ssa.LiftFromIR: OpLoop at op[%d] non-void BlockType %d not yet supported", i, op.I32)
		}
		header := l.out.NewBlock()
		postB := l.out.NewBlock()
		l.out.SetBr(l.cur, header)
		// Determine which slots the loop body actually writes to
		// (OpStoreLocal / OpTeeLocal). Only those slots need a
		// header phi — unmodified slots keep their pre-loop value
		// throughout the loop, so a phi for them would just
		// reference the pre-loop Value as both Args[0] and the
		// back-edge arg. That self-phi is fine inside the loop
		// but creates a dominance violation when the loop is
		// reached conditionally (e.g., the surrounding `if`'s
		// else-arm bypasses the loop, and the eager phi's pre-
		// loop Value isn't dominated by an enclosing merge phi
		// at the if-postB if the merge skipped a single-Value
		// "doesn't vary" case).
		writtenSlots := loopBodyWrites(l.in.Ops, i)
		loopPhis := make([]*Op, len(l.slots))
		for sIdx, v := range l.slots {
			if !v.IsValid() {
				continue
			}
			if !writtenSlots[sIdx] {
				continue
			}
			phiResult := l.out.AddPhi(header, v)
			var phiOp *Op
			for _, op := range header.Ops {
				if op.Kind == OpPhi && op.Result == phiResult {
					phiOp = op
					break
				}
			}
			loopPhis[sIdx] = phiOp
			l.slots[sIdx] = phiResult
		}
		l.scopes = append(l.scopes, scope{
			kind:        ir.OpLoop,
			header:      header,
			postB:       postB,
			preSlots:    append([]Value(nil), l.slots...),
			blockType:   op.I32,
			stackHeight: len(l.stack),
			loopPhis:    loopPhis,
		})
		l.cur = header
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
		case ir.OpLoop:
			l.endLoopScope(top)
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
		switch target.kind {
		case ir.OpBlock, ir.OpIf:
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
		case ir.OpLoop:
			// Back-edge: branch to header, append every header
			// phi's Args with the current slot value.
			l.out.SetBr(l.cur, target.header)
			for sIdx, phi := range target.loopPhis {
				if phi == nil {
					continue
				}
				phi.Args = append(phi.Args, l.slots[sIdx])
			}
		default:
			return fmt.Errorf("ssa.LiftFromIR: OpBr at op[%d] targets unsupported scope kind %v", i, target.kind)
		}
		l.cur = nil
	case ir.OpBrIf:
		depth := int(op.I32)
		if depth < 0 || depth >= len(l.scopes) {
			return fmt.Errorf("ssa.LiftFromIR: OpBrIf at op[%d] depth %d out of range (have %d scopes)",
				i, depth, len(l.scopes))
		}
		target := &l.scopes[len(l.scopes)-1-depth]
		if target.kind != ir.OpBlock && target.kind != ir.OpIf && target.kind != ir.OpLoop {
			return fmt.Errorf("ssa.LiftFromIR: OpBrIf at op[%d] targets unsupported scope kind %v",
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
		switch target.kind {
		case ir.OpBlock, ir.OpIf:
			target.brSources = append(target.brSources, brSource{
				block:    l.cur,
				slots:    append([]Value(nil), l.slots...),
				stackTop: stackTop,
			})
			l.out.SetBrIf(l.cur, cond, target.postB, fallthroughB)
		case ir.OpLoop:
			l.out.SetBrIf(l.cur, cond, target.header, fallthroughB)
			for sIdx, phi := range target.loopPhis {
				if phi == nil {
					continue
				}
				phi.Args = append(phi.Args, l.slots[sIdx])
			}
		}
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
	case ir.OpReturnPair:
		// IR stack at the OpReturnPair site: [..., tag, payload].
		if len(l.stack) < 2 {
			return fmt.Errorf("ssa.LiftFromIR: OpReturnPair at op[%d] needs (tag, payload)", i)
		}
		payload := l.stack[len(l.stack)-1]
		tag := l.stack[len(l.stack)-2]
		l.stack = l.stack[:len(l.stack)-2]
		l.out.SetRetPair(l.cur, tag, payload)
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
// undefValue returns a `const 0` in the entry block, created on first use, for
// filling phi args on unreachable edges where a slot is undefined. Entry
// dominates every block, so it is a valid incoming value for any phi.
func (l *lifter) undefValue() Value {
	if !l.undef.IsValid() {
		v := l.out.AddOp(l.out.Entry, OpConstInt)
		l.out.Entry.Ops[len(l.out.Entry.Ops)-1].Imm = 0
		l.undef = v
	}
	return l.undef
}

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
		// A slot may be undefined on some incoming edge — e.g. the impossible
		// arm of an exhaustive match (`match opt { Some(v) => …, None => … }`
		// still emits a CFG edge for a tag that can't occur), where the result
		// slot was never stored. That edge is unreachable, so filling its phi
		// arg with the entry-block undef keeps SSA well-formed (every predecessor
		// gets an arg that dominates it) without changing behaviour on the
		// reachable paths. Previously the merge gave up here and picked a single
		// arm's value, producing a `ret`/use of a value defined on only one path.
		anyValid := false
		for _, a := range args {
			if a.IsValid() {
				anyValid = true
				break
			}
		}
		if !anyValid {
			continue // slot undefined on every edge — nothing to merge
		}
		for j := range args {
			if !args[j].IsValid() {
				args[j] = l.undefValue()
			}
		}
		l.slots[i] = l.out.AddPhi(postB, args...)
	}
}

// endLoopScope closes an OpLoop scope. Fall-through past the
// loop body goes to postB. The header phis already have one
// arg per back-edge appended at the OpBr/OpBrIf sites; if the
// loop body fell through (l.cur != nil at OpEnd), that's a
// silent fall-through edge that doesn't loop back (wasm OpLoop
// semantics: fall-through goes past OpEnd, NOT back to the
// header).
//
// If the loop body never fell through (always looped back or
// exited via OpBr/OpReturn), cur stays nil so the outer scope's
// endX doesn't pick up loop.postB as a phantom fall-through
// source — that would put an unreachable Pred into the outer
// merge and pull in Values that don't dominate it.
func (l *lifter) endLoopScope(top scope) {
	if l.cur != nil {
		l.out.SetBr(l.cur, top.postB)
		l.cur = top.postB
		return
	}
	// Loop body never fell through — postB is unreachable. Give
	// it a void-ret terminator so Verify doesn't reject; the
	// upcoming PruneUnreachable pass will drop the block in
	// Optimize.
	l.out.SetRet(top.postB, Value{})
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

// mapUnsignedVariant returns the unsigned counterpart of a
// signed-by-default integer op (OpDiv → OpDivU, etc.). For ops
// that aren't signedness-affected (OpAdd, OpEq, OpNe, OpAnd,
// OpOr, OpXor, OpShl, OpMul, OpSub) it returns the input
// unchanged.
func mapUnsignedVariant(k OpKind) OpKind {
	switch k {
	case OpDiv:
		return OpDivU
	case OpRem:
		return OpRemU
	case OpShr:
		return OpShrU
	case OpLt:
		return OpLtU
	case OpLe:
		return OpLeU
	case OpGt:
		return OpGtU
	case OpGe:
		return OpGeU
	}
	return k
}

// loopBodyWrites scans the IR ops starting at the OpLoop at
// `loopIdx` (exclusive) up to (but not including) the matching
// OpEnd. Returns a set of slot indices that are written via
// OpStoreLocal / OpTeeLocal anywhere in the loop body.
//
// Used by the OpLoop handler to decide which slots need a
// header phi. Slots that aren't written keep their pre-loop
// SSA Value throughout the loop, so no phi is needed (and a
// phi for an unwritten slot would create a dominance bug
// when the loop is conditionally entered).
func loopBodyWrites(ops []ir.Op, loopIdx int) map[int]bool {
	written := map[int]bool{}
	depth := 1
	for j := loopIdx + 1; j < len(ops); j++ {
		switch ops[j].Kind {
		case ir.OpBlock, ir.OpLoop, ir.OpIf:
			depth++
		case ir.OpEnd:
			depth--
			if depth == 0 {
				return written
			}
		case ir.OpStoreLocal, ir.OpTeeLocal:
			written[int(ops[j].I32)] = true
		}
	}
	return written
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

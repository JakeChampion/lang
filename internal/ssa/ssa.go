// Package ssa is the SSA-form intermediate representation —
// the foundation for `docs/PERFORMANCE-RESEARCH.md` Rec §1.
//
// The existing `internal/ir` package uses a flat op list with
// `OpBlock` / `OpBranch` markers and indexed local slots.
// Peephole optimisations (constprop, copyprop, dce) work on
// that shape but tend to miss cross-block opportunities;
// loop-level analyses (LICM, unrolling, escape analysis,
// range analysis through phi nodes) need a CFG and def-use
// chains. SSA gets us there.
//
// This package is the Phase 1 foundation: type definitions
// plus a builder API for constructing SSA programs
// imperatively. No conversion from the existing IR yet —
// `LiftFromIR` is a Phase 2 task. Phase 3 migrates the
// optimisation passes to consume SSA form; Phase 4 (after
// the migration settles) deletes the legacy flat-op layer.
//
// Reference shape: Briggs/Cooper/Torczon's "Engineering a
// Compiler" Chapter 9 — pruned SSA construction with the
// dominance-frontier phi-insertion algorithm. Cliff Click's
// "A Simple, Fast Dominance Algorithm" (TR-06-33870) for the
// O(N²) dominator-tree builder. Both well-trodden ground;
// no novel research in this package.
package ssa

import "fmt"

// Value identifies an SSA name. Per-Func unique; the Builder
// hands them out monotonically. Zero is the invalid sentinel
// — `Value{}` indicates "no result" (e.g. side-effect-only
// op like Store).
type Value struct {
	// ID is a per-Func sequence number. The text dump renders
	// it as `v42`; opt passes look up def-use chains by ID.
	ID int32
	// Func is a back-pointer for debugging dumps — usually
	// stays nil except in error paths where the value crossed
	// a function boundary.
	Func *Func `json:"-"`
}

// String renders the Value as `v<ID>` (SSA convention).
func (v Value) String() string {
	if v.ID == 0 {
		return "v_"
	}
	return fmt.Sprintf("v%d", v.ID)
}

// IsValid reports whether `v` names a real SSA value (i.e.
// isn't the zero sentinel). Useful in optimisation passes
// that walk Op.Args + need to skip Op kinds where a slot
// might be intentionally unfilled (no current ops; future-
// proofing the API).
func (v Value) IsValid() bool { return v.ID != 0 }

// OpKind enumerates the operations an SSA op can perform.
// Phase 1 covers the minimum set the legacy IR needs;
// Phase 2 fleshes out the long tail (call shapes, struct/
// array ops, closure conversion).
type OpKind int

const (
	OpInvalid OpKind = iota

	// Arithmetic — integer. Signed-by-default; the U variants
	// (DivU, RemU) interpret operands as unsigned int64.
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpDivU
	OpRem
	OpRemU

	// Bitwise — integer (i32 / i64).
	OpAnd
	OpOr
	OpXor

	// Shift — integer. Shl is logical left; Shr is arithmetic
	// (sign-preserving) right; ShrU is logical right (fills
	// with zero). Shift count comes from Args[1]; out-of-range
	// counts (rhs < 0 or rhs >= 64) are left unfolded — the
	// runtime owns that case.
	OpShl
	OpShr
	OpShrU

	// Unary arithmetic — integer negation.
	OpNeg

	// Integer width conversions. SSA Values are stored as
	// int64 internally; these ops let the IR distinguish
	// truncation, sign-/zero-extension, and sub-i32 sign-
	// extension so backend codegen can emit the right
	// instruction.
	OpTrunc     // i64 → i32, keep low 32 bits (sign-aware: int64(int32(v)))
	OpExtendS   // i32 → i64, sign-extend
	OpExtendU   // i32 → i64, zero-extend
	OpExtend8S  // i32 → i32, sign-extend low byte
	OpExtend16S // i32 → i32, sign-extend low halfword

	// Float width conversions. Internally SSA stores all floats
	// as f64; OpFPromote / OpFDemote let backend codegen emit
	// the precision-changing wasm/arm64/x86 instruction.
	OpFPromote // f32 → f64 (lossless)
	OpFDemote  // f64 → f32 (lossy — round to f32 precision)

	// Int ↔ float conversions. Signed/unsigned distinction is
	// encoded in the OpKind (S vs U). The float side is f64
	// in SSA; backend codegen decides f32 vs f64 from the
	// upcoming type-tagging story.
	OpIToFS // int64 → float64 (signed integer)
	OpIToFU // int64 → float64 (unsigned integer)
	OpFToIS // float64 → int64 (signed truncation)
	OpFToIU // float64 → int64 (unsigned truncation)

	// Bit reinterpret — same-width float ↔ int with no value
	// conversion (just re-types the bit pattern). Lowers to
	// wasm's i32.reinterpret_f32 / f32.reinterpret_i32 /
	// i64.reinterpret_f64 / f64.reinterpret_i64. Four kinds
	// to preserve the width distinction the IR encodes.
	OpReinterpretF32ToI32 // (f32 bits) → (i32 with same bit pattern)
	OpReinterpretI32ToF32 // (i32 bits) → (f32 with same bit pattern)
	OpReinterpretF64ToI64 // (f64 bits) → (i64 with same bit pattern)
	OpReinterpretI64ToF64 // (i64 bits) → (f64 with same bit pattern)

	// Unary logical — boolean negation. Args[0] is a bool
	// Value; result is the flipped bool.
	OpNot

	// Ternary select — `cond ? ifTrue : ifFalse`. Args[0] is
	// the bool condition; Args[1] is the value when true;
	// Args[2] is the value when false. Branchless; backends
	// lower to csel (arm64), cmov (x86), or select (wasm).
	OpSelect

	// Float arithmetic — IEEE-754 double (f64). The integer
	// op set covers i32/i64 via Width metadata; floats get
	// their own kinds because the operand type is encoded in
	// the kind rather than in a width sidecar (matches the
	// wasm + arm64 backends' actual instruction shape).
	OpFAdd
	OpFSub
	OpFMul
	OpFDiv
	OpFNeg
	OpFEq
	OpFNe
	OpFLt
	OpFLe
	OpFGt
	OpFGe

	// Comparison — produces a boolean Value. The U variants
	// interpret operands as unsigned int64.
	OpEq
	OpNe
	OpLt
	OpLtU
	OpLe
	OpLeU
	OpGt
	OpGtU
	OpGe
	OpGeU

	// Constants.
	OpConstInt    // Imm carries the value
	OpConstBool   // Imm == 0 or 1
	OpConstString // Str carries the value
	OpConstFloat  // F64 carries the value

	// Function call. `Args[0]` is the callee (a Value of
	// function type once we grow types; for Phase 1 the
	// Str field carries the callee name and Args[0..]
	// are the call's arguments).
	OpCall

	// Indirect function call. Args[0] is the callee (a Value
	// holding a function-table index); Args[1..] are the
	// call's arguments. Used for OpCallIndirect / closure
	// dispatch / function-pointer values.
	OpCallIndirect

	// Pair-returning direct call — produces two Values (tag
	// in Result, payload in Result2). Str holds the callee
	// name; Args hold the popped arguments. Used for the
	// pair-form lowering of Option/Result returns to avoid
	// the per-call heap box allocation.
	OpCallPair

	// Closure construction.
	//
	// OpMakeClosure allocates a
	// {fn_idx, env_ptr, drop_idx, env_ptr} cell plus an env
	// block, populates the env block from Args, and returns
	// the closure pointer (docs/SSA-CLOSURE-DISPATCH.md). Str
	// = the target function name; Args = the capture values
	// in declaration order.
	//
	// OpMakeEnv is the defunctionalised form: allocates the
	// env block only (no cell), returns the env pointer
	// directly. Args = the captures.
	//
	// Both are impure — heap allocation has side effects.
	OpMakeClosure
	OpMakeEnv

	// Load / Store — memory access. Index-into-array,
	// struct-field-access, etc. resolve to these.
	OpLoad
	OpStore

	// Sub-i32 load / store variants. OpLoad8S sign-extends
	// the low byte; OpLoad8U zero-extends. The halfword
	// variants do the same for the low 16 bits. OpStore8 /
	// OpStore16 write only the low N bits, leaving higher
	// bits in memory untouched.
	OpLoad8S
	OpLoad8U
	OpLoad16S
	OpLoad16U
	OpStore8
	OpStore16

	// 4-byte (i32 word) load / store. OpLoad32U zero-extends the
	// low 32 bits (matching a plain `mov eax, [addr]`); OpStore32
	// writes only the low 4 bytes. These are the default width for
	// the IR's OpLoad/OpStore (an i32 field); pointer-width
	// (8-byte) access uses OpLoad/OpStore.
	OpLoad32U
	OpStore32

	// Float load / store. The internal SSA float type is f64;
	// backend codegen decides f32 vs f64 from the type-tagging
	// story once it lands.
	OpLoadF
	OpStoreF

	// OpAlloc reserves N bytes from the bump allocator and
	// pushes the result pointer. Args[0] is the size; result
	// is the new pointer. Impure — allocator state changes
	// across calls so CSE/DCE must treat each Alloc as unique.
	OpAlloc

	// OpEnumSentinel pushes the address of a shared static
	// 4-byte sentinel whose tag is op.Imm. The op is pure —
	// two OpEnumSentinel with the same Imm produce the same
	// pointer, so CSE can merge them; DCE can drop unused
	// ones.
	OpEnumSentinel

	// OpConstStringLen returns the byte length of a string
	// literal as an i32. Args[0] is the result of an
	// OpConstString — the length is whatever ran through that
	// literal's Str field at lift time. Pure (the length is
	// known at compile time); CSE / DCE can treat it like any
	// other constant.
	OpConstStringLen

	// dyn Trait dispatch (docs/DYN-TRAITS.md §4.2.2, BOXED one-word). Lifted
	// 1:1 from the IR's OpConstVtable / OpBoxDyn / OpCallDyn; only the
	// arm64-ssa path (which opts into ir.DynSupported) ever produces them.
	//
	// OpConstVtable: Str = "<traitSetKey>/<concrete>" — pushes the address of
	// the static (trait-set, concrete) vtable (a .rodata array of absolute
	// method function pointers, trait declaration order). Pure-ish, but kept
	// impure for simplicity (a rodata address; never CSE-merged in practice).
	OpConstVtable

	// OpBoxDyn: Args = [data, vtable]. Allocates a 16-byte {data@0, vtable@8}
	// cell and returns its pointer. Impure (heap alloc + a call to the
	// allocator across which caller-saved values must be preserved).
	OpBoxDyn

	// OpCallDyn: Args = [data, method-args..., vtable]; Imm = the method slot
	// index. Loads vtable[Imm] (an absolute fn pointer) and indirect-calls it
	// with (data, method-args...) as receiver-first args. Width carries the
	// result width (0 => void/i32, 64 => i64). Impure (a call).
	OpCallDyn

	// Phi — SSA merge. In a block B with predecessors
	// P[0..n-1], a Phi op's Args[i] is the Value flowing in
	// from P[i]. Phi ops MUST appear at the top of B before
	// any non-phi op; Verify enforces this.
	OpPhi
)

// String renders the OpKind for dumps + error messages.
func (k OpKind) String() string {
	switch k {
	case OpInvalid:
		return "invalid"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpMul:
		return "mul"
	case OpDiv:
		return "div"
	case OpDivU:
		return "div_u"
	case OpRem:
		return "rem"
	case OpRemU:
		return "rem_u"
	case OpAnd:
		return "and"
	case OpOr:
		return "or"
	case OpXor:
		return "xor"
	case OpShl:
		return "shl"
	case OpShr:
		return "shr"
	case OpShrU:
		return "shr_u"
	case OpNeg:
		return "neg"
	case OpTrunc:
		return "trunc"
	case OpExtendS:
		return "extend_s"
	case OpExtendU:
		return "extend_u"
	case OpExtend8S:
		return "extend8_s"
	case OpExtend16S:
		return "extend16_s"
	case OpFPromote:
		return "f_promote"
	case OpFDemote:
		return "f_demote"
	case OpIToFS:
		return "i_to_f_s"
	case OpIToFU:
		return "i_to_f_u"
	case OpFToIS:
		return "f_to_i_s"
	case OpFToIU:
		return "f_to_i_u"
	case OpReinterpretF32ToI32:
		return "reinterpret_f32_to_i32"
	case OpReinterpretI32ToF32:
		return "reinterpret_i32_to_f32"
	case OpReinterpretF64ToI64:
		return "reinterpret_f64_to_i64"
	case OpReinterpretI64ToF64:
		return "reinterpret_i64_to_f64"
	case OpNot:
		return "not"
	case OpSelect:
		return "select"
	case OpFAdd:
		return "fadd"
	case OpFSub:
		return "fsub"
	case OpFMul:
		return "fmul"
	case OpFDiv:
		return "fdiv"
	case OpFNeg:
		return "fneg"
	case OpFEq:
		return "feq"
	case OpFNe:
		return "fne"
	case OpFLt:
		return "flt"
	case OpFLe:
		return "fle"
	case OpFGt:
		return "fgt"
	case OpFGe:
		return "fge"
	case OpConstFloat:
		return "const_float"
	case OpEq:
		return "eq"
	case OpNe:
		return "ne"
	case OpLt:
		return "lt"
	case OpLtU:
		return "lt_u"
	case OpLe:
		return "le"
	case OpLeU:
		return "le_u"
	case OpGt:
		return "gt"
	case OpGtU:
		return "gt_u"
	case OpGe:
		return "ge"
	case OpGeU:
		return "ge_u"
	case OpConstInt:
		return "const_int"
	case OpConstBool:
		return "const_bool"
	case OpConstString:
		return "const_string"
	case OpCall:
		return "call"
	case OpCallIndirect:
		return "call_indirect"
	case OpCallPair:
		return "call_pair"
	case OpMakeClosure:
		return "make_closure"
	case OpMakeEnv:
		return "make_env"
	case OpLoad:
		return "load"
	case OpStore:
		return "store"
	case OpLoad8S:
		return "load8_s"
	case OpLoad8U:
		return "load8_u"
	case OpLoad16S:
		return "load16_s"
	case OpLoad16U:
		return "load16_u"
	case OpStore8:
		return "store8"
	case OpStore16:
		return "store16"
	case OpLoad32U:
		return "load32_u"
	case OpStore32:
		return "store32"
	case OpLoadF:
		return "load_f"
	case OpStoreF:
		return "store_f"
	case OpAlloc:
		return "alloc"
	case OpEnumSentinel:
		return "enum_sentinel"
	case OpConstStringLen:
		return "const_string_len"
	case OpConstVtable:
		return "const_vtable"
	case OpBoxDyn:
		return "box_dyn"
	case OpCallDyn:
		return "call_dyn"
	case OpPhi:
		return "phi"
	default:
		return fmt.Sprintf("op(%d)", int(k))
	}
}

// Op is a single SSA operation. Result is the Value the op
// defines (or the zero sentinel for side-effect-only ops).
// Args lists the consumed Values in their natural order
// (e.g. lhs, rhs for binary ops; callee, ...callargs for
// OpCall).
type Op struct {
	Kind   OpKind
	Result Value
	// Result2 is the second-value-defined slot — used only by
	// multi-result ops (currently OpCallPair). Zero sentinel
	// for every other Op.
	Result2 Value
	Args    []Value

	// Op-kind-specific immediate fields. Mutually exclusive
	// in practice — each kind uses at most one.
	Imm int64   // OpConstInt / OpConstBool
	F64 float64 // OpConstFloat
	Str string  // OpConstString / OpCall (callee name)

	// Width selects between i32 and i64 for integer ops where
	// the kind alone doesn't disambiguate (OpAdd, OpConstInt,
	// OpEq, etc.). 0 (or 32) means i32; 64 means i64. Float
	// ops carry width in their kind (OpFAdd vs hypothetical
	// OpF32Add never materialised — floats are f64 in SSA);
	// this field is only meaningful for integer kinds.
	Width int8

	// CaptureSlots is set on OpMakeClosure / OpMakeEnv to the per-capture
	// env-block slot size in bytes (in capture order), carried from the IR
	// so the backend packs its env stores at the offsets/widths the capture
	// loads read. Nil means one 8-byte slot per capture (the uniform layout
	// hand-built SSA closures assume).
	CaptureSlots []int32
}

// TermKind enumerates terminator shapes. Every Block ends
// with exactly one Terminator; the Block's `Succs` derive
// from the terminator (br → 1 succ, brif → 2 succs, ret →
// 0 succs).
type TermKind int

const (
	TermInvalid TermKind = iota
	TermBr               // unconditional branch to one block
	TermBrIf             // conditional branch — Cond → True or False
	TermRet              // return; optional Value
	TermRetPair          // return a (tag, payload) pair — Value + Value2
)

// Terminator ends a Block. Succs are populated from `Target`
// (Br), `True` + `False` (BrIf), or empty (Ret / RetPair).
type Terminator struct {
	Kind   TermKind
	Cond   Value  // BrIf only
	Target *Block // Br only
	True   *Block // BrIf only
	False  *Block // BrIf only
	Value  Value  // Ret only; zero sentinel for void returns. Also the tag half of RetPair.
	Value2 Value  // RetPair only — the payload half.
}

// Block is a basic block — a straight-line sequence of Ops
// ending with a Terminator. No control flow inside.
type Block struct {
	ID    int32
	Ops   []*Op
	Preds []*Block
	Term  Terminator
}

// Succs returns the block's successor list, derived from
// the terminator. Empty for Ret blocks; 1 for Br; 2 for
// BrIf. Recomputed on each call — terminators are mutable
// during construction, so caching would invite stale
// answers.
func (b *Block) Succs() []*Block {
	switch b.Term.Kind {
	case TermBr:
		if b.Term.Target == nil {
			return nil
		}
		return []*Block{b.Term.Target}
	case TermBrIf:
		var out []*Block
		if b.Term.True != nil {
			out = append(out, b.Term.True)
		}
		if b.Term.False != nil {
			out = append(out, b.Term.False)
		}
		return out
	default:
		return nil
	}
}

// Func is an SSA-form function. Params name the entry block's
// initial-value providers; Blocks lists every reachable basic
// block (Entry is also in there at index 0 by convention but
// the package doesn't depend on that — walk via Entry +
// Succs for portability).
type Func struct {
	Name   string
	Params []Value
	Blocks []*Block
	Entry  *Block

	// ParamWidths is the bit-width of each param in `Params`
	// (excluding the zero sentinel) — 32 by default, 64 when
	// the param is i64. Parallel to `realParams(f)` in callers.
	// Empty / nil means "all 32 (i32)" — backwards-compatible
	// for builders that don't populate it.
	ParamWidths []int8
	// ReturnWidth is the bit-width of the function's return
	// value. 0 (or 32) means i32; 64 means i64. Reserved
	// values for float types will be added when the wasmssa
	// backend gains float support.
	ReturnWidth int8

	// ParamFloats is parallel to ParamWidths — true at index i
	// when param i is a float (f32 if width=32, f64 if width=64).
	// Empty / nil means "all int". Backends that don't support
	// floats can ignore.
	ParamFloats []bool
	// ReturnFloat: true when the function returns a float. Combined
	// with ReturnWidth: (true, 64) = f64, (true, 32) = f32.
	ReturnFloat bool

	// nextValueID is the counter the Builder uses to mint
	// fresh Values. Per-Func to keep IDs dense + predictable
	// in dumps.
	nextValueID int32
	// nextBlockID is the same for Block IDs.
	nextBlockID int32
}

// NewFunc creates an empty Func with no blocks. Add an entry
// block via `(f *Func).NewBlock()` and start populating.
func NewFunc(name string) *Func {
	return &Func{Name: name}
}

// NewValue mints a fresh SSA Value. The caller stores it as
// an Op's Result (or as a Param) — the package doesn't track
// def sites separately; def-use chains are computed
// on-demand by analysis passes.
func (f *Func) NewValue() Value {
	f.nextValueID++
	return Value{ID: f.nextValueID, Func: f}
}

// AddParam mints a Value for a function parameter and
// appends it to Params. Params are SSA Values like any
// other — uses see them on entry.
func (f *Func) AddParam() Value {
	v := f.NewValue()
	f.Params = append(f.Params, v)
	return v
}

// NewBlock creates an empty Block, appends it to f.Blocks,
// and returns it. Sets f.Entry if this is the first block.
func (f *Func) NewBlock() *Block {
	f.nextBlockID++
	b := &Block{ID: f.nextBlockID}
	f.Blocks = append(f.Blocks, b)
	if f.Entry == nil {
		f.Entry = b
	}
	return b
}

// AddOp appends an Op to block `b` and returns the Op's
// Result Value. Convenience over manually constructing the
// Op + minting a Value separately — most call sites want
// the result Value.
func (f *Func) AddOp(b *Block, kind OpKind, args ...Value) Value {
	result := f.NewValue()
	op := &Op{
		Kind:   kind,
		Result: result,
		Args:   args,
	}
	b.Ops = append(b.Ops, op)
	return result
}

// AddOpNoResult appends a side-effect-only Op (Store, etc.)
// to block `b`. The Op's Result is the zero sentinel.
func (f *Func) AddOpNoResult(b *Block, kind OpKind, args ...Value) *Op {
	op := &Op{Kind: kind, Args: args}
	b.Ops = append(b.Ops, op)
	return op
}

// AddCallPair appends an OpCallPair to block `b` and returns
// the two freshly-minted Values (tag, payload). Str on the
// emitted Op holds the callee name; callers set it before or
// after this call. Used for the pair-form lowering of
// Option/Result returns.
func (f *Func) AddCallPair(b *Block, args ...Value) (Value, Value) {
	tag := f.NewValue()
	payload := f.NewValue()
	op := &Op{Kind: OpCallPair, Result: tag, Result2: payload, Args: args}
	b.Ops = append(b.Ops, op)
	return tag, payload
}

// AddPhi prepends a Phi Op to block `b` and returns the
// freshly-minted Value. `args` MUST be in the same order as
// `b.Preds` — Args[i] is the Value flowing in from Preds[i].
// Phi Ops live at the top of the block (Verify rejects them
// in any other position), so this method splices the new Op
// in BEFORE any existing non-phi Op.
//
// Callers building a block bottom-up (terminator first, then
// body, then phis) get the placement right for free; callers
// building top-down can also use this since it preserves the
// invariant by splicing at the first non-phi index.
func (f *Func) AddPhi(b *Block, args ...Value) Value {
	result := f.NewValue()
	op := &Op{Kind: OpPhi, Result: result, Args: append([]Value(nil), args...)}
	insert := 0
	for insert < len(b.Ops) && b.Ops[insert].Kind == OpPhi {
		insert++
	}
	b.Ops = append(b.Ops, nil)
	copy(b.Ops[insert+1:], b.Ops[insert:])
	b.Ops[insert] = op
	return result
}

// SetBr sets `b`'s terminator to an unconditional branch
// to `target`. Updates `target.Preds` to include `b`.
func (f *Func) SetBr(b *Block, target *Block) {
	b.Term = Terminator{Kind: TermBr, Target: target}
	target.Preds = appendUnique(target.Preds, b)
}

// SetBrIf sets `b`'s terminator to a conditional branch on
// `cond`, with successors `tBlock` (true) and `fBlock`
// (false). Updates both successors' Preds.
func (f *Func) SetBrIf(b *Block, cond Value, tBlock, fBlock *Block) {
	b.Term = Terminator{Kind: TermBrIf, Cond: cond, True: tBlock, False: fBlock}
	tBlock.Preds = appendUnique(tBlock.Preds, b)
	fBlock.Preds = appendUnique(fBlock.Preds, b)
}

// SetRet sets `b`'s terminator to a return. Pass the zero
// Value sentinel for void returns.
func (f *Func) SetRet(b *Block, v Value) {
	b.Term = Terminator{Kind: TermRet, Value: v}
}

// SetRetPair sets `b`'s terminator to a pair-return — the
// (tag, payload) calling convention used for Option/Result
// returns lowered via OpReturnPair in the legacy IR. Both
// Values flow out together; backends emit a multi-value
// return (wasm `(result tag_t payload_t)`, x86-64 SysV
// rax+rdx, AArch64 x0+x1).
func (f *Func) SetRetPair(b *Block, tag, payload Value) {
	b.Term = Terminator{Kind: TermRetPair, Value: tag, Value2: payload}
}

// HasSideEffects reports whether `f` contains any impure op
// (Call, Load, Store, Alloc, MakeClosure/Env, or their
// sub-width Load/Store variants). Pure-only functions return
// false.
//
// Bails early on the first impure op; O(ops-until-first-hit)
// in the worst case (a fully pure function), much cheaper
// than building a full Stats snapshot when callers only need
// the yes/no.
func (f *Func) HasSideEffects() bool {
	if f == nil {
		return false
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if !IsPure(op.Kind) {
				return true
			}
		}
	}
	return false
}

// appendUnique appends `x` to `s` if it's not already
// present. Preds shouldn't have duplicates (a block can't
// branch to the same successor twice in SSA terms).
func appendUnique(s []*Block, x *Block) []*Block {
	for _, b := range s {
		if b == x {
			return s
		}
	}
	return append(s, x)
}

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

	// Arithmetic — integer.
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpRem

	// Bitwise — integer (i32 / i64).
	OpAnd
	OpOr
	OpXor

	// Shift — integer. Shl is logical left; Shr is arithmetic
	// (sign-preserving) right. Shift count comes from Args[1];
	// out-of-range counts (rhs < 0 or rhs >= 64) are left
	// unfolded — the runtime owns that case.
	OpShl
	OpShr

	// Unary arithmetic — integer negation.
	OpNeg

	// Unary logical — boolean negation. Args[0] is a bool
	// Value; result is the flipped bool.
	OpNot

	// Comparison — produces a boolean Value.
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe

	// Constants.
	OpConstInt    // Imm carries the value
	OpConstBool   // Imm == 0 or 1
	OpConstString // Str carries the value

	// Function call. `Args[0]` is the callee (a Value of
	// function type once we grow types; for Phase 1 the
	// Str field carries the callee name and Args[0..]
	// are the call's arguments).
	OpCall

	// Load / Store — memory access. Index-into-array,
	// struct-field-access, etc. resolve to these.
	OpLoad
	OpStore

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
	case OpRem:
		return "rem"
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
	case OpNeg:
		return "neg"
	case OpNot:
		return "not"
	case OpEq:
		return "eq"
	case OpNe:
		return "ne"
	case OpLt:
		return "lt"
	case OpLe:
		return "le"
	case OpGt:
		return "gt"
	case OpGe:
		return "ge"
	case OpConstInt:
		return "const_int"
	case OpConstBool:
		return "const_bool"
	case OpConstString:
		return "const_string"
	case OpCall:
		return "call"
	case OpLoad:
		return "load"
	case OpStore:
		return "store"
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
	Args   []Value

	// Op-kind-specific immediate fields. Mutually exclusive
	// in practice — each kind uses at most one.
	Imm int64  // OpConstInt / OpConstBool
	Str string // OpConstString / OpCall (callee name)
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
)

// Terminator ends a Block. Succs are populated from `Target`
// (Br), `True` + `False` (BrIf), or empty (Ret).
type Terminator struct {
	Kind   TermKind
	Cond   Value   // BrIf only
	Target *Block  // Br only
	True   *Block  // BrIf only
	False  *Block  // BrIf only
	Value  Value   // Ret only; zero sentinel for void returns
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

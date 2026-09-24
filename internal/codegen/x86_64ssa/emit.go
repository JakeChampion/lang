// Package x86_64ssa is the SSA→x86-64 register-based emit path (phase 2 of the
// SSA-level register allocator, #4112). It consumes allocated SSA — produced by
// ssa.LinearScan — instead of walking the IR as a stack machine, so values live
// in registers and only spill when the register file is exhausted.
//
// Coverage so far: the integer subset (arithmetic / bitwise / shift / comparison
// / const) plus full control flow — multiple blocks, conditional/unconditional
// branches, and phi resolution (out-of-SSA). It emits an abstract
// register-machine program (Inst over MBlocks) rather than final GAS text, and
// is validated differentially against ssa.Eval via the model interpreter Run.
// That proves the regalloc- and out-of-SSA-specific logic — operand assignment,
// the x86 two-address fixup, spill load/store, and phi-move sequentialisation /
// critical-edge splitting — independently of final-assembly concerns. Real
// GAS-text emission (call ABI, idiv's rax/rdx pinning, the ELF _start runtime)
// is a later slice; unsupported ops return a clear error.
package x86_64ssa

import (
	"fmt"
	"math"
	"math/bits"
	"sort"

	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/ssa"
)

// Scratch registers sit above the allocatable file. s0/s1 hold materialised
// (possibly reloaded-from-slot) operands; s2 accumulates a result before it is
// placed; s3 stages a value through memory during slot↔slot moves.
const numScratch = 4

// Opcode is the abstract register-machine operation.
type Opcode int

const (
	MovImm       Opcode = iota // reg[Dst] = Imm
	MovReg                     // reg[Dst] = reg[Src]
	BinOp                      // reg[Dst] = reg[Dst] (K) reg[Src]   (K an integer arith op)
	UnNeg                      // reg[Dst] = -reg[Dst]
	UnOp                       // reg[Dst] = K(reg[Dst])   (K a unary: Not / Trunc / Extend*)
	SetCmp                     // reg[Dst] = (reg[Dst] K reg[Src]) ? 1 : 0   (K a comparison)
	LoadSlot                   // reg[Dst] = slot[Imm]
	StoreSlot                  // slot[Imm] = reg[Src]
	Call                       // reg[Dst] = Callee(ArgLocs...)   (model: recurse into callee Program)
	MemAlloc                   // reg[Dst] = heap.alloc(reg[Src])
	MemLoad                    // reg[Dst] = heap[reg[Src] + Imm]
	MemStore                   // heap[reg[Src] + Imm] = reg[Src2]
	ConstStr                   // reg[Dst] = pointer to freshly heap-materialised Str bytes
	FConst                     // reg[Dst] = f64 bits of F64 (rounded to f32 if W==32)
	FBin                       // reg[Dst] = reg[Dst] (K) reg[Src] as floats   (K a float arith op)
	FCmp                       // reg[Dst] = (reg[Dst] K reg[Src]) as floats ? 1 : 0
	FConv                      // reg[Dst] = K(reg[Dst])   (K a float unary/convert: FNeg/FPromote/FDemote/IToF*/FToI*)
	EnumSentinel               // reg[Dst] = shared static sentinel pointer for tag Imm
	CallPair                   // reg[Dst], reg[Dst2] = Callee(ArgLocs...)   (two-result direct call)
	CallIndirect               // reg[Dst] = table[readLoc(IdxLoc)](ArgLocs...)   (fn-index dispatch)
	MakeEnv                    // reg[Dst] = env block of the ArgLocs captures (8B slots)
	MakeClosure                // reg[Dst] = {fn_idx(Callee), env_ptr} cell over the ArgLocs captures
	Select                     // reg[Dst] = reg[Src] != 0 ? reg[Src2] : reg[Src3]

	// dyn Trait dispatch (docs/DYN-TRAITS.md §4.2.2, BOXED one-word). Only the
	// arm64-ssa render path emits these to asm; the x86-64-ssa render path
	// never receives them (only arm64-ssa opts into ir.DynSupported).
	ConstVtable // reg[Dst] = &vtable(Str)   (.rodata address of a (trait-set/concrete) vtable)
	BoxDyn      // reg[Dst] = {data=ArgLocs[0], vtable=ArgLocs[1]} 16-byte cell (calls the allocator)
	CallDyn     // reg[Dst] = (*(ArgLocs[last] + Imm*8))(ArgLocs[0..last-1])   (vtable-slot indirect call)
)

// Inst is one straight-line abstract instruction. Registers are indices into a
// flat file of size Program.NumRegFile (allocatable registers, then scratch).
type Inst struct {
	Op   Opcode
	Dst  int
	Dst2 int // second destination register (CallPair: the payload result)
	Src  int
	Src2 int // second source register (MemStore: the value; Select: the then value)
	Src3 int // third source register (Select: the else value)
	Imm  int64
	K    ssa.OpKind // BinOp / SetCmp operation
	W    int8       // result width for MovImm/BinOp/UnNeg/Call (0/32 => i32, 64 => i64)

	// Narrow: no use of this result reads bits above 31, so the i32
	// high-half fix is dead code here. Only meaningful when W != 64.
	// See ssa.FindNarrowResults for why the analysis is a whitelist.
	Narrow bool

	// SrcImm (BinOp/SetCmp): the right operand is Imm rather than reg[Src].
	// On a MemAlloc, or a Call to __fern_box_free, it says the size is the
	// constant Imm as well as reg[Src] / the size argument's home, so a
	// renderer can pick the freelist class at compile time.
	// The constant that would have been materialised has every use in this
	// position, so no register ever holds it.
	SrcImm bool

	Bytes  int8 // MemLoad/MemStore access width in bytes (1/2/8)
	Signed bool // MemLoad: sign-extend a sub-word value

	Callee  string  // Call: callee function name
	ArgLocs []Loc   // Call: homes of the argument values, in order
	IdxLoc  Loc     // CallIndirect: home of the function-index value (Args[0])
	Str     string  // ConstStr: the literal bytes to materialise
	F64     float64 // FConst: the float value

	// CaptureSlots: MakeEnv/MakeClosure per-capture env-slot byte sizes, in
	// order (nil => one 8-byte slot each). Drives the packed env layout.
	CaptureSlots []int32

	// SaveRegs (Call/CallPair/CallIndirect): the caller-saved allocatable
	// registers holding values live ACROSS this call — the only ones the
	// caller must preserve. Computed from liveness when SaveRegsSet is true;
	// otherwise the emitter falls back to saving every caller-saved register.
	SaveRegs    []int
	SaveRegsSet bool
}

// TermKind is an MBlock terminator shape.
type TermKind int

const (
	TJmp     TermKind = iota // unconditional jump to Target
	TBrIf                    // if reg[CondReg] != 0 goto True else goto False
	TRet                     // return reg[RetReg]
	TRetPair                 // return (reg[RetReg], reg[RetReg2]) — pair-return convention
)

// Term ends an MBlock.
type Term struct {
	Kind    TermKind
	Target  int // TJmp
	CondReg int // TBrIf
	True    int // TBrIf
	False   int // TBrIf
	RetReg  int // TRet / TRetPair (tag)
	RetReg2 int // TRetPair (payload)

	// CondFuse (TBrIf) reports that CondReg is defined by the SetCmp that ends
	// this block's instruction run and is read by nothing else. A backend whose
	// conditional branch tests condition flags directly can then branch on the
	// comparison and skip materialising the 0/1. It is an annotation only: the
	// SetCmp is still present and still defines CondReg, so a backend that
	// ignores the flag stays correct.
	CondFuse bool
}

// MBlock is a straight-line instruction run plus a terminator.
type MBlock struct {
	Insts []Inst
	Term  Term
}

// Loc is a value's home: a register (IsReg) or a spill slot. A dead param has
// IsReg=false and Slot=-1.
type Loc struct {
	IsReg bool
	Reg   int
	Slot  int
}

func (a Loc) eq(b Loc) bool {
	if a.IsReg != b.IsReg {
		return false
	}
	if a.IsReg {
		return a.Reg == b.Reg
	}
	return a.Slot == b.Slot
}

// Program is the emitted abstract function.
type Program struct {
	Blocks     []MBlock
	Entry      int
	NumRegFile int // allocatable + scratch
	NumSlots   int // includes phi-move temp slots
	ParamLocs  []Loc
}

// Emit lowers an integer SSA function (with control flow) to an abstract
// register program, allocating over numAlloc physical registers, using the
// x86-64 System V callee-saved partition (rbx / r12–r15).
func Emit(f *ssa.Func, numAlloc int) (*Program, error) {
	// Tell the allocator which allocatable registers are callee-saved, so it can
	// steer call-crossing values there and avoid a per-call caller-save (EQ-1
	// then leaves them out of the save set; the function prologue preserves them
	// once via calleeSavedUsed).
	calleeSaved := make([]bool, numAlloc)
	for r := 0; r < numAlloc; r++ {
		calleeSaved[r] = !isCallerSaved(r)
	}
	return EmitWithCalleeSaved(f, numAlloc, calleeSaved)
}

// EmitWithCalleeSaved is Emit with an explicit callee-saved partition, letting a
// non-x86 backend describe its own ABI. calleeSaved[r] reports whether
// allocatable register r survives a call; a target whose whole allocatable file
// is caller-saved (e.g. arm64 mapping onto x0–x15) passes an all-false mask, so
// the allocator marks every call-crossing value for caller-save (SaveRegsSet).
// A nil mask defaults to all-false (every register caller-saved).
func EmitWithCalleeSaved(f *ssa.Func, numAlloc int, calleeSaved []bool) (*Program, error) {
	if numAlloc < 1 {
		return nil, fmt.Errorf("x86_64ssa: numAlloc must be >= 1")
	}
	if f.Entry == nil {
		return nil, fmt.Errorf("x86_64ssa: function has no entry block")
	}
	if calleeSaved != nil && len(calleeSaved) != numAlloc {
		return nil, fmt.Errorf("x86_64ssa: calleeSaved has %d entries, want numAlloc=%d", len(calleeSaved), numAlloc)
	}
	alloc := ssa.LinearScan(f, ssa.Target{NumRegs: numAlloc, CalleeSaved: calleeSaved})
	e := &emitter{
		f:        f,
		alloc:    alloc,
		uses:     alloc.Uses,
		narrow:   ssa.FindNarrowResults(f, alloc.Uses),
		numAlloc: numAlloc,
		folded:   foldableConsts(f),
		s0:       numAlloc, s1: numAlloc + 1, s2: numAlloc + 2, s3: numAlloc + 3,
		idx:        map[*ssa.Block]int{},
		phiTempCap: maxPhiCount(f),
		strLen:     map[int32]int{},
		consts:     map[int32]int64{},
		// A register is caller-saved under this target iff it is not in the
		// callee-saved partition. nil mask => whole file caller-saved (arm64).
		callerSaved: func(r int) bool { return calleeSaved == nil || !calleeSaved[r] },
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind == ssa.OpConstString && op.Result.IsValid() {
				e.strLen[op.Result.ID] = len(op.Str)
			}
			if op.Kind == ssa.OpConstInt && op.Result.IsValid() {
				e.consts[op.Result.ID] = op.Imm
			}
		}
	}
	e.remat = rematerialisable(f, alloc, e.uses, e.folded)
	// Phi-move temp slots live just above the allocator's spill slots and are
	// reused across edges (edges never execute concurrently).
	e.phiTempBase = alloc.NumSlots
	e.numSlots = alloc.NumSlots + e.phiTempCap

	// Only emit blocks reachable from entry. The allocator's liveness runs over
	// the reachable CFG (RPO from entry), so values defined solely in an
	// unreachable block get no allocation; emitting such a block would fail to
	// materialise them. Unreachable blocks (e.g. a lowerer-left dead epilogue
	// after both arms of an if return) are never branch targets of a reachable
	// block, so skipping them can't dangle a jump.
	reachable := ssa.Reachable(f)

	// Pre-assign an MBlock index to every reachable SSA block so branch targets
	// resolve; split blocks for critical edges are appended afterwards.
	for _, b := range f.Blocks {
		if !reachable[b] {
			continue
		}
		e.idx[b] = len(e.blocks)
		e.blocks = append(e.blocks, MBlock{})
	}

	for _, b := range f.Blocks {
		if !reachable[b] {
			continue
		}
		if err := e.emitBlock(b); err != nil {
			return nil, err
		}
	}

	return &Program{
		Blocks:     e.blocks,
		Entry:      e.idx[f.Entry],
		NumRegFile: numAlloc + numScratch,
		NumSlots:   e.numSlots,
		ParamLocs:  e.paramLocs(),
	}, nil
}

// EmitModule lowers a set of functions (keyed by name) to abstract Programs,
// so direct calls between them resolve at Run time. Each function is allocated
// independently over numAlloc registers.
func EmitModule(funcs map[string]*ssa.Func, numAlloc int) (map[string]*Program, error) {
	out := make(map[string]*Program, len(funcs))
	for name, f := range funcs {
		p, err := Emit(f, numAlloc)
		if err != nil {
			return nil, fmt.Errorf("emit %q: %w", name, err)
		}
		out[name] = p
	}
	return out, nil
}

type emitter struct {
	f     *ssa.Func
	alloc *ssa.Allocation
	uses  *ssa.Uses
	// narrow: results no use reads above bit 31, so the i32 high-half fix
	// can be left off them (ssa.FindNarrowResults).
	narrow *ssa.NarrowResults
	// folded: OpConstInt results every reader takes as an immediate right
	// operand (foldableConsts), so no MovImm is emitted for them.
	folded         map[int32]int64
	numAlloc       int
	s0, s1, s2, s3 int

	// callerSaved reports whether allocatable register r is caller-saved under
	// the target ABI — the partition that decides which live-across register
	// homes a call must preserve. It tracks the calleeSaved mask passed to
	// EmitWithCalleeSaved, so a non-x86 backend (e.g. arm64, whole file
	// caller-saved) computes the correct save set instead of the x86 default.
	callerSaved func(int) bool

	blocks []MBlock
	idx    map[*ssa.Block]int

	phiTempBase int
	phiTempCap  int
	numSlots    int
	strLen      map[int32]int // OpConstString result ID -> literal byte length
	// consts is every OpConstInt result's value, for the sites that can use a
	// size known at compile time (MemAlloc, __fern_box_free).
	consts map[int32]int64
	// remat is every spilled constant whose readers all take their operand
	// through materialize: it has no definition in the text and no slot
	// store, and each reader writes the constant into its scratch register
	// where a reload would have gone (rematerialisable).
	remat map[int32]*ssa.Op

	cur []Inst // instruction accumulator for the block being emitted
}

func (e *emitter) push(i Inst) { e.cur = append(e.cur, i) }

func (e *emitter) loc(id int32) (Loc, bool) {
	if r, ok := e.alloc.Reg[id]; ok {
		return Loc{IsReg: true, Reg: r}, true
	}
	if s, ok := e.alloc.Slot[id]; ok {
		return Loc{IsReg: false, Slot: s}, true
	}
	return Loc{}, false
}

// callSaveRegs returns the caller-saved allocatable registers holding values
// live across the call op `op` — the minimal save set — and true when it could
// be computed. Spilled values need no save (they survive on the stack) and
// callee-saved registers are preserved by the callee, so only caller-saved
// register homes of live-across values are returned. Returns (nil, false) when
// the op has no program point (then the caller conservatively saves everything).
func (e *emitter) callSaveRegs(op *ssa.Op) ([]int, bool) {
	live, ok := e.alloc.LiveAcrossOp(op)
	if !ok {
		return nil, false
	}
	seen := map[int]bool{}
	var regs []int
	for id := range live {
		r, isReg := e.alloc.Reg[id]
		if !isReg || r >= e.numAlloc || !e.callerSaved(r) || seen[r] {
			continue
		}
		seen[r] = true
		regs = append(regs, r)
	}
	sort.Ints(regs)
	return regs, true
}

// emitBlock emits one SSA block's straight-line ops, its phi moves, and its
// terminator into the corresponding MBlock.
func (e *emitter) emitBlock(b *ssa.Block) error {
	// An SSA op lowers to 1.38 machine instructions across the self-host
	// driver, and a block adds its terminator and any edge moves on top, so
	// this covers a block in one allocation where growing from nothing took
	// five.
	e.cur = make([]Inst, 0, len(b.Ops)*3/2+4)
	for _, op := range b.Ops {
		if op.Kind == ssa.OpPhi {
			continue // phis are resolved as edge moves, not in-block
		}
		if err := e.emitOp(op); err != nil {
			return err
		}
	}

	bi := e.idx[b]
	switch b.Term.Kind {
	case ssa.TermRet:
		var rr int
		if b.Term.Value.IsValid() {
			var err error
			if rr, err = e.materialize(b.Term.Value, e.s0); err != nil {
				return err
			}
		} else {
			e.push(Inst{Op: MovImm, Dst: e.s0, Imm: 0})
			rr = e.s0
		}
		e.blocks[bi].Insts = e.cur
		e.blocks[bi].Term = Term{Kind: TRet, RetReg: rr}

	case ssa.TermRetPair:
		// Materialise tag and payload into distinct scratch registers so loading
		// the second (from a slot) can't clobber the first.
		tag, err := e.materialize(b.Term.Value, e.s0)
		if err != nil {
			return err
		}
		payload, err := e.materialize(b.Term.Value2, e.s1)
		if err != nil {
			return err
		}
		e.blocks[bi].Insts = e.cur
		e.blocks[bi].Term = Term{Kind: TRetPair, RetReg: tag, RetReg2: payload}

	case ssa.TermBr:
		// Single successor: phi moves go at the end of this block.
		e.emitEdgeMoves(b, b.Term.Target)
		e.blocks[bi].Insts = e.cur
		e.blocks[bi].Term = Term{Kind: TJmp, Target: e.idx[b.Term.Target]}

	case ssa.TermBrIf:
		// Two successors: the condition is materialised here; each edge's phi
		// moves go into a split block (uniform + always correct, incl. the
		// critical-edge case) when the edge carries any.
		cond, err := e.materialize(b.Term.Cond, e.s0)
		if err != nil {
			return err
		}
		e.blocks[bi].Insts = e.cur
		fuse := e.condFusable(b.Term.Cond, cond)
		tTarget := e.edgeTarget(b, b.Term.True)
		fTarget := e.edgeTarget(b, b.Term.False)
		e.blocks[bi].Term = Term{Kind: TBrIf, CondReg: cond, True: tTarget, False: fTarget, CondFuse: fuse}

	default:
		return fmt.Errorf("x86_64ssa: unsupported terminator %v", b.Term.Kind)
	}
	return nil
}

// condFusable reports whether the just-emitted block ends in the SetCmp that
// defines `cond`'s home register and nothing other than the terminator reads
// the comparison's value — the precondition for Term.CondFuse.
func (e *emitter) condFusable(v ssa.Value, cond int) bool {
	n := len(e.cur)
	if n == 0 || e.cur[n-1].Op != SetCmp || e.cur[n-1].Dst != cond {
		return false
	}
	// BuildUses counts the BrIf's own read of Cond, so exactly one use means
	// the terminator is the only one.
	return e.uses.Count(v) == 1
}

// edgeTarget returns the MBlock index to branch to for edge b→s: s directly if
// the edge carries no phi moves, otherwise a freshly-appended split block that
// performs the moves then jumps to s.
func (e *emitter) edgeTarget(b, s *ssa.Block) int {
	moves := e.edgeMoves(b, s)
	if len(moves) == 0 {
		return e.idx[s]
	}
	saved := e.cur
	e.cur = nil
	e.emitParallelMoves(moves)
	split := MBlock{Insts: e.cur, Term: Term{Kind: TJmp, Target: e.idx[s]}}
	e.cur = saved
	e.blocks = append(e.blocks, split)
	return len(e.blocks) - 1
}

// emitEdgeMoves appends edge b→s's phi moves to the current block (used when b
// has a single successor, so the moves can't disturb a sibling edge).
func (e *emitter) emitEdgeMoves(b, s *ssa.Block) {
	e.emitParallelMoves(e.edgeMoves(b, s))
}

// move is a single parallel-copy entry: dst <- src, both value homes.
type move struct{ dst, src Loc }

// edgeMoves returns the phi assignments for edge b→s: each phi in s takes its
// arg from b's predecessor slot. Dead phis and self-moves are dropped.
func (e *emitter) edgeMoves(b, s *ssa.Block) []move {
	pi := -1
	for i, p := range s.Preds {
		if p == b {
			pi = i
			break
		}
	}
	if pi < 0 {
		return nil
	}
	var moves []move
	for _, op := range s.Ops {
		if op.Kind != ssa.OpPhi {
			break // phis are at the top
		}
		if pi >= len(op.Args) {
			continue
		}
		dst, ok := e.loc(op.Result.ID)
		if !ok {
			continue // dead phi
		}
		src, ok := e.loc(op.Args[pi].ID)
		if !ok {
			continue
		}
		if !dst.eq(src) {
			moves = append(moves, move{dst: dst, src: src})
		}
	}
	return moves
}

// emitParallelMoves realises a set of simultaneous copies, ordering them so
// that each is emitted only once nothing else still needs to read its
// destination. Destinations are distinct (a value has one home), so the only
// thing that can stall the ordering is a cycle, and only a cycle needs a temp.
//
// Routing every move through a temp slot instead — read all, then write all —
// is also correct, and is what this did before. But it costs a store and a load
// per move where a single copy would do, on every loop back edge, and phi edges
// are dense in loop-shaped code: that was the largest single source of the SSA
// backend's remaining memory traffic (docs/SSA-REGALLOC-PLAN.md).
func (e *emitter) emitParallelMoves(moves []move) {
	for _, m := range sequentialMoves(moves, e.phiTempBase) {
		e.moveLoc(m.dst, m.src)
	}
}

// sequentialMoves orders a parallel copy into a sequence of ordinary ones,
// inserting a park into a temp slot (from tempBase upward) only where a cycle
// leaves no move that can go first. Pure, so the ordering can be tested without
// running instruction selection.
func sequentialMoves(moves []move, tempBase int) []move {
	var out []move
	pending := append([]move(nil), moves...)
	// readsFrom reports whether any pending move other than the one at skip
	// still needs to read loc.
	readsFrom := func(loc Loc, skip int) bool {
		for i, m := range pending {
			if i != skip && m.src.eq(loc) {
				return true
			}
		}
		return false
	}
	temp := 0
	for len(pending) > 0 {
		moved := false
		for i := 0; i < len(pending); {
			if readsFrom(pending[i].dst, i) {
				i++
				continue
			}
			out = append(out, pending[i])
			pending = append(pending[:i], pending[i+1:]...)
			moved = true
		}
		if len(pending) == 0 || moved {
			continue
		}
		// Every remaining move is blocked, so the rest is cycles. Park one
		// destination's current value in a temp and let whoever needed it read
		// the temp instead; that frees the destination and the cycle unrolls.
		park := Loc{Slot: tempBase + temp}
		temp++
		out = append(out, move{dst: park, src: pending[0].dst})
		for i := range pending {
			if i != 0 && pending[i].src.eq(pending[0].dst) {
				pending[i].src = park
			}
		}
	}
	return out
}

// moveLoc emits dst <- src for any reg/slot combination, staging through the s3
// scratch register for slot→slot.
func (e *emitter) moveLoc(dst, src Loc) {
	if dst.eq(src) {
		return
	}
	switch {
	case dst.IsReg && src.IsReg:
		e.push(Inst{Op: MovReg, Dst: dst.Reg, Src: src.Reg})
	case dst.IsReg && !src.IsReg:
		e.push(Inst{Op: LoadSlot, Dst: dst.Reg, Imm: int64(src.Slot)})
	case !dst.IsReg && src.IsReg:
		e.push(Inst{Op: StoreSlot, Imm: int64(dst.Slot), Src: src.Reg})
	default: // slot <- slot
		e.push(Inst{Op: LoadSlot, Dst: e.s3, Imm: int64(src.Slot)})
		e.push(Inst{Op: StoreSlot, Imm: int64(dst.Slot), Src: e.s3})
	}
}

func (e *emitter) materialize(v ssa.Value, scratch int) (int, error) {
	if c := e.remat[v.ID]; c != nil {
		if c.Kind == ssa.OpConstString {
			e.push(Inst{Op: ConstStr, Dst: scratch, Str: c.Str})
		} else {
			e.push(Inst{Op: MovImm, Dst: scratch, Imm: c.Imm, W: c.Width})
		}
		return scratch, nil
	}
	l, ok := e.loc(v.ID)
	if !ok {
		return 0, fmt.Errorf("x86_64ssa: value v%d has no allocation (dead?)", v.ID)
	}
	if l.IsReg {
		return l.Reg, nil
	}
	e.push(Inst{Op: LoadSlot, Dst: scratch, Imm: int64(l.Slot)})
	return scratch, nil
}

// rematerialisable finds the constants the allocator spilled whose every
// reader takes its operand through materialize, so the constant can be
// written into the reader's scratch register in place of the reload and
// needs neither a definition nor a slot store. A phi, a call argument, a
// closure or env capture and a boxing read their operand's home directly,
// so one such reader keeps the constant's slot.
//
// It is a saving in bytes only when the readers are few enough: the
// constant is seven bytes (`mov r64, imm32`, `lea r64, [rip + sym]`) where
// a reload from one of the first sixteen slots is four, so a near-slot
// constant read more than three times is smaller stored and reloaded.
func rematerialisable(f *ssa.Func, alloc *ssa.Allocation, uses *ssa.Uses, folded map[int32]int64) map[int32]*ssa.Op {
	const constBytes = 7
	out := map[int32]*ssa.Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			switch op.Kind {
			case ssa.OpConstInt, ssa.OpConstBool, ssa.OpConstString:
			default:
				continue
			}
			if !op.Result.IsValid() {
				continue
			}
			slot, ok := alloc.Slot[op.Result.ID]
			if !ok {
				continue
			}
			if _, ok := folded[op.Result.ID]; ok {
				continue
			}
			reloadBytes := 7
			if 8*(slot+1) <= 128 {
				reloadBytes = 4
			}
			sites := uses.Of(op.Result)
			if len(sites)*(constBytes-reloadBytes) > constBytes+reloadBytes {
				continue
			}
			ok = true
			for _, u := range sites {
				if u.Op != nil && readsHomeDirectly(u.Op.Kind) {
					ok = false
					break
				}
			}
			if ok {
				out[op.Result.ID] = op
			}
		}
	}
	return out
}

// readsHomeDirectly reports whether an op's emitter reads its operands'
// homes as locations (loc) rather than through materialize.
func readsHomeDirectly(k ssa.OpKind) bool {
	switch k {
	case ssa.OpPhi, ssa.OpCall, ssa.OpCallPair, ssa.OpCallIndirect, ssa.OpCallDyn,
		ssa.OpMakeEnv, ssa.OpMakeClosure, ssa.OpBoxDyn:
		return true
	}
	return false
}

func (e *emitter) place(v ssa.Value, srcReg int) {
	l, ok := e.loc(v.ID)
	if !ok {
		return // dead result
	}
	if l.IsReg {
		if l.Reg != srcReg {
			e.push(Inst{Op: MovReg, Dst: l.Reg, Src: srcReg})
		}
		return
	}
	e.push(Inst{Op: StoreSlot, Imm: int64(l.Slot), Src: srcReg})
}

// movReg pushes a register-to-register move, eliding the no-op when the two
// registers coincide (which happens once a result is coalesced into an operand's
// own register home).
func (e *emitter) movReg(dst, src int) {
	if dst != src {
		e.push(Inst{Op: MovReg, Dst: dst, Src: src})
	}
}

// coalesceDst chooses the register an op computes its result into: the result's
// own register home when it has one and that register isn't `avoid` — an operand
// the op still reads after the compute-in move — so the result lands in place
// without a separate placing move. Falls back to the scratch s2 (the caller then
// place()s it) when the result is spilled, has no home, or its home collides with
// `avoid`. Pass avoid = -1 for single-operand ops (no such collision). When the
// returned register is not s2 the caller must skip place(): the value is already
// home.
func (e *emitter) coalesceDst(result ssa.Value, avoid int) int {
	if l, ok := e.loc(result.ID); ok && l.IsReg && l.Reg != avoid {
		return l.Reg
	}
	return e.s2
}

// swapForDst reorders an op's operands when the result's register home is the
// RIGHT one. Left as they are, coalesceDst has to refuse that home — the
// compute-in move would clobber rb before the op reads it — and stage through
// the s2 scratch, costing a move in and a copy out. Read the other way round
// the compute-in move is a self-move and the result is already home.
//
// A commutative op is the same value either way round. A directional
// comparison is not, so it swaps under the flipped predicate (`a < b` read as
// `b > a`), which is the same value again. Anything else keeps its order and
// pays the scratch.
//
// Returning the op kind matters beyond the moves saved: a comparison staged
// through the scratch is no longer the last instruction defining the
// terminator's condition register, so condFusable refuses it and the block
// materialises a boolean — cmp/setcc/movzx/test/jcc — where a fused cmp/jcc
// would do. That is five instructions a loop test pays every iteration.
func (e *emitter) swapForDst(op *ssa.Op, ra, rb int) (ssa.OpKind, int, int) {
	if ra == rb || !op.Result.IsValid() {
		return op.Kind, ra, rb
	}
	l, ok := e.loc(op.Result.ID)
	if !ok || !l.IsReg || l.Reg != rb {
		return op.Kind, ra, rb
	}
	if ssa.IsCommutative(op.Kind) {
		return op.Kind, rb, ra
	}
	if flipped, ok := ssa.FlipDirectionalCmp(op.Kind); ok {
		return flipped, rb, ra
	}
	return op.Kind, ra, rb
}

// callResultDst names the register a call should deliver its result into: the
// result's own register home when it has one, else the given staging scratch
// (from which place() stores it to its slot, or drops it when the result is
// dead).
//
// A call renderer writes Dst only AFTER restoring the caller-saved registers,
// so naming a home here is safe whatever that home is — it removes the separate
// place() copy, not an ordering constraint. Capturing INTO Dst ahead of those
// restores is the renderers' own further step, and is guarded there.
func (e *emitter) callResultDst(result ssa.Value, staging int) int {
	if l, ok := e.loc(result.ID); ok && l.IsReg {
		return l.Reg
	}
	return staging
}

// immediateRight reports the constant an op's right operand folds into, when
// foldableConsts admitted it.
func (e *emitter) immediateRight(op *ssa.Op) (int64, bool) {
	if len(op.Args) != 2 {
		return 0, false
	}
	imm, ok := e.folded[op.Args[1].ID]
	return imm, ok
}

// foldableConsts finds the integer constants that can be an x86 immediate at
// every site that reads them: an i32-range value whose readers are all the
// right operand of a comparison or of a direct-form binary op. Those readers
// render `cmp r, imm` / `add r, imm` and the constant needs no register.
// Anything else — a phi, a call argument, a store, a terminator, the LEFT
// operand — keeps the materialising MovImm, so a partially foldable constant
// is not folded at all.
func foldableConsts(f *ssa.Func) map[int32]int64 {
	uses := ssa.BuildUses(f)
	out := map[int32]int64{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind != ssa.OpConstInt || !op.Result.IsValid() {
				continue
			}
			if op.Imm < -(1<<31) || op.Imm >= (1<<31) {
				continue
			}
			sites := uses.Of(op.Result)
			if len(sites) == 0 {
				continue
			}
			ok := true
			for _, u := range sites {
				if u.Op == nil || u.Index != 1 || !foldableRightUse(u.Op, op.Imm) {
					ok = false
					break
				}
			}
			if ok {
				out[op.Result.ID] = op.Imm
			}
		}
	}
	return out
}

// foldableRightUse reports whether a reader takes the constant imm as its right
// operand without a register behind it: the immediate-form ops, a shift
// (whose count is an imm8, masked to the width as a register count would
// be), and a division or remainder by a power of two, which lowers to shifts
// (emitPow2DivRem).
func foldableRightUse(reader *ssa.Op, imm int64) bool {
	if immediateRightKind(reader.Kind) {
		return true
	}
	switch reader.Kind {
	case ssa.OpShl, ssa.OpShr, ssa.OpShrU, ssa.OpRotr:
		return true
	case ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
		if _, ok := powerOfTwoDivisor(imm, reader.Width); ok {
			return true
		}
		return magicDivisor(imm, reader.Kind, reader.Width)
	}
	return false
}

// magicDivisor reports whether an i32 division or remainder by the constant
// n lowers to a multiply by its reciprocal (emitMagicDivRem): every divisor
// but the ones with a cheaper or no lowering. Unsigned reads n's low 32 bits;
// signed excludes 0, ±1 (the identities) and the most negative value, which
// has no magnitude. A power of two, of either sign, is left to the shift
// lowering or the real division. Only the 32-bit reciprocals are derived.
func magicDivisor(n int64, k ssa.OpKind, w int8) bool {
	if w == 64 {
		return false
	}
	if k == ssa.OpDivU || k == ssa.OpRemU {
		d := uint32(n)
		return d > 1 && d&(d-1) != 0
	}
	v := int32(n)
	if v == 0 || v == 1 || v == -1 || v == math.MinInt32 {
		return false
	}
	mag := uint32(v)
	if v < 0 {
		mag = uint32(-v)
	}
	return mag&(mag-1) != 0
}

// powerOfTwoDivisor is the shift a division or remainder by the constant n
// reduces to: n = 1 << sh with sh between 1 and width-2. sh = 0 is the
// identity StrengthReduce already removes, and 1 << (width-1) is the sign bit,
// whose signed quotient the bias below would not reach.
func powerOfTwoDivisor(n int64, w int8) (int64, bool) {
	width := int64(32)
	if w == 64 {
		width = 64
	}
	if n <= 1 || n&(n-1) != 0 {
		return 0, false
	}
	sh := int64(bits.TrailingZeros64(uint64(n)))
	if sh > width-2 {
		return 0, false
	}
	return sh, true
}

// immediateRightKind is the set of ops whose right operand may be an imm32:
// the comparisons and the binary ops binMnemonic renders in two-operand form.
func immediateRightKind(k ssa.OpKind) bool {
	switch k {
	case ssa.OpEq, ssa.OpNe, ssa.OpLt, ssa.OpLtU, ssa.OpLe, ssa.OpLeU,
		ssa.OpGt, ssa.OpGtU, ssa.OpGe, ssa.OpGeU,
		ssa.OpAdd, ssa.OpSub, ssa.OpAnd, ssa.OpOr, ssa.OpXor:
		return true
	}
	return false
}

func (e *emitter) emitOp(op *ssa.Op) error {
	switch op.Kind {
	case ssa.OpConstInt, ssa.OpConstBool:
		if _, ok := e.folded[op.Result.ID]; ok {
			return nil // every reader takes it as an immediate
		}
		if e.remat[op.Result.ID] != nil {
			return nil // every reader writes it into its own scratch register
		}
		dst := e.coalesceDst(op.Result, -1)
		e.push(Inst{Op: MovImm, Dst: dst, Imm: op.Imm, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result)})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpAdd, ssa.OpSub, ssa.OpMul, ssa.OpAnd, ssa.OpOr, ssa.OpXor,
		ssa.OpShl, ssa.OpShr, ssa.OpShrU, ssa.OpRotr,
		ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
		switch op.Kind {
		case ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
			if n, ok := e.consts[op.Args[1].ID]; ok {
				if sh, ok := powerOfTwoDivisor(n, op.Width); ok {
					return e.emitPow2DivRem(op, sh)
				}
				if magicDivisor(n, op.Kind, op.Width) {
					return e.emitMagicDivRem(op, n)
				}
			}
		}
		if imm, ok := e.immediateRight(op); ok {
			ra, err := e.materialize(op.Args[0], e.s0)
			if err != nil {
				return err
			}
			dst := e.coalesceDst(op.Result, -1)
			e.movReg(dst, ra)
			e.push(Inst{Op: BinOp, Dst: dst, Imm: imm, SrcImm: true, K: op.Kind, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result)})
			if dst == e.s2 {
				e.place(op.Result, e.s2)
			}
			return nil
		}
		ra, rb, err := e.binOperands(op)
		if err != nil {
			return err
		}
		// The direct-form ops (op dst, src) can land straight in the result's
		// register home; div/rem/shift stage through fixed registers (rax/rdx,
		// cl) inside asmInst, so they stay on the scratch s2.
		dst := e.s2
		kind := op.Kind
		switch op.Kind {
		case ssa.OpAdd, ssa.OpSub, ssa.OpMul, ssa.OpAnd, ssa.OpOr, ssa.OpXor:
			kind, ra, rb = e.swapForDst(op, ra, rb)
			dst = e.coalesceDst(op.Result, rb)
		}
		e.movReg(dst, ra)
		e.push(Inst{Op: BinOp, Dst: dst, Src: rb, K: kind, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result)})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpEq, ssa.OpNe, ssa.OpLt, ssa.OpLtU, ssa.OpLe, ssa.OpLeU,
		ssa.OpGt, ssa.OpGtU, ssa.OpGe, ssa.OpGeU:
		if imm, ok := e.immediateRight(op); ok {
			ra, err := e.materialize(op.Args[0], e.s0)
			if err != nil {
				return err
			}
			dst := e.coalesceDst(op.Result, -1)
			e.movReg(dst, ra)
			e.push(Inst{Op: SetCmp, Dst: dst, Imm: imm, SrcImm: true, K: op.Kind})
			if dst == e.s2 {
				e.place(op.Result, e.s2)
			}
			return nil
		}
		ra, rb, err := e.binOperands(op)
		if err != nil {
			return err
		}
		kind, ra, rb := e.swapForDst(op, ra, rb)
		dst := e.coalesceDst(op.Result, rb)
		e.movReg(dst, ra)
		e.push(Inst{Op: SetCmp, Dst: dst, Src: rb, K: kind})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpNeg:
		ra, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		dst := e.coalesceDst(op.Result, -1)
		e.movReg(dst, ra)
		e.push(Inst{Op: UnNeg, Dst: dst, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result)})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpNot, ssa.OpTrunc, ssa.OpExtendS, ssa.OpExtendU, ssa.OpExtend8S, ssa.OpExtend16S,
		ssa.OpClz, ssa.OpCtz, ssa.OpPopcount:
		ra, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		dst := e.coalesceDst(op.Result, -1)
		e.movReg(dst, ra)
		e.push(Inst{Op: UnOp, Dst: dst, K: op.Kind, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result)})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpConstFloat:
		e.push(Inst{Op: FConst, Dst: e.s2, F64: op.F64, W: op.Width})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpFAdd, ssa.OpFSub, ssa.OpFMul, ssa.OpFDiv:
		ra, rb, err := e.binOperands(op)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MovReg, Dst: e.s2, Src: ra})
		e.push(Inst{Op: FBin, Dst: e.s2, Src: rb, K: op.Kind, W: op.Width})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpFEq, ssa.OpFNe, ssa.OpFLt, ssa.OpFLe, ssa.OpFGt, ssa.OpFGe:
		ra, rb, err := e.binOperands(op)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MovReg, Dst: e.s2, Src: ra})
		e.push(Inst{Op: FCmp, Dst: e.s2, Src: rb, K: op.Kind})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpFNeg, ssa.OpFPromote, ssa.OpFDemote,
		ssa.OpIToFS, ssa.OpIToFU, ssa.OpFToIS, ssa.OpFToIU,
		ssa.OpReinterpretF32ToI32, ssa.OpReinterpretI32ToF32,
		ssa.OpReinterpretF64ToI64, ssa.OpReinterpretI64ToF64:
		ra, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MovReg, Dst: e.s2, Src: ra})
		e.push(Inst{Op: FConv, Dst: e.s2, K: op.Kind, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result)})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpSelect:
		// Ternary select. cond → s0, then → s1, else → s3 (s3 is free during op
		// emission — it only stages slot↔slot moves at edge boundaries), result
		// accumulated in s2. The three scratch regs are distinct, so spilled
		// operands can't clobber one another.
		if len(op.Args) != 3 {
			return fmt.Errorf("x86_64ssa: OpSelect expects 3 args, got %d", len(op.Args))
		}
		rc, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		rt, err := e.materialize(op.Args[1], e.s1)
		if err != nil {
			return err
		}
		re, err := e.materialize(op.Args[2], e.s3)
		if err != nil {
			return err
		}
		e.push(Inst{Op: Select, Dst: e.s2, Src: rc, Src2: rt, Src3: re, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result)})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpLoadF:
		base, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		// A load is a single instruction that reads its base and writes its
		// destination, so it can land straight in the result's register home —
		// no `avoid`, and no separate placing move.
		dst := e.coalesceDst(op.Result, -1)
		e.push(Inst{Op: MemLoad, Dst: dst, Src: base, Imm: op.Imm, W: 64, Bytes: 8})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpStoreF:
		base, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		val, err := e.materialize(op.Args[1], e.s1)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MemStore, Src: base, Src2: val, Imm: op.Imm, Bytes: 8})
		return nil

	case ssa.OpCall:
		// Direct integer call. The argument homes are captured at the call
		// point; the model interpreter reads them, recurses into the callee
		// Program, and delivers the result into s2.
		argLocs := make([]Loc, 0, len(op.Args))
		for _, a := range op.Args {
			l, ok := e.loc(a.ID)
			if !ok {
				return fmt.Errorf("x86_64ssa: call arg v%d has no allocation", a.ID)
			}
			argLocs = append(argLocs, l)
		}
		saveRegs, saveSet := e.callSaveRegs(op)
		dst := e.callResultDst(op.Result, e.s2)
		call := Inst{Op: Call, Dst: dst, Callee: op.Str, ArgLocs: argLocs, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result), SaveRegs: saveRegs, SaveRegsSet: saveSet}
		// A box released at a constant size names its freelist class at
		// compile time; the renderer that can push it inline reads Imm.
		if op.Str == "__fern_box_free" && len(op.Args) == 2 {
			if n, ok := e.consts[op.Args[1].ID]; ok {
				call.Imm, call.SrcImm = n, true
			}
		}
		e.push(call)
		e.place(op.Result, dst)
		return nil

	case ssa.OpCallPair:
		// Two-result direct call: tag delivered into s2, payload into s3. The two
		// results are placed independently; destinations are allocatable regs or
		// slots, never s2/s3, so the second place can't clobber the first source.
		argLocs := make([]Loc, 0, len(op.Args))
		for _, a := range op.Args {
			l, ok := e.loc(a.ID)
			if !ok {
				return fmt.Errorf("x86_64ssa: callpair arg v%d has no allocation", a.ID)
			}
			argLocs = append(argLocs, l)
		}
		saveRegs, saveSet := e.callSaveRegs(op)
		dst := e.callResultDst(op.Result, e.s2)
		dst2 := e.callResultDst(op.Result2, e.s3)
		e.push(Inst{Op: CallPair, Dst: dst, Dst2: dst2, Callee: op.Str, ArgLocs: argLocs, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result), SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, dst)
		e.place(op.Result2, dst2)
		return nil

	case ssa.OpCallIndirect:
		// Args[0] is the function-index value; Args[1..] are the call args. The
		// index and arg homes are captured here; the model reads the index,
		// resolves table[idx] → callee Program, recurses, and delivers into s2.
		if len(op.Args) < 1 {
			return fmt.Errorf("x86_64ssa: OpCallIndirect needs a callee operand")
		}
		if op.Result2.IsValid() {
			// A two-word return through a function value. No target this
			// model serves uses the two-word string ABI, so it has no pair
			// delivery for an indirect call.
			return fmt.Errorf("x86_64ssa: OpCallIndirect returning two words is not supported")
		}
		idxLoc, ok := e.loc(op.Args[0].ID)
		if !ok {
			return fmt.Errorf("x86_64ssa: callindirect index v%d has no allocation", op.Args[0].ID)
		}
		argLocs := make([]Loc, 0, len(op.Args)-1)
		for _, a := range op.Args[1:] {
			l, ok := e.loc(a.ID)
			if !ok {
				return fmt.Errorf("x86_64ssa: callindirect arg v%d has no allocation", a.ID)
			}
			argLocs = append(argLocs, l)
		}
		saveRegs, saveSet := e.callSaveRegs(op)
		dst := e.callResultDst(op.Result, e.s2)
		e.push(Inst{Op: CallIndirect, Dst: dst, IdxLoc: idxLoc, ArgLocs: argLocs, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result), SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, dst)
		return nil

	case ssa.OpMakeEnv, ssa.OpMakeClosure:
		// Closure construction. The capture homes are captured here; the model
		// allocates the env block (and, for OpMakeClosure, the closure
		// cell), stores the captures, and delivers the pointer into s2. fn_idx is
		// resolved from the callee name (Str) against the run-time table.
		argLocs := make([]Loc, 0, len(op.Args))
		for _, a := range op.Args {
			l, ok := e.loc(a.ID)
			if !ok {
				return fmt.Errorf("x86_64ssa: %v capture v%d has no allocation", op.Kind, a.ID)
			}
			argLocs = append(argLocs, l)
		}
		mop := MakeEnv
		if op.Kind == ssa.OpMakeClosure {
			mop = MakeClosure
		}
		e.push(Inst{Op: mop, Dst: e.s2, Callee: op.Str, ArgLocs: argLocs, CaptureSlots: op.CaptureSlots})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpConstVtable:
		// A static .rodata address — same shape as ConstStr (Dst := &vtable).
		e.push(Inst{Op: ConstVtable, Dst: e.s2, Str: op.Str})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpBoxDyn:
		// Allocate a {data, vtable} cell — a call to the allocator, so its
		// operand homes plus the caller-save set are captured like a call.
		argLocs := make([]Loc, 0, len(op.Args))
		for _, a := range op.Args {
			l, ok := e.loc(a.ID)
			if !ok {
				return fmt.Errorf("x86_64ssa: OpBoxDyn operand v%d has no allocation", a.ID)
			}
			argLocs = append(argLocs, l)
		}
		saveRegs, saveSet := e.callSaveRegs(op)
		dst := e.callResultDst(op.Result, e.s2)
		e.push(Inst{Op: BoxDyn, Dst: dst, ArgLocs: argLocs, SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, dst)
		return nil

	case ssa.OpCallDyn:
		// Vtable-slot indirect dispatch. ArgLocs = [data, method-args..., vtable];
		// Imm = the method slot. Like a call (caller-save set captured).
		if len(op.Args) < 1 {
			return fmt.Errorf("x86_64ssa: OpCallDyn needs at least the vtable operand")
		}
		argLocs := make([]Loc, 0, len(op.Args))
		for _, a := range op.Args {
			l, ok := e.loc(a.ID)
			if !ok {
				return fmt.Errorf("x86_64ssa: OpCallDyn operand v%d has no allocation", a.ID)
			}
			argLocs = append(argLocs, l)
		}
		saveRegs, saveSet := e.callSaveRegs(op)
		dst := e.callResultDst(op.Result, e.s2)
		e.push(Inst{Op: CallDyn, Dst: dst, ArgLocs: argLocs, Imm: op.Imm, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result), SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, dst)
		return nil

	case ssa.OpAlloc:
		size, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		alloc := Inst{Op: MemAlloc, Dst: e.s2, Src: size}
		// A constant size names its freelist class at compile time; Src still
		// carries it for the renderers and the interpreter that allocate
		// through the runtime.
		if n, ok := e.consts[op.Args[0].ID]; ok {
			alloc.Imm, alloc.SrcImm = n, true
		}
		e.push(alloc)
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpLoad, ssa.OpLoad8U, ssa.OpLoad8S, ssa.OpLoad16U, ssa.OpLoad16S, ssa.OpLoad32U:
		base, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		bytes, signed := memInfo(op.Kind)
		dst := e.coalesceDst(op.Result, -1)
		e.push(Inst{Op: MemLoad, Dst: dst, Src: base, Imm: op.Imm, W: op.Width, Narrow: e.narrow.HighBitsDead(op.Result), Bytes: bytes, Signed: signed})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpStore, ssa.OpStore8, ssa.OpStore16, ssa.OpStore32:
		base, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		val, err := e.materialize(op.Args[1], e.s1)
		if err != nil {
			return err
		}
		bytes, _ := memInfo(op.Kind)
		e.push(Inst{Op: MemStore, Src: base, Src2: val, Imm: op.Imm, Bytes: bytes})
		return nil

	case ssa.OpConstString:
		if e.remat[op.Result.ID] != nil {
			return nil
		}
		e.push(Inst{Op: ConstStr, Dst: e.s2, Str: op.Str})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpEnumSentinel:
		e.push(Inst{Op: EnumSentinel, Dst: e.s2, Imm: op.Imm})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpConstStringLen:
		if len(op.Args) != 1 {
			return fmt.Errorf("x86_64ssa: OpConstStringLen needs 1 arg")
		}
		n, ok := e.strLen[op.Args[0].ID]
		if !ok {
			return fmt.Errorf("x86_64ssa: OpConstStringLen arg is not an OpConstString result")
		}
		e.push(Inst{Op: MovImm, Dst: e.s2, Imm: int64(n), W: op.Width})
		e.place(op.Result, e.s2)
		return nil

	default:
		return fmt.Errorf("x86_64ssa: unsupported op %v in this slice", op.Kind)
	}
}

// binOperands materialises a binary op's two operands into distinct registers
// (s0 and s1 for spilled operands, kept separate so loading the second doesn't
// clobber the first).
func (e *emitter) binOperands(op *ssa.Op) (int, int, error) {
	if len(op.Args) != 2 {
		return 0, 0, fmt.Errorf("x86_64ssa: %v expects 2 args, got %d", op.Kind, len(op.Args))
	}
	ra, err := e.materialize(op.Args[0], e.s0)
	if err != nil {
		return 0, 0, err
	}
	rb, err := e.materialize(op.Args[1], e.s1)
	if err != nil {
		return 0, 0, err
	}
	return ra, rb, nil
}

func (e *emitter) paramLocs() []Loc {
	var out []Loc
	for _, p := range e.f.Params {
		if !p.IsValid() {
			continue
		}
		if l, ok := e.loc(p.ID); ok {
			out = append(out, l)
		} else {
			out = append(out, Loc{IsReg: false, Slot: -1}) // dead param
		}
	}
	return out
}

// memInfo returns the byte width and signedness of a load/store op kind.
func memInfo(k ssa.OpKind) (bytes int8, signed bool) {
	switch k {
	case ssa.OpLoad8U:
		return 1, false
	case ssa.OpLoad8S:
		return 1, true
	case ssa.OpLoad16U:
		return 2, false
	case ssa.OpLoad16S:
		return 2, true
	case ssa.OpStore8:
		return 1, false
	case ssa.OpStore16:
		return 2, false
	case ssa.OpLoad32U:
		return 4, false
	case ssa.OpStore32:
		return 4, false
	default: // OpLoad / OpStore (full 8-byte word)
		return 8, false
	}
}

// maxPhiCount is the largest number of phi ops in any block — the number of
// phi-move temp slots needed (reused across edges).
func maxPhiCount(f *ssa.Func) int {
	max := 0
	for _, b := range f.Blocks {
		n := 0
		for _, op := range b.Ops {
			if op.Kind == ssa.OpPhi {
				n++
			}
		}
		if n > max {
			max = n
		}
	}
	return max
}

// emitPow2DivRem lowers a division or remainder by 2^sh to shifts. Unsigned
// is exact: a logical shift, or a mask of the low bits. Signed rounds toward
// zero where an arithmetic shift rounds toward negative infinity, so the
// dividend is biased by 2^sh-1 when it is negative — the sar/shr pair
// computes that without a branch — before the shift; the remainder is the
// dividend less the quotient shifted back up. Mirrors the flat backends'
// emitConstDivRem.
func (e *emitter) emitPow2DivRem(op *ssa.Op, sh int64) error {
	ra, err := e.materialize(op.Args[0], e.s0)
	if err != nil {
		return err
	}
	w := op.Width
	bits := int64(32)
	if w == 64 {
		bits = 64
	}
	rem := op.Kind == ssa.OpRem || op.Kind == ssa.OpRemU
	shift := func(dst int, k ssa.OpKind, n int64) {
		e.push(Inst{Op: BinOp, Dst: dst, K: k, Imm: n, SrcImm: true, W: w})
	}
	dst := e.coalesceDst(op.Result, -1)
	if op.Kind == ssa.OpDivU || op.Kind == ssa.OpRemU {
		e.movReg(dst, ra)
		switch {
		case !rem:
			shift(dst, ssa.OpShrU, sh)
		case sh <= 31:
			e.push(Inst{Op: BinOp, Dst: dst, K: ssa.OpAnd, Imm: int64(1)<<sh - 1, SrcImm: true, W: w})
		default:
			// The mask is not an imm32: clear the high bits with a shift pair.
			shift(dst, ssa.OpShl, bits-sh)
			shift(dst, ssa.OpShrU, bits-sh)
		}
	} else {
		bias := e.s1
		e.movReg(bias, ra)
		shift(bias, ssa.OpShr, bits-1)   // all ones iff negative
		shift(bias, ssa.OpShrU, bits-sh) // 2^sh-1 iff negative
		e.push(Inst{Op: BinOp, Dst: bias, Src: ra, K: ssa.OpAdd, W: w})
		if rem {
			shift(bias, ssa.OpShr, sh) // the quotient
			shift(bias, ssa.OpShl, sh) // times the divisor
			e.movReg(dst, ra)
			e.push(Inst{Op: BinOp, Dst: dst, Src: bias, K: ssa.OpSub, W: w})
		} else {
			e.movReg(dst, bias)
			shift(dst, ssa.OpShr, sh)
		}
	}
	if dst == e.s2 {
		e.place(op.Result, e.s2)
	}
	return nil
}

// emitMagicDivRem lowers an i32 division or remainder by a constant that is
// neither 0, ±1 nor a power of two to a multiply by the reciprocal from
// ir.DeriveMagic*32 — the same lowering the flat x86-64 backend takes, in
// the abstract ops both renderers already have: the dividend is widened to
// 64 bits, multiplied by the magic, and the product's high half shifted down
// (a divide is 20-40 cycles where the multiply and shifts are a few). The
// fixups are the reciprocal's: signed adds or subtracts the dividend back
// when the magic wrapped and rounds a negative quotient toward zero; a 33-bit
// unsigned magic averages the dividend and the high half by shifting so the
// carry is kept. The remainder is the dividend less the quotient times the
// divisor. Scratch: s1 holds the widened dividend and then the quotient, s2
// the magic and then the divisor; the dividend's register is read until the
// last instruction that needs it and written, if it is dst, after.
func (e *emitter) emitMagicDivRem(op *ssa.Op, n int64) error {
	ra, err := e.materialize(op.Args[0], e.s0)
	if err != nil {
		return err
	}
	h, m := e.s1, e.s2
	dst := e.coalesceDst(op.Result, -1)
	rem := op.Kind == ssa.OpRem || op.Kind == ssa.OpRemU
	unsigned := op.Kind == ssa.OpDivU || op.Kind == ssa.OpRemU
	bin := func(d int, k ssa.OpKind, src int, w int8) {
		e.push(Inst{Op: BinOp, Dst: d, Src: src, K: k, W: w})
	}
	shift := func(d int, k ssa.OpKind, count int64, w int8) {
		e.push(Inst{Op: BinOp, Dst: d, K: k, Imm: count, SrcImm: true, W: w})
	}
	var divisor int64
	if unsigned {
		mg := ir.DeriveMagicU32(uint32(n))
		divisor = int64(uint32(n))
		e.movReg(h, ra)
		e.push(Inst{Op: UnOp, Dst: h, K: ssa.OpExtendU, W: 64})
		e.push(Inst{Op: MovImm, Dst: m, Imm: int64(mg.M), W: 64})
		bin(h, ssa.OpMul, m, 64)
		shift(h, ssa.OpShrU, 32, 64) // the high half of the 64-bit product
		if !mg.Add {
			shift(h, ssa.OpShrU, int64(mg.S), 32)
		} else {
			// A 33-bit magic: (x - h) / 2 + h is (x + h) / 2 with the carry
			// a plain add of two u32 would lose.
			e.movReg(m, ra)
			bin(m, ssa.OpSub, h, 32)
			shift(m, ssa.OpShrU, 1, 32)
			bin(m, ssa.OpAdd, h, 32)
			shift(m, ssa.OpShrU, int64(mg.S)-1, 32)
			e.movReg(h, m)
		}
	} else {
		mg := ir.DeriveMagicS32(int32(n))
		divisor = int64(int32(n))
		e.movReg(h, ra)
		e.push(Inst{Op: UnOp, Dst: h, K: ssa.OpExtendS, W: 64})
		e.push(Inst{Op: MovImm, Dst: m, Imm: int64(mg.M), W: 64})
		bin(h, ssa.OpMul, m, 64)
		shift(h, ssa.OpShr, 32, 64) // the signed high half
		switch {
		case mg.Add:
			bin(h, ssa.OpAdd, ra, 32)
		case mg.Sub:
			bin(h, ssa.OpSub, ra, 32)
		}
		if mg.S != 0 {
			shift(h, ssa.OpShr, int64(mg.S), 32)
		}
		// The shift floors, so a negative quotient is one too low.
		e.movReg(m, h)
		shift(m, ssa.OpShrU, 31, 32)
		bin(h, ssa.OpAdd, m, 32)
	}
	if rem {
		// dst may be s2 (m), so the product goes in h, which dst never is.
		e.push(Inst{Op: MovImm, Dst: m, Imm: divisor, W: 32})
		bin(h, ssa.OpMul, m, 32)
		e.movReg(dst, ra)
		bin(dst, ssa.OpSub, h, 32)
	} else {
		e.movReg(dst, h)
	}
	if dst == e.s2 {
		e.place(op.Result, e.s2)
	}
	return nil
}

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
	"sort"

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
		numAlloc: numAlloc,
		s0:       numAlloc, s1: numAlloc + 1, s2: numAlloc + 2, s3: numAlloc + 3,
		idx:        map[*ssa.Block]int{},
		phiTempCap: maxPhiCount(f),
		strLen:     map[int32]int{},
		// A register is caller-saved under this target iff it is not in the
		// callee-saved partition. nil mask => whole file caller-saved (arm64).
		callerSaved: func(r int) bool { return calleeSaved == nil || !calleeSaved[r] },
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind == ssa.OpConstString && op.Result.IsValid() {
				e.strLen[op.Result.ID] = len(op.Str)
			}
		}
	}
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
	f              *ssa.Func
	alloc          *ssa.Allocation
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
	p, ok := e.alloc.OpPos[op]
	if !ok {
		return nil, false
	}
	seen := map[int]bool{}
	var regs []int
	for id := range e.alloc.LiveAcross(p) {
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
	e.cur = nil
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
		tTarget := e.edgeTarget(b, b.Term.True)
		fTarget := e.edgeTarget(b, b.Term.False)
		e.blocks[bi].Term = Term{Kind: TBrIf, CondReg: cond, True: tTarget, False: fTarget}

	default:
		return fmt.Errorf("x86_64ssa: unsupported terminator %v", b.Term.Kind)
	}
	return nil
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

// emitParallelMoves realises a set of simultaneous copies. To handle arbitrary
// cycles/swaps simply and correctly, it reads every source into a temp slot
// first, then writes every destination from its temp — so all reads precede all
// writes. The temp slots are reused across edges. (A later slice can replace
// this with a minimal-move sequentialisation for fewer instructions.)
func (e *emitter) emitParallelMoves(moves []move) {
	for i, m := range moves {
		e.moveLoc(Loc{Slot: e.phiTempBase + i}, m.src)
	}
	for i, m := range moves {
		e.moveLoc(m.dst, Loc{Slot: e.phiTempBase + i})
	}
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

func (e *emitter) emitOp(op *ssa.Op) error {
	switch op.Kind {
	case ssa.OpConstInt, ssa.OpConstBool:
		dst := e.coalesceDst(op.Result, -1)
		e.push(Inst{Op: MovImm, Dst: dst, Imm: op.Imm, W: op.Width})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpAdd, ssa.OpSub, ssa.OpMul, ssa.OpAnd, ssa.OpOr, ssa.OpXor,
		ssa.OpShl, ssa.OpShr, ssa.OpShrU,
		ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
		ra, rb, err := e.binOperands(op)
		if err != nil {
			return err
		}
		// The direct-form ops (op dst, src) can land straight in the result's
		// register home; div/rem/shift stage through fixed registers (rax/rdx,
		// cl) inside asmInst, so they stay on the scratch s2.
		dst := e.s2
		switch op.Kind {
		case ssa.OpAdd, ssa.OpSub, ssa.OpMul, ssa.OpAnd, ssa.OpOr, ssa.OpXor:
			dst = e.coalesceDst(op.Result, rb)
		}
		e.movReg(dst, ra)
		e.push(Inst{Op: BinOp, Dst: dst, Src: rb, K: op.Kind, W: op.Width})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpEq, ssa.OpNe, ssa.OpLt, ssa.OpLtU, ssa.OpLe, ssa.OpLeU,
		ssa.OpGt, ssa.OpGtU, ssa.OpGe, ssa.OpGeU:
		ra, rb, err := e.binOperands(op)
		if err != nil {
			return err
		}
		dst := e.coalesceDst(op.Result, rb)
		e.movReg(dst, ra)
		e.push(Inst{Op: SetCmp, Dst: dst, Src: rb, K: op.Kind})
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
		e.push(Inst{Op: UnNeg, Dst: dst, W: op.Width})
		if dst == e.s2 {
			e.place(op.Result, e.s2)
		}
		return nil

	case ssa.OpNot, ssa.OpTrunc, ssa.OpExtendS, ssa.OpExtendU, ssa.OpExtend8S, ssa.OpExtend16S:
		ra, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		dst := e.coalesceDst(op.Result, -1)
		e.movReg(dst, ra)
		e.push(Inst{Op: UnOp, Dst: dst, K: op.Kind, W: op.Width})
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
		e.push(Inst{Op: FConv, Dst: e.s2, K: op.Kind, W: op.Width})
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
		e.push(Inst{Op: Select, Dst: e.s2, Src: rc, Src2: rt, Src3: re, W: op.Width})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpLoadF:
		base, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MemLoad, Dst: e.s2, Src: base, Imm: op.Imm, W: 64, Bytes: 8})
		e.place(op.Result, e.s2)
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
		e.push(Inst{Op: Call, Dst: e.s2, Callee: op.Str, ArgLocs: argLocs, W: op.Width, SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, e.s2)
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
		e.push(Inst{Op: CallPair, Dst: e.s2, Dst2: e.s3, Callee: op.Str, ArgLocs: argLocs, W: op.Width, SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, e.s2)
		e.place(op.Result2, e.s3)
		return nil

	case ssa.OpCallIndirect:
		// Args[0] is the function-index value; Args[1..] are the call args. The
		// index and arg homes are captured here; the model reads the index,
		// resolves table[idx] → callee Program, recurses, and delivers into s2.
		if len(op.Args) < 1 {
			return fmt.Errorf("x86_64ssa: OpCallIndirect needs a callee operand")
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
		e.push(Inst{Op: CallIndirect, Dst: e.s2, IdxLoc: idxLoc, ArgLocs: argLocs, W: op.Width, SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, e.s2)
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
		e.push(Inst{Op: BoxDyn, Dst: e.s2, ArgLocs: argLocs, SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, e.s2)
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
		e.push(Inst{Op: CallDyn, Dst: e.s2, ArgLocs: argLocs, Imm: op.Imm, W: op.Width, SaveRegs: saveRegs, SaveRegsSet: saveSet})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpAlloc:
		size, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MemAlloc, Dst: e.s2, Src: size})
		e.place(op.Result, e.s2)
		return nil

	case ssa.OpLoad, ssa.OpLoad8U, ssa.OpLoad8S, ssa.OpLoad16U, ssa.OpLoad16S, ssa.OpLoad32U:
		base, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		bytes, signed := memInfo(op.Kind)
		e.push(Inst{Op: MemLoad, Dst: e.s2, Src: base, Imm: op.Imm, W: op.Width, Bytes: bytes, Signed: signed})
		e.place(op.Result, e.s2)
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

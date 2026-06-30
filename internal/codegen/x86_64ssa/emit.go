// Package x86_64ssa is the SSA→x86-64 register-based emit path (phase 2 of the
// SSA-level register allocator, #4112). It consumes allocated SSA — produced by
// ssa.LinearScan — instead of walking the IR as a stack machine, so values live
// in registers and only spill when the register file is exhausted.
//
// This first slice covers the straight-line integer subset: a single block of
// integer arithmetic / comparison / const ops ending in Ret. It emits an
// abstract register-machine program (Inst) rather than final GAS text, and is
// validated differentially against ssa.Eval via the model interpreter in this
// package (Run). That proves the regalloc-specific logic — operand assignment,
// the x86 two-address fixup (`add dst,src` is `dst = dst + src`), and spill
// load/store — independently of final-assembly concerns. Real GAS-text emission
// (with the call ABI, idiv's rax/rdx pinning, etc.) and control flow / phi
// resolution are later slices; unsupported shapes return a clear error.
package x86_64ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// numScratch reserved registers sit above the allocatable file and break the
// operand-aliasing cases the two-address form would otherwise hit: s0/s1 hold
// materialised (possibly reloaded-from-slot) operands, s2 accumulates the
// result before it is placed into the value's assigned location.
const numScratch = 3

// Opcode is the abstract register-machine operation.
type Opcode int

const (
	MovImm    Opcode = iota // reg[Dst] = Imm
	MovReg                  // reg[Dst] = reg[Src]
	BinOp                   // reg[Dst] = reg[Dst] (K) reg[Src]   (K an integer arith op)
	UnNeg                   // reg[Dst] = -reg[Dst]
	SetCmp                  // reg[Dst] = (reg[Dst] K reg[Src]) ? 1 : 0   (K a comparison)
	LoadSlot                // reg[Dst] = slot[Imm]
	StoreSlot               // slot[Imm] = reg[Src]
	Ret                     // result = reg[Src]
)

// Inst is one abstract instruction. Registers are indices into a flat file of
// size Program.NumRegFile (allocatable registers, then the scratch registers).
type Inst struct {
	Op  Opcode
	Dst int
	Src int
	Imm int64
	K   ssa.OpKind // BinOp / SetCmp operation
	W   int8       // result width for MovImm/BinOp/UnNeg (0/32 => i32, 64 => i64)
}

// Loc is a value's home: a register (IsReg) or a spill slot. A dead param has
// IsReg=false and Slot=-1.
type Loc struct {
	IsReg bool
	Reg   int
	Slot  int
}

// Program is the emitted abstract function: the instruction stream plus the
// frame shape and the parameter homes the interpreter seeds args into.
type Program struct {
	Insts      []Inst
	NumRegFile int // allocatable + scratch
	NumSlots   int
	ParamLocs  []Loc
}

// Emit lowers a straight-line integer SSA function to an abstract register
// program, allocating over numAlloc physical registers (the rest spill).
func Emit(f *ssa.Func, numAlloc int) (*Program, error) {
	if numAlloc < 1 {
		return nil, fmt.Errorf("x86_64ssa: numAlloc must be >= 1")
	}
	if len(f.Blocks) != 1 || f.Entry == nil {
		return nil, fmt.Errorf("x86_64ssa: only single-block functions are supported in this slice (got %d blocks)", len(f.Blocks))
	}
	b := f.Entry
	if b.Term.Kind != ssa.TermRet {
		return nil, fmt.Errorf("x86_64ssa: only Ret-terminated blocks are supported in this slice")
	}

	alloc := ssa.LinearScan(f, ssa.Target{NumRegs: numAlloc})
	e := &emitter{
		alloc:    alloc,
		numAlloc: numAlloc,
		s0:       numAlloc, s1: numAlloc + 1, s2: numAlloc + 2,
	}

	for _, op := range b.Ops {
		if err := e.emitOp(op); err != nil {
			return nil, err
		}
	}
	// Ret.
	if b.Term.Value.IsValid() {
		rr, err := e.materialize(b.Term.Value, e.s0)
		if err != nil {
			return nil, err
		}
		e.push(Inst{Op: Ret, Src: rr})
	} else {
		// void return: model it as returning 0.
		e.push(Inst{Op: MovImm, Dst: e.s0, Imm: 0})
		e.push(Inst{Op: Ret, Src: e.s0})
	}

	return &Program{
		Insts:      e.insts,
		NumRegFile: numAlloc + numScratch,
		NumSlots:   alloc.NumSlots,
		ParamLocs:  e.paramLocs(f),
	}, nil
}

type emitter struct {
	alloc      *ssa.Allocation
	numAlloc   int
	s0, s1, s2 int
	insts      []Inst
}

func (e *emitter) push(i Inst) { e.insts = append(e.insts, i) }

// loc returns where a value lives.
func (e *emitter) loc(id int32) (Loc, bool) {
	if r, ok := e.alloc.Reg[id]; ok {
		return Loc{IsReg: true, Reg: r}, true
	}
	if s, ok := e.alloc.Slot[id]; ok {
		return Loc{IsReg: false, Slot: s}, true
	}
	return Loc{}, false
}

// materialize ensures value v is in a register and returns its index. A spilled
// value is loaded into the given scratch register.
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

// place stores the result currently in srcReg into value v's home.
func (e *emitter) place(v ssa.Value, srcReg int) error {
	l, ok := e.loc(v.ID)
	if !ok {
		// Result is dead (never used, not returned). Nothing to store.
		return nil
	}
	if l.IsReg {
		if l.Reg != srcReg {
			e.push(Inst{Op: MovReg, Dst: l.Reg, Src: srcReg})
		}
		return nil
	}
	e.push(Inst{Op: StoreSlot, Imm: int64(l.Slot), Src: srcReg})
	return nil
}

func (e *emitter) emitOp(op *ssa.Op) error {
	switch op.Kind {
	case ssa.OpConstInt, ssa.OpConstBool:
		e.push(Inst{Op: MovImm, Dst: e.s2, Imm: op.Imm, W: op.Width})
		return e.place(op.Result, e.s2)

	case ssa.OpAdd, ssa.OpSub, ssa.OpMul, ssa.OpAnd, ssa.OpOr, ssa.OpXor,
		ssa.OpShl, ssa.OpShr, ssa.OpShrU:
		ra, err := e.binOperands(op)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MovReg, Dst: e.s2, Src: ra})
		rb, err := e.materialize(op.Args[1], e.s1)
		if err != nil {
			return err
		}
		e.push(Inst{Op: BinOp, Dst: e.s2, Src: rb, K: op.Kind, W: op.Width})
		return e.place(op.Result, e.s2)

	case ssa.OpEq, ssa.OpNe, ssa.OpLt, ssa.OpLtU, ssa.OpLe, ssa.OpLeU,
		ssa.OpGt, ssa.OpGtU, ssa.OpGe, ssa.OpGeU:
		ra, err := e.binOperands(op)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MovReg, Dst: e.s2, Src: ra})
		rb, err := e.materialize(op.Args[1], e.s1)
		if err != nil {
			return err
		}
		e.push(Inst{Op: SetCmp, Dst: e.s2, Src: rb, K: op.Kind})
		return e.place(op.Result, e.s2)

	case ssa.OpNeg:
		ra, err := e.materialize(op.Args[0], e.s0)
		if err != nil {
			return err
		}
		e.push(Inst{Op: MovReg, Dst: e.s2, Src: ra})
		e.push(Inst{Op: UnNeg, Dst: e.s2, W: op.Width})
		return e.place(op.Result, e.s2)

	default:
		return fmt.Errorf("x86_64ssa: unsupported op %v in this slice", op.Kind)
	}
}

// binOperands materialises the first operand of a binary op into s0 and returns
// its register; the caller materialises the second into s1 (kept separate so a
// spilled-into-s0 first operand isn't clobbered by loading the second).
func (e *emitter) binOperands(op *ssa.Op) (int, error) {
	if len(op.Args) != 2 {
		return 0, fmt.Errorf("x86_64ssa: %v expects 2 args, got %d", op.Kind, len(op.Args))
	}
	return e.materialize(op.Args[0], e.s0)
}

func (e *emitter) paramLocs(f *ssa.Func) []Loc {
	var out []Loc
	for _, p := range f.Params {
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

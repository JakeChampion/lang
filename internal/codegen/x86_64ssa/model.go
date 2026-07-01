package x86_64ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
)

// Run executes an emitted abstract program over a model register file + spill
// slots and returns the value its Ret produces. It is the differential
// counterpart to ssa.Eval: for any supported function, Run(Emit(f), args) must
// equal ssa.Eval(f, args). The model's integer semantics deliberately mirror
// ssa.Eval (including i32 width masking) so a divergence pinpoints a bug in the
// emitter's operand wiring / two-address fixup / spill handling rather than a
// semantic mismatch.
func Run(p *Program, args []int64) (int64, error) {
	return runProg(nil, p, newModelHeap(), args)
}

// RunModule runs the named entry program, resolving Call instructions against
// the module so direct calls (and recursion) execute. A single heap is shared
// across the whole call tree (OpAlloc state persists), matching the runtime.
func RunModule(m map[string]*Program, entry string, args []int64) (int64, error) {
	p, ok := m[entry]
	if !ok {
		return 0, fmt.Errorf("RunModule: unknown entry %q", entry)
	}
	return runProg(m, p, newModelHeap(), args)
}

func runProg(m map[string]*Program, p *Program, h *modelHeap, args []int64) (int64, error) {
	if len(args) != len(p.ParamLocs) {
		return 0, fmt.Errorf("Run: got %d args, program has %d params", len(args), len(p.ParamLocs))
	}
	regs := make([]int64, p.NumRegFile)
	slots := make([]int64, p.NumSlots)
	readLoc := func(l Loc) int64 {
		if l.IsReg {
			return regs[l.Reg]
		}
		return slots[l.Slot]
	}

	for i, l := range p.ParamLocs {
		if !l.IsReg && l.Slot < 0 {
			continue // dead param
		}
		if l.IsReg {
			regs[l.Reg] = args[i]
		} else {
			slots[l.Slot] = args[i]
		}
	}

	bi := p.Entry
	const maxSteps = 1 << 22
	for steps := 0; ; steps++ {
		if steps > maxSteps {
			return 0, fmt.Errorf("Run: step limit exceeded (non-terminating?)")
		}
		if bi < 0 || bi >= len(p.Blocks) {
			return 0, fmt.Errorf("Run: branch to out-of-range block %d", bi)
		}
		blk := p.Blocks[bi]
		for _, in := range blk.Insts {
			switch in.Op {
			case MovImm:
				regs[in.Dst] = maskW(in.W, in.Imm)
			case MovReg:
				regs[in.Dst] = regs[in.Src]
			case BinOp:
				r, err := binInt(in.K, regs[in.Dst], regs[in.Src])
				if err != nil {
					return 0, err
				}
				regs[in.Dst] = maskW(in.W, r)
			case UnNeg:
				regs[in.Dst] = maskW(in.W, -regs[in.Dst])
			case UnOp:
				regs[in.Dst] = maskW(in.W, unInt(in.K, regs[in.Dst]))
			case SetCmp:
				regs[in.Dst] = cmpInt(in.K, regs[in.Dst], regs[in.Src])
			case LoadSlot:
				regs[in.Dst] = slots[in.Imm]
			case StoreSlot:
				slots[in.Imm] = regs[in.Src]
			case Call:
				if m == nil {
					return 0, fmt.Errorf("Run: Call %q requires a module (use RunModule)", in.Callee)
				}
				callee, ok := m[in.Callee]
				if !ok {
					return 0, fmt.Errorf("Run: unknown callee %q", in.Callee)
				}
				argvals := make([]int64, 0, len(in.ArgLocs))
				for _, l := range in.ArgLocs {
					argvals = append(argvals, readLoc(l))
				}
				r, err := runProg(m, callee, h, argvals)
				if err != nil {
					return 0, err
				}
				regs[in.Dst] = maskW(in.W, r)
			case MemAlloc:
				regs[in.Dst] = h.alloc(regs[in.Src])
			case MemLoad:
				v, err := h.load(regs[in.Src]+in.Imm, int(in.Bytes), in.Signed)
				if err != nil {
					return 0, err
				}
				regs[in.Dst] = maskW(in.W, v)
			case MemStore:
				if err := h.store(regs[in.Src]+in.Imm, regs[in.Src2], int(in.Bytes)); err != nil {
					return 0, err
				}
			case ConstStr:
				p := h.alloc(int64(len(in.Str)))
				for i := 0; i < len(in.Str); i++ {
					if err := h.store(p+int64(i), int64(in.Str[i]), 1); err != nil {
						return 0, err
					}
				}
				regs[in.Dst] = p
			default:
				return 0, fmt.Errorf("Run: unknown opcode %d", in.Op)
			}
		}
		switch blk.Term.Kind {
		case TRet:
			return regs[blk.Term.RetReg], nil
		case TJmp:
			bi = blk.Term.Target
		case TBrIf:
			if regs[blk.Term.CondReg] != 0 {
				bi = blk.Term.True
			} else {
				bi = blk.Term.False
			}
		default:
			return 0, fmt.Errorf("Run: unknown terminator %d", blk.Term.Kind)
		}
	}
}

// modelHeap mirrors ssa.Eval's memory model: a little-endian byte buffer with
// a bump allocator and a reserved null page, so Run and Eval agree on memory
// semantics for the differential check.
type modelHeap struct{ data []byte }

func newModelHeap() *modelHeap { return &modelHeap{data: make([]byte, 8)} }

func (h *modelHeap) alloc(size int64) int64 {
	base := int64(len(h.data))
	if r := base % 8; r != 0 {
		base += 8 - r
	}
	if size < 0 {
		size = 0
	}
	h.data = append(h.data, make([]byte, base-int64(len(h.data))+size)...)
	return base
}

func (h *modelHeap) check(addr int64, n int) error {
	if addr < 8 || addr+int64(n) > int64(len(h.data)) {
		return fmt.Errorf("Run: out-of-bounds memory access at %d (heap %d bytes)", addr, len(h.data))
	}
	return nil
}

func (h *modelHeap) load(addr int64, n int, signed bool) (int64, error) {
	if err := h.check(addr, n); err != nil {
		return 0, err
	}
	var v uint64
	for i := 0; i < n; i++ {
		v |= uint64(h.data[addr+int64(i)]) << (8 * i)
	}
	if signed && n < 8 {
		shift := uint(64 - 8*n)
		return int64(v<<shift) >> shift, nil
	}
	return int64(v), nil
}

func (h *modelHeap) store(addr, val int64, n int) error {
	if err := h.check(addr, n); err != nil {
		return err
	}
	u := uint64(val)
	for i := 0; i < n; i++ {
		h.data[addr+int64(i)] = byte(u >> (8 * i))
	}
	return nil
}

// unInt evaluates a unary integer transform, mirroring ssa.Eval.
func unInt(k ssa.OpKind, v int64) int64 {
	switch k {
	case ssa.OpNot:
		return b2i(v == 0)
	case ssa.OpTrunc, ssa.OpExtendS:
		return int64(int32(v))
	case ssa.OpExtendU:
		return int64(uint32(v))
	case ssa.OpExtend8S:
		return int64(int8(v))
	case ssa.OpExtend16S:
		return int64(int16(v))
	default:
		return v
	}
}

func maskW(w int8, v int64) int64 {
	if w == 64 {
		return v
	}
	return int64(int32(v))
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func binInt(k ssa.OpKind, a, b int64) (int64, error) {
	switch k {
	case ssa.OpAdd:
		return a + b, nil
	case ssa.OpSub:
		return a - b, nil
	case ssa.OpMul:
		return a * b, nil
	case ssa.OpAnd:
		return a & b, nil
	case ssa.OpOr:
		return a | b, nil
	case ssa.OpXor:
		return a ^ b, nil
	case ssa.OpShl:
		return a << uint64(b), nil
	case ssa.OpShr:
		return a >> uint64(b), nil
	case ssa.OpShrU:
		return int64(uint64(a) >> uint64(b)), nil
	default:
		return 0, fmt.Errorf("Run: not a supported binary op: %v", k)
	}
}

func cmpInt(k ssa.OpKind, a, b int64) int64 {
	switch k {
	case ssa.OpEq:
		return b2i(a == b)
	case ssa.OpNe:
		return b2i(a != b)
	case ssa.OpLt:
		return b2i(a < b)
	case ssa.OpLtU:
		return b2i(uint64(a) < uint64(b))
	case ssa.OpLe:
		return b2i(a <= b)
	case ssa.OpLeU:
		return b2i(uint64(a) <= uint64(b))
	case ssa.OpGt:
		return b2i(a > b)
	case ssa.OpGtU:
		return b2i(uint64(a) > uint64(b))
	case ssa.OpGe:
		return b2i(a >= b)
	case ssa.OpGeU:
		return b2i(uint64(a) >= uint64(b))
	default:
		return 0
	}
}

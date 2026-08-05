package x86_64ssa

import (
	"fmt"
	"math"

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
	v, _, err := runProg(nil, nil, p, newModelHeap(), args)
	return v, err
}

// RunModule runs the named entry program, resolving Call instructions against
// the module so direct calls (and recursion) execute. A single heap is shared
// across the whole call tree (OpAlloc state persists), matching the runtime.
func RunModule(m map[string]*Program, entry string, args []int64) (int64, error) {
	return RunModuleTable(m, nil, entry, args)
}

// RunModuleTable is RunModule plus an ordered function-index table so
// CallIndirect can resolve a function value (an integer index) to a callee:
// table[idx] names the callee, looked up in m. Mirrors ssa.EvalInTable.
func RunModuleTable(m map[string]*Program, table []string, entry string, args []int64) (int64, error) {
	p, ok := m[entry]
	if !ok {
		return 0, fmt.Errorf("RunModule: unknown entry %q", entry)
	}
	v, _, err := runProg(m, table, p, newModelHeap(), args)
	return v, err
}

func runProg(m map[string]*Program, table []string, p *Program, h *modelHeap, args []int64) (int64, int64, error) {
	if len(args) != len(p.ParamLocs) {
		return 0, 0, fmt.Errorf("Run: got %d args, program has %d params", len(args), len(p.ParamLocs))
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
			return 0, 0, fmt.Errorf("Run: step limit exceeded (non-terminating?)")
		}
		if bi < 0 || bi >= len(p.Blocks) {
			return 0, 0, fmt.Errorf("Run: branch to out-of-range block %d", bi)
		}
		blk := p.Blocks[bi]
		for _, in := range blk.Insts {
			switch in.Op {
			case MovImm:
				regs[in.Dst] = maskW(in.W, in.Imm)
			case MovReg:
				regs[in.Dst] = regs[in.Src]
			case BinOp:
				r, err := binInt(in.K, regs[in.Dst], regs[in.Src], in.W)
				if err != nil {
					return 0, 0, err
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
					return 0, 0, fmt.Errorf("Run: Call %q requires a module (use RunModule)", in.Callee)
				}
				callee, ok := m[in.Callee]
				if !ok {
					return 0, 0, fmt.Errorf("Run: unknown callee %q", in.Callee)
				}
				argvals := make([]int64, 0, len(in.ArgLocs))
				for _, l := range in.ArgLocs {
					argvals = append(argvals, readLoc(l))
				}
				r0, _, err := runProg(m, table, callee, h, argvals)
				if err != nil {
					return 0, 0, err
				}
				regs[in.Dst] = maskW(in.W, r0)
			case CallIndirect:
				if m == nil {
					return 0, 0, fmt.Errorf("Run: CallIndirect requires a module (use RunModuleTable)")
				}
				// IdxLoc holds a {fn, env} cell pointer: fn (table index) at +0,
				// env_ptr at +8. Deref and call fn with env appended last.
				ptr := readLoc(in.IdxLoc)
				idx, err := h.load(ptr, 8, false)
				if err != nil {
					return 0, 0, err
				}
				env, err := h.load(ptr+8, 8, false)
				if err != nil {
					return 0, 0, err
				}
				if idx < 1 || idx > int64(len(table)) {
					return 0, 0, fmt.Errorf("Run: CallIndirect fn index %d out of range (table has %d entries, index 0 is the null reference)", idx, len(table))
				}
				callee, ok := m[table[idx-1]]
				if !ok {
					return 0, 0, fmt.Errorf("Run: CallIndirect target %q (index %d) not in module", table[idx-1], idx)
				}
				argvals := make([]int64, 0, len(in.ArgLocs)+1)
				for _, l := range in.ArgLocs {
					argvals = append(argvals, readLoc(l))
				}
				argvals = append(argvals, env) // env is the last parameter
				r0, _, err := runProg(m, table, callee, h, argvals)
				if err != nil {
					return 0, 0, err
				}
				regs[in.Dst] = maskW(in.W, r0)
			case CallPair:
				if m == nil {
					return 0, 0, fmt.Errorf("Run: CallPair %q requires a module (use RunModule)", in.Callee)
				}
				callee, ok := m[in.Callee]
				if !ok {
					return 0, 0, fmt.Errorf("Run: unknown callee %q", in.Callee)
				}
				argvals := make([]int64, 0, len(in.ArgLocs))
				for _, l := range in.ArgLocs {
					argvals = append(argvals, readLoc(l))
				}
				r0, r1, err := runProg(m, table, callee, h, argvals)
				if err != nil {
					return 0, 0, err
				}
				regs[in.Dst] = maskW(in.W, r0)
				regs[in.Dst2] = r1
			case MakeEnv:
				env, err := storeCaptures(h, in.ArgLocs, readLoc)
				if err != nil {
					return 0, 0, err
				}
				regs[in.Dst] = env
			case MakeClosure:
				idx, ok := funcIndexOf(table, in.Callee)
				if !ok {
					return 0, 0, fmt.Errorf("Run: MakeClosure target %q not in table (use RunModuleTable)", in.Callee)
				}
				// The 32-byte {fn_idx, env_ptr, drop_idx, env_ptr} cell the real
				// asm builds. A zero-capture closure has no env block, so nothing
				// may dispatch its drop sub-pair: both slots stay null.
				var env, dropIdx int64
				if len(in.ArgLocs) > 0 {
					dropIdx, _ = funcIndexOf(table, "__closure_drop_"+in.Callee)
					e, err := storeCaptures(h, in.ArgLocs, readLoc)
					if err != nil {
						return 0, 0, err
					}
					env = e
				}
				cell := h.alloc(32)
				for i, v := range []int64{idx, env, dropIdx, env} {
					if err := h.store(cell+int64(i)*8, v, 8); err != nil {
						return 0, 0, err
					}
				}
				regs[in.Dst] = cell
			case MemAlloc:
				regs[in.Dst] = h.alloc(regs[in.Src])
			case MemLoad:
				v, err := h.load(regs[in.Src]+in.Imm, int(in.Bytes), in.Signed)
				if err != nil {
					return 0, 0, err
				}
				regs[in.Dst] = maskW(in.W, v)
			case MemStore:
				if err := h.store(regs[in.Src]+in.Imm, regs[in.Src2], int(in.Bytes)); err != nil {
					return 0, 0, err
				}
			case ConstStr:
				p := h.alloc(int64(len(in.Str)))
				for i := 0; i < len(in.Str); i++ {
					if err := h.store(p+int64(i), int64(in.Str[i]), 1); err != nil {
						return 0, 0, err
					}
				}
				regs[in.Dst] = p
			case EnumSentinel:
				regs[in.Dst] = h.sentinel(in.Imm)
			case FConst:
				regs[in.Dst] = fbits(in.F64, in.W)
			case FBin:
				regs[in.Dst] = fbin(in.K, regs[in.Dst], regs[in.Src], in.W)
			case FCmp:
				regs[in.Dst] = fcmp(in.K, regs[in.Dst], regs[in.Src])
			case FConv:
				regs[in.Dst] = fconv(in.K, regs[in.Dst], in.W)
			case Select:
				if regs[in.Src] != 0 {
					regs[in.Dst] = maskW(in.W, regs[in.Src2])
				} else {
					regs[in.Dst] = maskW(in.W, regs[in.Src3])
				}
			default:
				return 0, 0, fmt.Errorf("Run: unknown opcode %d", in.Op)
			}
		}
		switch blk.Term.Kind {
		case TRet:
			return regs[blk.Term.RetReg], 0, nil
		case TRetPair:
			return regs[blk.Term.RetReg], regs[blk.Term.RetReg2], nil
		case TJmp:
			bi = blk.Term.Target
		case TBrIf:
			if regs[blk.Term.CondReg] != 0 {
				bi = blk.Term.True
			} else {
				bi = blk.Term.False
			}
		default:
			return 0, 0, fmt.Errorf("Run: unknown terminator %d", blk.Term.Kind)
		}
	}
}

// funcIndexOf returns name's function-value index — its position in the ordered
// function table, biased by one so that 0 is the NULL function reference (the
// value a closure cell's drop slot carries when its target has no
// __closure_drop_ thunk). Returns false if absent.
func funcIndexOf(table []string, name string) (int64, bool) {
	for i, n := range table {
		if n == name {
			return int64(i) + 1, true
		}
	}
	return 0, false
}

// storeCaptures allocates an env block of len(argLocs) 8-byte slots, stores each
// capture (argLocs[i] at offset 8*i), and returns the env pointer. Shared by
// MakeEnv and MakeClosure; mirrors ssa.Eval's storeCaptures byte-for-byte so the
// differential check on the shared heap layout holds.
func storeCaptures(h *modelHeap, argLocs []Loc, readLoc func(Loc) int64) (int64, error) {
	env := h.alloc(int64(len(argLocs)) * 8)
	for i, l := range argLocs {
		if err := h.store(env+int64(i)*8, readLoc(l), 8); err != nil {
			return 0, err
		}
	}
	return env, nil
}

// modelHeap mirrors ssa.Eval's memory model: a little-endian byte buffer with
// a bump allocator and a reserved null page, so Run and Eval agree on memory
// semantics for the differential check.
type modelHeap struct {
	data []byte
	sent map[int64]int64 // OpEnumSentinel pointers memoised by tag
}

func newModelHeap() *modelHeap { return &modelHeap{data: make([]byte, 8)} }

func (h *modelHeap) sentinel(tag int64) int64 {
	if h.sent == nil {
		h.sent = map[int64]int64{}
	}
	if a, ok := h.sent[tag]; ok {
		return a
	}
	a := h.alloc(4)
	_ = h.store(a, tag, 4)
	h.sent[tag] = a
	return a
}

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

// Float helpers. Floats live in the int64 registers as their IEEE-754 f64 bit
// pattern, mirroring ssa.Eval, so Run and Eval agree bit-for-bit.
func ffrom(b int64) float64 { return math.Float64frombits(uint64(b)) }

func fbits(v float64, w int8) int64 {
	if w == 32 {
		v = float64(float32(v))
	}
	return int64(math.Float64bits(v))
}

func fbin(k ssa.OpKind, a, b int64, w int8) int64 {
	x, y := ffrom(a), ffrom(b)
	var r float64
	switch k {
	case ssa.OpFAdd:
		r = x + y
	case ssa.OpFSub:
		r = x - y
	case ssa.OpFMul:
		r = x * y
	case ssa.OpFDiv:
		r = x / y
	}
	return fbits(r, w)
}

func fcmp(k ssa.OpKind, a, b int64) int64 {
	x, y := ffrom(a), ffrom(b)
	switch k {
	case ssa.OpFEq:
		return b2i(x == y)
	case ssa.OpFNe:
		return b2i(x != y)
	case ssa.OpFLt:
		return b2i(x < y)
	case ssa.OpFLe:
		return b2i(x <= y)
	case ssa.OpFGt:
		return b2i(x > y)
	case ssa.OpFGe:
		return b2i(x >= y)
	default:
		return 0
	}
}

func fconv(k ssa.OpKind, a int64, w int8) int64 {
	switch k {
	case ssa.OpFNeg:
		return fbits(-ffrom(a), w)
	case ssa.OpFPromote:
		return int64(math.Float64bits(ffrom(a)))
	case ssa.OpFDemote:
		return int64(math.Float64bits(float64(float32(ffrom(a)))))
	case ssa.OpIToFS:
		return fbits(float64(a), w)
	case ssa.OpIToFU:
		return fbits(float64(uint64(a)), w)
	case ssa.OpFToIS:
		return int64(ffrom(a))
	case ssa.OpFToIU:
		return int64(uint64(ffrom(a)))
	case ssa.OpReinterpretF64ToI64, ssa.OpReinterpretI64ToF64:
		return a // identity: floats live as their f64 bit pattern already
	case ssa.OpReinterpretF32ToI32:
		return int64(int32(math.Float32bits(float32(ffrom(a))))) // f32 bits as i32
	case ssa.OpReinterpretI32ToF32:
		return fbits(float64(math.Float32frombits(uint32(a))), 32) // i32 bits as f32 (stored as f64 bits)
	default:
		return a
	}
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

// binInt mirrors ssa.evalBinaryInt, width included. w is the op width (64, or
// 32 for everything else). Two things depend on it, both invisible until an
// operand or a count runs past 32 bits:
//
//   - the unsigned ops must read a 32-bit operand as u32, since 32-bit values
//     sit sign-extended in the int64 slot (see maskW);
//   - a shift masks its count to the operand width, 5 bits at 32 and 6 at 64,
//     where Go would instead yield 0 for anything out of range.
//
// A model that skips either agrees with an emitter that makes the same
// mistake, which is precisely the divergence Run exists to catch.
func binInt(k ssa.OpKind, a, b int64, w int8) (int64, error) {
	if w != 64 {
		switch k {
		case ssa.OpDivU, ssa.OpRemU, ssa.OpShrU:
			a = int64(uint32(a))
			b = int64(uint32(b))
		}
	}
	shiftBy := func() uint64 {
		if w == 64 {
			return uint64(b) & 63
		}
		return uint64(uint32(b) & 31)
	}
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
		return a << shiftBy(), nil
	case ssa.OpShr:
		return a >> shiftBy(), nil
	case ssa.OpShrU:
		return int64(uint64(a) >> shiftBy()), nil
	case ssa.OpDiv:
		if b == 0 {
			return 0, fmt.Errorf("Run: division by zero")
		}
		return a / b, nil
	case ssa.OpDivU:
		if b == 0 {
			return 0, fmt.Errorf("Run: division by zero")
		}
		return int64(uint64(a) / uint64(b)), nil
	case ssa.OpRem:
		if b == 0 {
			return 0, fmt.Errorf("Run: remainder by zero")
		}
		return a % b, nil
	case ssa.OpRemU:
		if b == 0 {
			return 0, fmt.Errorf("Run: remainder by zero")
		}
		return int64(uint64(a) % uint64(b)), nil
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

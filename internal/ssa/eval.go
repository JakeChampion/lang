package ssa

import (
	"fmt"
	"math"
)

// Eval is a reference interpreter for the integer/control-flow subset of SSA.
// It exists as a correctness oracle: an optimisation pass must not change a
// function's result (`Eval(f) == Eval(optimize(f))`), and the SSA→native
// register allocator + emitter (#4112) are validated differentially against it
// (the emitted code's result must match `Eval`). This mirrors how the
// self-hosted compiler validates its SSA builder with an SSA interpreter
// (CLAUDE.md), and how `irlower.fern` is checked end-to-end without a backend.
//
// Scope: integer values (i32/i64 modelled as int64), booleans (0/1), the
// integer arithmetic / bitwise / shift / comparison ops, OpNot, OpSelect,
// phis, and the Br/BrIf/Ret terminators. Memory, calls, floats, strings, and
// composites are out of scope here — those gain evaluation as the emitter
// learns them, phase by phase. An unsupported op is a clear error, never a
// silent wrong answer.

// Eval interprets f with the given integer arguments (bound to f's params in
// order) and returns the value its Ret terminator produces. Width is modelled
// by masking i32 results to 32 bits; i64 ops use the full width. It cannot
// resolve OpCall — use EvalIn with a function table for programs that call.
func Eval(f *Func, args ...int64) (int64, error) {
	return EvalIn(nil, f, args...)
}

// EvalIn is Eval with a function table so OpCall can recurse into callees
// (resolved by name via Op.Str). Direct integer calls only.
func EvalIn(funcs map[string]*Func, f *Func, args ...int64) (int64, error) {
	v, _, err := evalWith(funcs, nil, newHeap(), f, args...)
	return v, err
}

// EvalInTable is EvalIn plus an ordered function-index table so OpCallIndirect
// can resolve a function value (an integer index, as produced by OpConstFunc →
// OpConstInt) to a callee: table[idx] is the callee's name, looked up in funcs.
// This mirrors the backends' function-table dispatch (wasm call_indirect / the
// native closure-cell pool), where a function value is its position in the
// module's ordered function list.
func EvalInTable(funcs map[string]*Func, table []string, f *Func, args ...int64) (int64, error) {
	v, _, err := evalWith(funcs, table, newHeap(), f, args...)
	return v, err
}

// heap is the evaluator's model memory: a little-endian byte buffer with a bump
// allocator. Address 0 is reserved (null) by starting the buffer at 8 bytes, so
// a dereference of an unallocated pointer is caught rather than aliasing the
// first allocation. Shared across calls (OpAlloc state persists), matching the
// real bump allocator.
type heap struct {
	data []byte
	// sent memoises OpEnumSentinel pointers by tag, so two sentinels with the
	// same tag yield the same pointer (the shared-static contract).
	sent map[int64]int64
}

func newHeap() *heap { return &heap{data: make([]byte, 8)} }

// sentinel returns the shared 4-byte static pointer for the given tag, storing
// the tag at the pointer on first use.
func (h *heap) sentinel(tag int64) int64 {
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

func (h *heap) alloc(size int64) int64 {
	base := int64(len(h.data))
	if r := base % 8; r != 0 { // 8-byte align
		base += 8 - r
	}
	if size < 0 {
		size = 0
	}
	h.data = append(h.data, make([]byte, base-int64(len(h.data))+size)...)
	return base
}

func (h *heap) check(addr int64, n int) error {
	if addr < 8 || addr+int64(n) > int64(len(h.data)) {
		return fmt.Errorf("Eval: out-of-bounds memory access at %d (heap %d bytes)", addr, len(h.data))
	}
	return nil
}

// load reads n bytes (1/2/8) little-endian. When signed, the result is sign-
// extended from the high bit of the n-byte value; otherwise zero-extended.
func (h *heap) load(addr int64, n int, signed bool) (int64, error) {
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

// store writes the low n bytes (1/2/8) of val little-endian, leaving higher
// bytes untouched.
func (h *heap) store(addr, val int64, n int) error {
	if err := h.check(addr, n); err != nil {
		return err
	}
	u := uint64(val)
	for i := 0; i < n; i++ {
		h.data[addr+int64(i)] = byte(u >> (8 * i))
	}
	return nil
}

// memAccess returns the byte width and signedness of a load/store op kind.
func memAccess(k OpKind) (bytes int, signed bool) {
	switch k {
	case OpLoad8U:
		return 1, false
	case OpLoad8S:
		return 1, true
	case OpLoad16U:
		return 2, false
	case OpLoad16S:
		return 2, true
	case OpStore8:
		return 1, false
	case OpStore16:
		return 2, false
	case OpLoad32U:
		return 4, false
	case OpStore32:
		return 4, false
	default: // OpLoad / OpStore (full 8-byte word)
		return 8, false
	}
}

func evalWith(funcs map[string]*Func, table []string, h *heap, f *Func, args ...int64) (int64, int64, error) {
	vals := map[int32]int64{}

	// strLen maps an OpConstString result to its literal byte length, so
	// OpConstStringLen (which references the OpConstString result) resolves the
	// compile-time length — matching how the backends lower it.
	strLen := map[int32]int{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind == OpConstString && op.Result.IsValid() {
				strLen[op.Result.ID] = len(op.Str)
			}
		}
	}

	params := realParams(f)
	if len(args) != len(params) {
		return 0, 0, fmt.Errorf("Eval: got %d args, function has %d params", len(args), len(params))
	}
	for i, p := range params {
		vals[p.ID] = args[i]
	}

	cur := f.Entry
	var from *Block // predecessor we arrived from, for phi resolution
	const maxSteps = 1 << 20
	for steps := 0; ; steps++ {
		if steps > maxSteps {
			return 0, 0, fmt.Errorf("Eval: step limit exceeded (non-terminating?)")
		}

		// Phis first, resolved against the edge we arrived on. All phis in a
		// block execute in PARALLEL: read every incoming arg before assigning
		// any result, so a phi whose arg is another phi in the same block (the
		// swap / cycle case, e.g. `a,b = b,a`) sees the old value, not one a
		// sibling phi just overwrote. (A sequential read-then-assign here is the
		// classic out-of-SSA bug.)
		var phiResults []int32
		var phiValues []int64
		for _, op := range cur.Ops {
			if op.Kind != OpPhi {
				break
			}
			if from == nil {
				return 0, 0, fmt.Errorf("Eval: phi in entry block %d", cur.ID)
			}
			pi := predIndex(cur, from)
			if pi < 0 || pi >= len(op.Args) {
				return 0, 0, fmt.Errorf("Eval: phi v%d has no arg for predecessor block %d", op.Result.ID, from.ID)
			}
			v, err := readVal(vals, op.Args[pi])
			if err != nil {
				return 0, 0, err
			}
			phiResults = append(phiResults, op.Result.ID)
			phiValues = append(phiValues, v)
		}
		for k, id := range phiResults {
			vals[id] = phiValues[k]
		}

		// Then the straight-line ops.
		for _, op := range cur.Ops {
			if op.Kind == OpPhi {
				continue
			}
			if err := evalOp(funcs, table, h, strLen, op, vals); err != nil {
				return 0, 0, err
			}
		}

		// Terminator.
		switch cur.Term.Kind {
		case TermRet:
			if !cur.Term.Value.IsValid() {
				return 0, 0, nil // void return
			}
			v, err := readVal(vals, cur.Term.Value)
			return v, 0, err
		case TermRetPair:
			tag, err := readVal(vals, cur.Term.Value)
			if err != nil {
				return 0, 0, err
			}
			payload, err := readVal(vals, cur.Term.Value2)
			if err != nil {
				return 0, 0, err
			}
			return tag, payload, nil
		case TermBr:
			from, cur = cur, cur.Term.Target
		case TermBrIf:
			c, err := readVal(vals, cur.Term.Cond)
			if err != nil {
				return 0, 0, err
			}
			if c != 0 {
				from, cur = cur, cur.Term.True
			} else {
				from, cur = cur, cur.Term.False
			}
		default:
			return 0, 0, fmt.Errorf("Eval: unsupported terminator %v in block %d", cur.Term.Kind, cur.ID)
		}
		if cur == nil {
			return 0, 0, fmt.Errorf("Eval: branch to nil block")
		}
	}
}

// realParams returns f's params excluding the zero sentinel at index 0.
func realParams(f *Func) []Value {
	out := make([]Value, 0, len(f.Params))
	for _, p := range f.Params {
		if p.IsValid() {
			out = append(out, p)
		}
	}
	return out
}

func readVal(vals map[int32]int64, v Value) (int64, error) {
	if !v.IsValid() {
		return 0, fmt.Errorf("Eval: read of invalid value")
	}
	x, ok := vals[v.ID]
	if !ok {
		return 0, fmt.Errorf("Eval: value v%d used before defined", v.ID)
	}
	return x, nil
}

// mask applies the op's width to a result: 32-bit ops keep the low 32 bits
// (sign-extended back to int64 so comparisons behave), 64-bit ops pass through.
func mask(width int8, v int64) int64 {
	if width == 64 {
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

// funcIndex returns name's function-value index — its position in the ordered
// function table, biased by one so that 0 is the NULL function reference (the
// value a closure cell's drop slot carries when its target has no
// __closure_drop_ thunk; see docs/SSA-CLOSURE-DISPATCH.md). Returns false if
// absent.
func funcIndex(table []string, name string) (int64, bool) {
	for i, n := range table {
		if n == name {
			return int64(i) + 1, true
		}
	}
	return 0, false
}

// storeCaptures allocates an env block of len(op.Args) pointer-width (8-byte)
// slots on h, stores each capture value (Args[i] at offset 8*i), and returns
// the env pointer. Shared by OpMakeEnv and OpMakeClosure.
func storeCaptures(h *heap, op *Op, arg func(int) (int64, error)) (int64, error) {
	env := h.alloc(int64(len(op.Args)) * 8)
	for i := range op.Args {
		v, err := arg(i)
		if err != nil {
			return 0, err
		}
		if err := h.store(env+int64(i)*8, v, 8); err != nil {
			return 0, err
		}
	}
	return env, nil
}

func evalOp(funcs map[string]*Func, table []string, h *heap, strLen map[int32]int, op *Op, vals map[int32]int64) error {
	// Binary integer ops read Args[0], Args[1]; unary read Args[0].
	arg := func(i int) (int64, error) {
		if i >= len(op.Args) {
			return 0, fmt.Errorf("Eval: %v missing arg %d", op.Kind, i)
		}
		return readVal(vals, op.Args[i])
	}
	set := func(v int64) error {
		if !op.Result.IsValid() {
			return fmt.Errorf("Eval: %v has no result", op.Kind)
		}
		vals[op.Result.ID] = mask(op.Width, v)
		return nil
	}
	// setF stores a float result as its IEEE-754 f64 bit pattern (rounded to
	// f32 precision when the op is 32-bit). Floats live in the same int64 slots
	// as ints — as their bits — exactly like a hardware register, so they must
	// NOT go through the integer-width mask in set().
	setF := func(v float64) error {
		if !op.Result.IsValid() {
			return fmt.Errorf("Eval: %v has no result", op.Kind)
		}
		if op.Width == 32 {
			v = float64(float32(v))
		}
		vals[op.Result.ID] = int64(math.Float64bits(v))
		return nil
	}
	// farg reads a float operand back from its bit pattern.
	farg := func(i int) (float64, error) {
		b, err := arg(i)
		if err != nil {
			return 0, err
		}
		return math.Float64frombits(uint64(b)), nil
	}

	switch op.Kind {
	case OpConstInt:
		return set(op.Imm)
	case OpConstBool:
		return set(op.Imm)

	case OpAdd, OpSub, OpMul, OpDiv, OpDivU, OpRem, OpRemU,
		OpAnd, OpOr, OpXor, OpShl, OpShr, OpShrU:
		a, err := arg(0)
		if err != nil {
			return err
		}
		c, err := arg(1)
		if err != nil {
			return err
		}
		r, err := evalBinaryInt(op.Kind, a, c, op.Width)
		if err != nil {
			return err
		}
		return set(r)

	case OpNeg:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(-a)
	case OpTrunc:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(int64(int32(a))) // i64 -> i32, sign-aware low 32
	case OpExtendS:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(int64(int32(a))) // i32 -> i64 sign-extend
	case OpExtendU:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(int64(uint32(a))) // i32 -> i64 zero-extend
	case OpExtend8S:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(int64(int8(a)))
	case OpExtend16S:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(int64(int16(a)))
	case OpNot:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(b2i(a == 0))

	case OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe, OpGeU:
		a, err := arg(0)
		if err != nil {
			return err
		}
		c, err := arg(1)
		if err != nil {
			return err
		}
		return set(evalCompare(op.Kind, a, c))

	case OpSelect:
		cond, err := arg(0)
		if err != nil {
			return err
		}
		t, err := arg(1)
		if err != nil {
			return err
		}
		e, err := arg(2)
		if err != nil {
			return err
		}
		if cond != 0 {
			return set(t)
		}
		return set(e)

	case OpCall, OpCallPair:
		if funcs == nil {
			return fmt.Errorf("Eval: %v %q requires a function table (use EvalIn)", op.Kind, op.Str)
		}
		callee, ok := funcs[op.Str]
		if !ok {
			return fmt.Errorf("Eval: unknown callee %q", op.Str)
		}
		argvals := make([]int64, 0, len(op.Args))
		for i := range op.Args {
			v, err := arg(i)
			if err != nil {
				return err
			}
			argvals = append(argvals, v)
		}
		r0, r1, err := evalWith(funcs, table, h, callee, argvals...)
		if err != nil {
			return err
		}
		if op.Kind == OpCallPair {
			if op.Result.IsValid() {
				vals[op.Result.ID] = mask(op.Width, r0)
			}
			if op.Result2.IsValid() {
				vals[op.Result2.ID] = r1
			}
			return nil
		}
		return set(r0)

	case OpCallIndirect:
		// Args[0] is the callee: a function value, i.e. a pointer to a closure
		// cell (fn = table index at +0, env_ptr at +8) — the shape
		// OpMakeClosure / OpConstFunc build. Args[1..] are the call arguments.
		// Dispatch derefs the cell and calls fn with env appended as the last
		// argument (see docs/SSA-CLOSURE-DISPATCH.md).
		if funcs == nil {
			return fmt.Errorf("Eval: OpCallIndirect requires a function table (use EvalInTable)")
		}
		if len(op.Args) < 1 {
			return fmt.Errorf("Eval: OpCallIndirect needs a callee operand")
		}
		ptr, err := arg(0)
		if err != nil {
			return err
		}
		idx, err := h.load(ptr, 8, false) // fn = cell[0]
		if err != nil {
			return err
		}
		env, err := h.load(ptr+8, 8, false) // env_ptr = cell[8]
		if err != nil {
			return err
		}
		if idx < 1 || idx > int64(len(table)) {
			return fmt.Errorf("Eval: OpCallIndirect fn index %d out of range (table has %d entries, index 0 is the null reference)", idx, len(table))
		}
		callee, ok := funcs[table[idx-1]]
		if !ok {
			return fmt.Errorf("Eval: OpCallIndirect target %q (index %d) not in function table", table[idx-1], idx)
		}
		argvals := make([]int64, 0, len(op.Args))
		for i := 1; i < len(op.Args); i++ {
			v, err := arg(i)
			if err != nil {
				return err
			}
			argvals = append(argvals, v)
		}
		argvals = append(argvals, env) // env is the last parameter
		r0, _, err := evalWith(funcs, table, h, callee, argvals...)
		if err != nil {
			return err
		}
		return set(r0)

	case OpMakeEnv:
		// Allocate an env block of len(Args) pointer-width (8-byte) slots and
		// store each capture; return the env pointer.
		env, err := storeCaptures(h, op, arg)
		if err != nil {
			return err
		}
		return set(env)

	case OpMakeClosure:
		// Allocate the env block (as OpMakeEnv) plus the 32-byte
		// {fn_idx, env_ptr, drop_idx, env_ptr} cell, and return the cell pointer.
		// fn_idx is the target's function-value index — the value an
		// OpCallIndirect on this closure dispatches on. drop_idx names the
		// target's __closure_drop_ thunk (0 = none), and the env_ptr duplicate at
		// +24 makes {drop_idx, env_ptr} itself a dispatchable cell, which is how a
		// generic holder like __drop_arr_closure frees an element's captures
		// without knowing its closure's identity (docs/SSA-CLOSURE-DISPATCH.md).
		idx, ok := funcIndex(table, op.Str)
		if !ok {
			return fmt.Errorf("Eval: OpMakeClosure target %q not in function table (use EvalInTable)", op.Str)
		}
		// A zero-capture closure has no env block, so nothing may dispatch its
		// drop sub-pair: both slots stay null.
		var env, dropIdx int64
		if len(op.Args) > 0 {
			dropIdx, _ = funcIndex(table, "__closure_drop_"+op.Str)
			e, err := storeCaptures(h, op, arg)
			if err != nil {
				return err
			}
			env = e
		}
		cell := h.alloc(32)
		for i, v := range []int64{idx, env, dropIdx, env} {
			if err := h.store(cell+int64(i)*8, v, 8); err != nil {
				return err
			}
		}
		return set(cell)

	case OpAlloc:
		size, err := arg(0)
		if err != nil {
			return err
		}
		return set(h.alloc(size))
	case OpLoad, OpLoad8U, OpLoad8S, OpLoad16U, OpLoad16S, OpLoad32U:
		base, err := arg(0)
		if err != nil {
			return err
		}
		n, signed := memAccess(op.Kind)
		v, err := h.load(base+op.Imm, n, signed)
		if err != nil {
			return err
		}
		return set(v)
	case OpStore, OpStore8, OpStore16, OpStore32:
		base, err := arg(0)
		if err != nil {
			return err
		}
		val, err := arg(1)
		if err != nil {
			return err
		}
		n, _ := memAccess(op.Kind)
		return h.store(base+op.Imm, val, n) // no result

	case OpConstString:
		// Materialise the literal bytes on the heap and return a pointer.
		p := h.alloc(int64(len(op.Str)))
		for i := 0; i < len(op.Str); i++ {
			if err := h.store(p+int64(i), int64(op.Str[i]), 1); err != nil {
				return err
			}
		}
		return set(p)
	case OpConstStringLen:
		if len(op.Args) != 1 {
			return fmt.Errorf("Eval: OpConstStringLen needs 1 arg")
		}
		n, ok := strLen[op.Args[0].ID]
		if !ok {
			return fmt.Errorf("Eval: OpConstStringLen arg is not an OpConstString result")
		}
		return set(int64(n))

	// --- floats (stored as f64 bits; see setF/farg) ---
	case OpConstFloat:
		return setF(op.F64)
	case OpFAdd, OpFSub, OpFMul, OpFDiv:
		a, err := farg(0)
		if err != nil {
			return err
		}
		b, err := farg(1)
		if err != nil {
			return err
		}
		switch op.Kind {
		case OpFAdd:
			return setF(a + b)
		case OpFSub:
			return setF(a - b)
		case OpFMul:
			return setF(a * b)
		default:
			return setF(a / b)
		}
	case OpFNeg:
		a, err := farg(0)
		if err != nil {
			return err
		}
		return setF(-a)
	case OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe:
		a, err := farg(0)
		if err != nil {
			return err
		}
		b, err := farg(1)
		if err != nil {
			return err
		}
		return set(evalFCompare(op.Kind, a, b))
	case OpFPromote: // f32 -> f64 (value already stored as f64 bits): identity
		a, err := farg(0)
		if err != nil {
			return err
		}
		vals[op.Result.ID] = int64(math.Float64bits(a))
		return nil
	case OpFDemote: // f64 -> f32: round to f32 precision
		a, err := farg(0)
		if err != nil {
			return err
		}
		vals[op.Result.ID] = int64(math.Float64bits(float64(float32(a))))
		return nil
	case OpIToFS:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return setF(float64(a))
	case OpIToFU:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return setF(float64(uint64(a)))
	case OpFToIS:
		a, err := farg(0)
		if err != nil {
			return err
		}
		return set(satFToIS(a, op.Width))
	case OpFToIU:
		a, err := farg(0)
		if err != nil {
			return err
		}
		return set(satFToIU(a, op.Width))

	// Bit-reinterprets (no value conversion, just a type change on the same
	// bits). Floats live in the int64 slots AS their f64 bit pattern, so the
	// 64-bit reinterprets are the identity on the stored bits; the 32-bit ones
	// go via the f32 bit pattern.
	case OpReinterpretF64ToI64, OpReinterpretI64ToF64:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(a) // width 64: set() is the identity
	case OpReinterpretF32ToI32:
		f, err := farg(0)
		if err != nil {
			return err
		}
		return set(int64(math.Float32bits(float32(f)))) // f32 bits as i32 (set masks to 32)
	case OpReinterpretI32ToF32:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return setF(float64(math.Float32frombits(uint32(a)))) // i32 bits as f32, stored as f64 bits

	case OpLoadF:
		base, err := arg(0)
		if err != nil {
			return err
		}
		v, err := h.load(base+op.Imm, 8, false)
		if err != nil {
			return err
		}
		vals[op.Result.ID] = v // raw f64 bits
		return nil
	case OpStoreF:
		base, err := arg(0)
		if err != nil {
			return err
		}
		val, err := arg(1)
		if err != nil {
			return err
		}
		return h.store(base+op.Imm, val, 8) // no result

	case OpEnumSentinel:
		return set(h.sentinel(op.Imm))

	default:
		return fmt.Errorf("Eval: unsupported op %v", op.Kind)
	}
}

// evalBinaryInt evaluates an integer binary op. width is the operand width (64
// for i64/u64, else 32): for the UNSIGNED ops (OpDivU / OpRemU / OpShrU) at
// 32-bit width the operands must be reinterpreted as uint32, because 32-bit
// values are stored sign-extended into the int64 slot (see mask), so a u32 with
// bit 31 set carries 1s in bits 32-63. Reading those bits as part of an unsigned
// 64-bit divide/shift yields a wrong result (the u32 `>>` bug that miscompiled
// SHA-256). Signed ops want the sign-extended value as-is; masking would be wrong.
func evalBinaryInt(k OpKind, a, b int64, width int8) (int64, error) {
	if width != 64 {
		switch k {
		case OpDivU, OpRemU, OpShrU:
			a = int64(uint32(a))
			b = int64(uint32(b))
		}
	}
	switch k {
	case OpAdd:
		return a + b, nil
	case OpSub:
		return a - b, nil
	case OpMul:
		return a * b, nil
	case OpDiv:
		if b == 0 {
			return 0, fmt.Errorf("Eval: division by zero")
		}
		return a / b, nil
	case OpDivU:
		if b == 0 {
			return 0, fmt.Errorf("Eval: division by zero")
		}
		return int64(uint64(a) / uint64(b)), nil
	case OpRem:
		if b == 0 {
			return 0, fmt.Errorf("Eval: remainder by zero")
		}
		return a % b, nil
	case OpRemU:
		if b == 0 {
			return 0, fmt.Errorf("Eval: remainder by zero")
		}
		return int64(uint64(a) % uint64(b)), nil
	case OpAnd:
		return a & b, nil
	case OpOr:
		return a | b, nil
	case OpXor:
		return a ^ b, nil
	case OpShl:
		return a << shiftCount(b, width), nil
	case OpShr:
		return a >> shiftCount(b, width), nil
	case OpShrU:
		return int64(uint64(a) >> shiftCount(b, width)), nil
	default:
		return 0, fmt.Errorf("Eval: not a binary int op: %v", k)
	}
}

// shiftCount masks a shift amount to the operand width — 5 bits at 32, 6 at
// 64 — matching what every ISA does with the low bits of the count register
// and what foldint.go folds. Go instead yields 0 for an out-of-range count,
// so an unmasked `a << uint64(b)` here would make the model disagree with
// both the language and the backends it is the oracle for.
func shiftCount(b int64, width int8) uint64 {
	if width == 64 {
		return uint64(b) & 63
	}
	return uint64(uint32(b) & 31)
}

func evalFCompare(k OpKind, a, b float64) int64 {
	switch k {
	case OpFEq:
		return b2i(a == b)
	case OpFNe:
		return b2i(a != b)
	case OpFLt:
		return b2i(a < b)
	case OpFLe:
		return b2i(a <= b)
	case OpFGt:
		return b2i(a > b)
	case OpFGe:
		return b2i(a >= b)
	default:
		return 0
	}
}

// evalCompare evaluates an integer comparison. Unlike the unsigned value ops
// (see evalBinaryInt), the unsigned comparisons need NO 32-bit operand masking:
// sign-extension is strictly monotonic over uint32 (every bit-31-set value maps
// to 0xFFFFFFFF_xxxxxxxx, which sorts above every bit-31-clear value — exactly
// the uint32 order), so an unsigned 64-bit compare of sign-extended operands
// yields the same result as a uint32 compare. The arm64/x86-64 SSA backends rely
// on the same property to emit a plain full-width unsigned compare.
func evalCompare(k OpKind, a, b int64) int64 {
	switch k {
	case OpEq:
		return b2i(a == b)
	case OpNe:
		return b2i(a != b)
	case OpLt:
		return b2i(a < b)
	case OpLtU:
		return b2i(uint64(a) < uint64(b))
	case OpLe:
		return b2i(a <= b)
	case OpLeU:
		return b2i(uint64(a) <= uint64(b))
	case OpGt:
		return b2i(a > b)
	case OpGtU:
		return b2i(uint64(a) > uint64(b))
	case OpGe:
		return b2i(a >= b)
	case OpGeU:
		return b2i(uint64(a) >= uint64(b))
	default:
		return 0
	}
}

package arm64ssa_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/ssa"
)

// assembleWX assembles a program into a W^X static AArch64 ELF (an R+X code
// segment and a separate R+W data segment), so the .bss globals — the bump
// heap's cursor and limit, the string builder — are writable at runtime.
// Non-heap programs assemble fine too: their data blob is empty.
func assembleWX(t *testing.T, asm string) []byte {
	t.Helper()
	text, rodata, err := nativearm64.AssembleProgramWX(asm, nativeelf.TextVAddrWX)
	if err != nil {
		t.Fatalf("AssembleProgramWX: %v\n--- asm ---\n%s", err, asm)
	}
	return nativeelf.StaticExecutableDataWX(text, rodata)
}

// constOp adds an integer constant op and returns its value.
func constOp(f *ssa.Func, b *ssa.Block, imm int64) ssa.Value {
	v := f.AddOp(b, ssa.OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = imm
	return v
}

// callOp adds a direct-call op to callee with args and returns its result.
func callOp(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) ssa.Value {
	v := f.AddOp(b, ssa.OpCall, args...)
	b.Ops[len(b.Ops)-1].Str = callee
	return v
}

// loadOp / storeOp add a full-word heap load / store at base+offset.
func loadOp(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64) ssa.Value {
	v := f.AddOp(b, ssa.OpLoad, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

func storeOp(f *ssa.Func, b *ssa.Block, base, val ssa.Value, offset int64) {
	op := f.AddOpNoResult(b, ssa.OpStore, base, val)
	op.Imm = offset
}

// assembleRunArmModule assembles a multi-function module's arm64 SSA output into
// a static AArch64 ELF and runs it under qemu-aarch64, returning the exit code.
// Skips when qemu is unavailable.
func assembleRunArmModule(t *testing.T, funcs map[string]*ssa.Func, entry string, numAlloc int, entryArgs ...int64) int {
	t.Helper()
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		t.Skip("qemu-aarch64 not available")
	}
	ssa.AnnotateCallWidths(funcs) // match the CLI pipeline: 64-bit returns skip the i32 mask
	asm, err := arm64ssa.EmitAsmModule(funcs, entry, numAlloc, entryArgs)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, assembleWX(t, asm), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	if e := exec.Command(qemu, bin).Run(); e != nil {
		var ee *exec.ExitError
		if errors.As(e, &ee) {
			return ee.ExitCode()
		}
		t.Fatalf("run: %v", e)
	}
	return 0
}

// moduleMatchesEval asserts the arm64 binary's exit code equals EvalIn(funcs,
// entry) mod 256 across several register-file sizes (small ones force spills and
// larger caller-save sets).
func moduleMatchesEval(t *testing.T, funcs map[string]*ssa.Func, entry string, entryArgs ...int64) {
	t.Helper()
	want, err := ssa.EvalIn(funcs, funcs[entry], entryArgs...)
	if err != nil {
		t.Fatalf("EvalIn: %v", err)
	}
	for _, n := range []int{2, 4, 8} {
		got := assembleRunArmModule(t, funcs, entry, n, entryArgs...)
		if got != int(uint8(want)) {
			t.Errorf("nAlloc=%d arm64 run exit=%d, want EvalIn&0xFF=%d (EvalIn=%d)", n, got, int(uint8(want)), want)
		}
	}
}

// assembleRunArm assembles f's arm64 SSA output into a static AArch64 ELF and
// runs it under qemu-aarch64, returning the process exit code. entryArgs are
// loaded into the argument registers before the entry call. Skips when qemu is
// unavailable.
func assembleRunArm(t *testing.T, f *ssa.Func, numAlloc int, entryArgs ...int64) int {
	t.Helper()
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		t.Skip("qemu-aarch64 not available")
	}
	asm, err := arm64ssa.EmitAsm(f, numAlloc, entryArgs...)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, assembleWX(t, asm), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	if e := exec.Command(qemu, bin).Run(); e != nil {
		var ee *exec.ExitError
		if errors.As(e, &ee) {
			return ee.ExitCode()
		}
		t.Fatalf("run: %v", e)
	}
	return 0
}

// runMatchesEval asserts the arm64 binary's exit code equals ssa.Eval(f) mod 256
// — the target-neutral oracle — so the AArch64 rendering is validated against the
// same model the x86-64 path uses. entryArgs feed both the model and the binary.
func runMatchesEval(t *testing.T, f *ssa.Func, numAlloc int, entryArgs ...int64) {
	t.Helper()
	want, err := ssa.Eval(f, entryArgs...)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	got := assembleRunArm(t, f, numAlloc, entryArgs...)
	if got != int(uint8(want)) {
		t.Errorf("arm64 run exit=%d, want Eval&0xFF=%d (Eval=%d)", got, int(uint8(want)), want)
	}
}

// (3 + 4) * 5 = 35 — constants, add, mul, return, run natively under qemu.
func TestArmRunArithmetic(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		a := constOp(f, e, 3)
		b := constOp(f, e, 4)
		sum := f.AddOp(e, ssa.OpAdd, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpMul, sum, constOp(f, e, 5)))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// A longer chain exercising sub / and / or / xor and the i32 mask.
func TestArmRunBitwiseChain(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		x := f.AddOp(e, ssa.OpMul, constOp(f, e, 6), constOp(f, e, 7)) // 42
		y := f.AddOp(e, ssa.OpSub, x, constOp(f, e, 2))                // 40
		z := f.AddOp(e, ssa.OpAnd, y, constOp(f, e, 0x3c))             // 40 & 0x3c = 40
		z = f.AddOp(e, ssa.OpOr, z, constOp(f, e, 1))                  // 41
		f.SetRet(e, f.AddOp(e, ssa.OpXor, z, constOp(f, e, 3)))        // 41 ^ 3 = 42
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// abs(-7) via a conditional: if (n < 0) return 0 - n; return n.  = 7.
func TestArmRunAbs(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpSub, constOp(f, e, 0), constOp(f, e, 7)) // -7
		neg := f.AddOp(e, ssa.OpLt, n, constOp(f, e, 0))               // n < 0
		then := f.NewBlock()
		els := f.NewBlock()
		f.SetBrIf(e, neg, then, els)
		f.SetRet(then, f.AddOp(then, ssa.OpSub, constOp(f, then, 0), n)) // 0 - n = 7
		f.SetRet(els, n)
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg)
	}
}

// A single-param identity-ish function: f(n) = n + 1, called with n = 40 -> 41.
// Exercises the parameter ABI (x0 -> home) and the entry-arg loader.
func TestArmRunOneParam(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		n := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, n, constOp(f, e, 1)))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg, 40)
	}
}

// Multi-param function whose homes force a parallel copy / swap: g(a, b) = a - b,
// called with (50, 8) -> 42. Two params, so at least one incoming arg register
// may collide with the other's home; the parallel-move resolver must be correct.
func TestArmRunTwoParams(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, ssa.OpSub, a, b))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg, 50, 8)
	}
}

// Order-sensitive multi-param: h(a, b, c) = (a - b) - c with (100, 30, 28) -> 42.
// Three params flowing through arithmetic that is not commutative, so any
// mis-shuffle of the argument registers changes the result.
func TestArmRunThreeParams(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		b := f.AddParam()
		c := f.AddParam()
		e := f.NewBlock()
		ab := f.AddOp(e, ssa.OpSub, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpSub, ab, c))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg, 100, 30, 28)
	}
}

// Cross-function direct calls: add(a,b)=a+b; main()=add(3,4)+add(10,20)=37.
// Exercises the call ABI (args in x0/x1, result in x0), x30 save/restore, and
// caller-save of the value live across the second call.
func TestArmRunModuleDirectCalls(t *testing.T) {
	build := func() map[string]*ssa.Func {
		add := ssa.NewFunc("add")
		a := add.AddParam()
		b := add.AddParam()
		ae := add.NewBlock()
		add.SetRet(ae, add.AddOp(ae, ssa.OpAdd, a, b))

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		t1 := callOp(main, me, "add", constOp(main, me, 3), constOp(main, me, 4))
		t2 := callOp(main, me, "add", constOp(main, me, 10), constOp(main, me, 20))
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, t1, t2))
		return map[string]*ssa.Func{"add": add, "main": main}
	}
	moduleMatchesEval(t, build(), "main")
}

// Self-recursion through the call ABI: factorial(5) = 120 -> exit 120. The
// recursive result is live across nothing, but n is live across the call, so the
// caller-save path is exercised, as are the branch-based base/rec split and the
// frame's x30 preservation on every level.
func TestArmRunModuleRecursion(t *testing.T) {
	build := func() map[string]*ssa.Func {
		f := ssa.NewFunc("factorial")
		n := f.AddParam()
		entry := f.NewBlock()
		base := f.NewBlock()
		rec := f.NewBlock()
		cond := f.AddOp(entry, ssa.OpLe, n, constOp(f, entry, 1))
		f.SetBrIf(entry, cond, base, rec)
		f.SetRet(base, constOp(f, base, 1))
		nm1 := f.AddOp(rec, ssa.OpSub, n, constOp(f, rec, 1))
		fr := callOp(f, rec, "factorial", nm1)
		f.SetRet(rec, f.AddOp(rec, ssa.OpMul, n, fr))
		return map[string]*ssa.Func{"factorial": f}
	}
	moduleMatchesEval(t, build(), "factorial", 5)
}

// Division and remainder via the AArch64 sdiv / msub sequence: with (a,b)=(47,5),
// (a/b)*10 + (a%b) = 9*10 + 2 = 92 -> exit 92. Params force a runtime divide (no
// const-fold), diffed against ssa.Eval.
func TestArmRunDivRem(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		q := f.AddOp(e, ssa.OpDiv, a, b)
		r := f.AddOp(e, ssa.OpRem, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpMul, q, constOp(f, e, 10)), r))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg, 47, 5)
	}
}

// Left shift + unsigned right shift: (a << sh) >>u 1 with (a,sh)=(5,4) = 80 >>u 1
// = 40 -> exit 40. Exercises lsl / lsr against the model.
func TestArmRunShifts(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		sh := f.AddParam()
		e := f.NewBlock()
		x := f.AddOp(e, ssa.OpShl, a, sh)
		f.SetRet(e, f.AddOp(e, ssa.OpShrU, x, constOp(f, e, 1)))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg, 5, 4)
	}
}

// Arithmetic (signed) right shift on a negative value: with a=64, (0-a) asr 2 =
// -16, then +80 = 64 -> exit 64. The param keeps the negative runtime (no
// const-fold, no negative immediate in _start), validating asr's sign behaviour.
func TestArmRunArithShiftSigned(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpSub, constOp(f, e, 0), a) // -a
		s := f.AddOp(e, ssa.OpShr, n, constOp(f, e, 2)) // asr 2
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, s, constOp(f, e, 80)))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg, 64)
	}
}

// TestArmRunUnsignedShiftWidth pins the u32 logical-right-shift width fix with a
// HARDCODED expected value (an Eval-diff would agree with the emit even if both
// regressed). (0 - 3) is 0xFFFFFFFD at i32 width, stored sign-extended; as u32
// that is 4294967293, and >>u 28 = 0xF = 15. A 64-bit `lsr` on the sign-extended
// value instead yields 0xFF = 255 — the SHA-256 miscompile. The operand comes
// from a runtime subtract (not a const-fold) so the sign-extension is live.
func TestArmRunUnsignedShiftWidth(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		x := f.AddParam()
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpSub, constOp(f, e, 0), x) // -x (0xFFFFFFFD for x=3)
		f.SetRet(e, f.AddOp(e, ssa.OpShrU, n, constOp(f, e, 28)))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		if got := assembleRunArm(t, build(), nreg, 3); got != 15 {
			t.Errorf("nreg=%d: (-3)>>u28 exit=%d, want 15", nreg, got)
		}
	}
}

// TestArmRunUnsignedDivWidth pins the u32 unsigned-divide width fix, again with a
// HARDCODED value. (0 - 4) is 0xFFFFFFFC at i32 width; as u32 that is 4294967292,
// and /u 1000000000 = 4. A signed 64-bit divide of the sign-extended -4 would
// yield 0. Both operands are runtime values so no const-fold intervenes.
func TestArmRunUnsignedDivWidth(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		x := f.AddParam()
		d := f.AddParam()
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpSub, constOp(f, e, 0), x) // -x (0xFFFFFFFC for x=4)
		f.SetRet(e, f.AddOp(e, ssa.OpDivU, n, d))
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		if got := assembleRunArm(t, build(), nreg, 4, 1000000000); got != 4 {
			t.Errorf("nreg=%d: 0xFFFFFFFC /u 1e9 exit=%d, want 4", nreg, got)
		}
	}
}

// TestArmRunFloatToI64Width pins the f64 -> i64 conversion width. 5e9 needs more
// than 32 bits, so a maskFix (sxtw) narrowing the fcvtzs result would corrupt it:
// (5000000000 as i64) / 1000000 = 5000, exit 5000&0xFF = 136. A 32-bit-narrowed
// conversion would yield 5e9 mod 2^32 = 705032704, /1e6 = 705, exit 193. The op
// carries Width 64 (the i64 destination), so maskFix must be skipped. HARDCODED.
func TestArmRunFloatToI64Width(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpFToIS, constFloat(f, e, 5000000000.0))
		e.Ops[len(e.Ops)-1].Width = 64 // i64 destination
		q := f.AddOp(e, ssa.OpDiv, n, constOp(f, e, 1000000))
		e.Ops[len(e.Ops)-1].Width = 64
		f.SetRet(e, q)
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		if got := assembleRunArm(t, build(), nreg); got != 136 {
			t.Errorf("nreg=%d: (5e9 as i64)/1e6 exit=%d, want 136", nreg, got)
		}
	}
}

// Heap round-trip: alloc 16 bytes, store 42/100 at offsets 0/8, load both, sum ->
// 142 -> exit 142. Exercises the bump allocator + full-word ldr/str against the
// writable .bss heap.
func TestArmRunMemoryRoundtrip(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 16))
		storeOp(f, e, p, constOp(f, e, 42), 0)
		storeOp(f, e, p, constOp(f, e, 100), 8)
		a := loadOp(f, e, p, 0)
		b := loadOp(f, e, p, 8)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, a, b))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// Heap shared across a call: main allocs a cell, setCell stores into it via a
// pointer arg, main reads it back -> 77. Validates that the heap survives the
// call ABI (pointer passed in x0, caller-saved across the bl).
func TestArmRunMemorySharedAcrossCalls(t *testing.T) {
	build := func() map[string]*ssa.Func {
		setCell := ssa.NewFunc("setCell")
		ptr := setCell.AddParam()
		val := setCell.AddParam()
		se := setCell.NewBlock()
		storeOp(setCell, se, ptr, val, 0)
		setCell.SetRet(se, ssa.Value{})

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		p := main.AddOp(me, ssa.OpAlloc, constOp(main, me, 8))
		_ = callOp(main, me, "setCell", p, constOp(main, me, 77))
		main.SetRet(me, loadOp(main, me, p, 0))
		return map[string]*ssa.Func{"setCell": setCell, "main": main}
	}
	moduleMatchesEval(t, build(), "main")
}

// Sub-word store/load widths: store a byte (200) and a halfword (4097) and read
// them back unsigned; (200 & 0xff) + (4097 & 0xffff) = 200 + 4097 = 4297 ->
// exit 4297 & 0xff = 201. Exercises strb/ldrb and strh/ldrh.
func TestArmRunSubwordMemory(t *testing.T) {
	byteOp := func(f *ssa.Func, b *ssa.Block, base, val ssa.Value, off int64, store ssa.OpKind) {
		op := f.AddOpNoResult(b, store, base, val)
		op.Imm = off
	}
	loadU := func(f *ssa.Func, b *ssa.Block, base ssa.Value, off int64, k ssa.OpKind) ssa.Value {
		v := f.AddOp(b, k, base)
		b.Ops[len(b.Ops)-1].Imm = off
		return v
	}
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8))
		byteOp(f, e, p, constOp(f, e, 200), 0, ssa.OpStore8)
		byteOp(f, e, p, constOp(f, e, 4097), 2, ssa.OpStore16)
		a := loadU(f, e, p, 0, ssa.OpLoad8U)
		b := loadU(f, e, p, 2, ssa.OpLoad16U)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, a, b))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// __fern_rc_is_unique on the arm64 SSA path: a freshly rc-headed heap cell
// (OpAlloc lays down rc == 1) is unique; a null or low-address non-pointer
// scalar is not. Exit code is the helper result directly (not Eval-derived,
// since Eval can't model runtime-helper calls).
func TestArmRunRcIsUnique(t *testing.T) {
	isUniqueOf := func(mkArg func(f *ssa.Func, b *ssa.Block) ssa.Value) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", mkArg(f, e)))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8)
	}
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return f.AddOp(b, ssa.OpAlloc, constOp(f, b, 8)) // fresh rc=1 cell
	}); got != 1 {
		t.Errorf("is_unique(fresh cell) = %d, want 1", got)
	}
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return constOp(f, b, 0) // null
	}); got != 0 {
		t.Errorf("is_unique(null) = %d, want 0", got)
	}
	if got := isUniqueOf(func(f *ssa.Func, b *ssa.Block) ssa.Value {
		return constOp(f, b, 42) // low-address scalar below the 0x10000 guard
	}); got != 0 {
		t.Errorf("is_unique(42) = %d, want 0", got)
	}
}

// __fern_rc_inc / __fern_rc_dec on the arm64 SSA path, observed through
// __fern_rc_is_unique: bumping the rc past 1 makes a cell non-unique; dropping it
// back to 1 restores uniqueness. The void inc/dec calls survive DCE and run in
// order before the is_unique read.
func TestArmRunRcIncDec(t *testing.T) {
	isUniqueAfter := func(ops ...string) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		c := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8)) // fresh rc=1 cell
		for _, op := range ops {
			callOp(f, e, op, c) // void, impure — kept + ordered
		}
		f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", c))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8)
	}
	if got := isUniqueAfter("__fern_rc_inc"); got != 0 {
		t.Errorf("is_unique after inc = %d, want 0 (rc=2)", got)
	}
	if got := isUniqueAfter("__fern_rc_inc", "__fern_rc_dec"); got != 1 {
		t.Errorf("is_unique after inc,dec = %d, want 1 (rc=1)", got)
	}
	if got := isUniqueAfter("__fern_rc_inc", "__fern_rc_inc", "__fern_rc_dec"); got != 0 {
		t.Errorf("is_unique after inc,inc,dec = %d, want 0 (rc=2)", got)
	}
}

// __fern_closure_drop on the arm64 SSA path, observed through
// __fern_rc_is_unique. Two paths: rc == 1 tail-calls __fern_box_free (a no-op on
// the bump heap, so the cell stays unique); rc > 1 tail-calls __fern_rc_dec
// (dropping a shared reference). __fern_box_free is pulled in transitively via
// runtimeHelperDeps.
func TestArmRunClosureDrop(t *testing.T) {
	// isUniqueAfter allocs a cell, applies each mutation call in order, then
	// returns is_unique(cell) as the exit code.
	isUniqueAfter := func(ops ...string) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		c := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8)) // fresh rc=1 cell
		for _, op := range ops {
			callOp(f, e, op, c) // void, impure — kept + ordered
		}
		f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", c))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8)
	}
	// rc=1 -> closure_drop -> box_free (no-op) -> still unique.
	if got := isUniqueAfter("__fern_closure_drop"); got != 1 {
		t.Errorf("is_unique after closure_drop(rc=1) = %d, want 1 (box_free no-op)", got)
	}
	// rc=1 -> inc -> 2 -> closure_drop -> rc_dec -> 1 -> unique again.
	if got := isUniqueAfter("__fern_rc_inc", "__fern_closure_drop"); got != 1 {
		t.Errorf("is_unique after inc,closure_drop = %d, want 1 (rc 2->1)", got)
	}
	// rc=1 -> inc -> inc -> 3 -> closure_drop -> rc_dec -> 2 -> not unique.
	if got := isUniqueAfter("__fern_rc_inc", "__fern_rc_inc", "__fern_closure_drop"); got != 0 {
		t.Errorf("is_unique after inc,inc,closure_drop = %d, want 0 (rc 3->2)", got)
	}
}

// __fern_box_free returns the data pointer and leaves the cell intact (it's a
// no-op until the reuse slice), so a fresh cell is still unique after it.
func TestArmRunBoxFreeNoop(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	c := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8))
	callOp(f, e, "__fern_box_free", c) // no-op
	f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", c))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != 1 {
		t.Errorf("is_unique after box_free = %d, want 1 (no-op)", got)
	}
}

// constStr adds an OpConstString literal and returns its data pointer.
func constStr(f *ssa.Func, b *ssa.Block, s string) ssa.Value {
	v := f.AddOp(b, ssa.OpConstString)
	b.Ops[len(b.Ops)-1].Str = s
	return v
}

// load8u adds an unsigned byte load at base+offset.
func load8u(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64) ssa.Value {
	v := f.AddOp(b, ssa.OpLoad8U, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

// A string literal materialised in .rodata: length + byte reads, diffed against
// ssa.Eval (which models OpConstString/OpConstStringLen/OpLoad8U). "Hello": len 5
// + 'H'(72) + 'e'(101) = 178.
func TestArmRunConstString(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		s := constStr(f, e, "Hello")
		l := f.AddOp(e, ssa.OpConstStringLen, s)
		b0 := load8u(f, e, s, 0)
		b1 := load8u(f, e, s, 1)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpAdd, l, b0), b1))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// Two distinct literals coexist in .rodata and read back independently:
// 'A'(65) + 'x'(120) = 185.
func TestArmRunTwoStrings(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		a := constStr(f, e, "AB")
		b := constStr(f, e, "xy")
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, load8u(f, e, a, 0), load8u(f, e, b, 0)))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// __str_len on the arm64 SSA path: __str_len("Hello") = 5 (exit code direct, as
// the helper call is not Eval-modellable).
func TestArmRunStrLenHelper(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, callOp(f, e, "__str_len", constStr(f, e, "Hello")))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != 5 {
		t.Errorf("__str_len(\"Hello\") = %d, want 5", got)
	}
}

// __str_eq on the arm64 SSA path: equal literals -> 1, differing byte or length
// -> 0.
func TestArmRunStrEqHelper(t *testing.T) {
	eq := func(a, b string) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		f.SetRet(e, callOp(f, e, "__str_eq", constStr(f, e, a), constStr(f, e, b)))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8)
	}
	if got := eq("AB", "AB"); got != 1 {
		t.Errorf("str_eq(AB,AB) = %d, want 1", got)
	}
	if got := eq("AB", "AC"); got != 0 {
		t.Errorf("str_eq(AB,AC) = %d, want 0", got)
	}
	if got := eq("AB", "ABC"); got != 0 {
		t.Errorf("str_eq(AB,ABC) = %d, want 0", got)
	}
}

// __str_concat on the arm64 SSA path: concat("AB","CD") = "ABCD". The result is a
// fresh heap string — check its length (4) and a byte (index 2 -> 'C' = 67).
func TestArmRunStrConcatHelper(t *testing.T) {
	// concatLen returns __str_len(concat(a,b)).
	concatLen := func(a, b string) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		c := callOp(f, e, "__str_concat", constStr(f, e, a), constStr(f, e, b))
		f.SetRet(e, callOp(f, e, "__str_len", c))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8)
	}
	if got := concatLen("AB", "CD"); got != 4 {
		t.Errorf("len(concat(AB,CD)) = %d, want 4", got)
	}
	// Byte 2 of "ABCD" is 'C' = 67.
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	c := callOp(f, e, "__str_concat", constStr(f, e, "AB"), constStr(f, e, "CD"))
	f.SetRet(e, load8u(f, e, c, 2))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != 67 {
		t.Errorf("concat(AB,CD)[2] = %d, want 67 ('C')", got)
	}
}

// ptrAdd computes base + n as a 64-bit pointer op (Width 64 so no i32 sxtw
// truncates the heap pointer).
func ptrAdd(f *ssa.Func, b *ssa.Block, base ssa.Value, n int64) ssa.Value {
	v := f.AddOp(b, ssa.OpAdd, base, constOp(f, b, n))
	b.Ops[len(b.Ops)-1].Width = 64
	return v
}

// store32 / load32u add a 4-byte (i32) store / load at base+offset.
func store32(f *ssa.Func, b *ssa.Block, base, val ssa.Value, offset int64) {
	op := f.AddOpNoResult(b, ssa.OpStore32, base, val)
	op.Imm = offset
}

func load32u(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64) ssa.Value {
	v := f.AddOp(b, ssa.OpLoad32U, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

// __fern_arr_dec on the arm64 SSA path, observed through __fern_rc_is_unique
// (array rc lives at [data-8], same as other rc-headed cells). rc<=1 leaks
// (unchanged); rc>1 drops one. The stride arg is ignored.
func TestArmRunArrDec(t *testing.T) {
	isUniqueAfterDec := func(incs int) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		c := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8)) // fresh rc=1 cell
		for i := 0; i < incs; i++ {
			callOp(f, e, "__fern_rc_inc", c)
		}
		callOp(f, e, "__fern_arr_dec", c, constOp(f, e, 4)) // (data, stride)
		f.SetRet(e, callOp(f, e, "__fern_rc_is_unique", c))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8)
	}
	// rc=1 -> arr_dec -> leak, rc stays 1 -> unique.
	if got := isUniqueAfterDec(0); got != 1 {
		t.Errorf("is_unique after arr_dec(rc=1) = %d, want 1 (leak)", got)
	}
	// rc=2 -> arr_dec -> 1 -> unique.
	if got := isUniqueAfterDec(1); got != 1 {
		t.Errorf("is_unique after inc,arr_dec = %d, want 1 (rc 2->1)", got)
	}
	// rc=3 -> arr_dec -> 2 -> not unique.
	if got := isUniqueAfterDec(2); got != 0 {
		t.Errorf("is_unique after inc,inc,arr_dec = %d, want 0 (rc 3->2)", got)
	}
}

// __arr_idx on the arm64 SSA path: a length-prefixed i32 array [10,20,30] with
// its length at [arr-4]. In-bounds index returns the element address (a[1]=20);
// an out-of-range index traps with exit 134.
func TestArmRunArrIdx(t *testing.T) {
	// buildArr lays a 3-element i32 array into a fresh allocation and returns
	// (f, arr): arr = p+4, len=3 at [arr-4]=p, elements at arr+0/4/8.
	buildArr := func() (*ssa.Func, *ssa.Block, ssa.Value) {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 32))
		arr := ptrAdd(f, e, p, 4)
		store32(f, e, p, constOp(f, e, 3), 0) // len at [arr-4] = p+0
		store32(f, e, p, constOp(f, e, 10), 4)
		store32(f, e, p, constOp(f, e, 20), 8)
		store32(f, e, p, constOp(f, e, 30), 12)
		return f, e, arr
	}
	// In-bounds: a[1] = 20.
	f, e, arr := buildArr()
	addr := callOp(f, e, "__arr_idx", arr, constOp(f, e, 1))
	f.SetRet(e, load32u(f, e, addr, 0))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != 20 {
		t.Errorf("a[1] via __arr_idx = %d, want 20", got)
	}
	// Out-of-range: idx 5 >= len 3 -> trap (exit 134).
	f2, e2, arr2 := buildArr()
	bad := callOp(f2, e2, "__arr_idx", arr2, constOp(f2, e2, 5))
	f2.SetRet(e2, load32u(f2, e2, bad, 0))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f2}, "main", 8); got != 134 {
		t.Errorf("__arr_idx out-of-range exit = %d, want 134", got)
	}
}

// constFloat adds an OpConstFloat literal.
func constFloat(f *ssa.Func, b *ssa.Block, v float64) ssa.Value {
	x := f.AddOp(b, ssa.OpConstFloat)
	b.Ops[len(b.Ops)-1].F64 = v
	return x
}

// Float arithmetic + convert, diffed against ssa.Eval (floats live as f64 bits,
// so the arm64 FP round-trip must agree bit-for-bit): (1.5 + 2.5) * 2.0 = 8.0 ->
// FToIS = 8.
func TestArmRunFloatArith(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		s := f.AddOp(e, ssa.OpFAdd, constFloat(f, e, 1.5), constFloat(f, e, 2.5))
		p := f.AddOp(e, ssa.OpFMul, s, constFloat(f, e, 2.0))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, p))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// Float comparison: 3.0 >= 3.0 -> 1 (fcmp + cset via the unsigned condition).
func TestArmRunFloatCompare(t *testing.T) {
	for _, tc := range []struct {
		op   ssa.OpKind
		a, b float64
		want int64
	}{
		{ssa.OpFGe, 3.0, 3.0, 1},
		{ssa.OpFLt, 2.0, 3.0, 1},
		{ssa.OpFLt, 3.0, 3.0, 0},
		{ssa.OpFGt, 3.5, 3.0, 1},
		{ssa.OpFEq, 1.25, 1.25, 1},
		{ssa.OpFNe, 1.25, 1.5, 1},
	} {
		build := func() *ssa.Func {
			f := ssa.NewFunc("main")
			e := f.NewBlock()
			f.SetRet(e, f.AddOp(e, tc.op, constFloat(f, e, tc.a), constFloat(f, e, tc.b)))
			return f
		}
		for _, n := range []int{2, 8} {
			runMatchesEval(t, build(), n)
		}
	}
}

// int->float->store->load->int: IToFS(5) + 0.75 = 5.75, stored and reloaded as
// f64 bits, then FToIS = 5. Exercises scvtf, float memory (8-byte load/store),
// and fcvtzs. nAlloc=2 forces a spill so the f64 bits round-trip a slot.
func TestArmRunFloatConvAndMemory(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 8))
		fv := f.AddOp(e, ssa.OpIToFS, constOp(f, e, 5))
		sum := f.AddOp(e, ssa.OpFAdd, fv, constFloat(f, e, 0.75))
		stf := f.AddOpNoResult(e, ssa.OpStoreF, p, sum)
		stf.Imm = 0
		ld := f.AddOp(e, ssa.OpLoadF, p)
		e.Ops[len(e.Ops)-1].Imm = 0
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, ld))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// Float negation and f32 demotion: -(2.5) demoted to f32 then back = -2.5 ->
// FToIS = -2 (truncation toward zero) -> exit 254 (uint8 of -2).
func TestArmRunFloatNegDemote(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		n := f.AddOp(e, ssa.OpFNeg, constFloat(f, e, 2.5))
		d := f.AddOp(e, ssa.OpFDemote, n)
		e.Ops[len(e.Ops)-1].Width = 32
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, d))
		return f
	}
	for _, n := range []int{2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// A float-returning cross-function call: mk() = 7.0, main returns mk() as i32 ->
// 7. Guards the call-result width propagation (ssa.AnnotateCallWidths): without
// it the f64 return is sxtw-masked to i32, zeroing its exponent bits -> 0.
func TestArmRunFloatReturnCall(t *testing.T) {
	build := func() map[string]*ssa.Func {
		mk := ssa.NewFunc("mk")
		mk.ReturnWidth = 64
		me := mk.NewBlock()
		mk.SetRet(me, constFloat(mk, me, 7.0))

		main := ssa.NewFunc("main")
		e := main.NewBlock()
		r := callOp(main, e, "mk")
		main.SetRet(e, main.AddOp(e, ssa.OpFToIS, r))
		return map[string]*ssa.Func{"mk": mk, "main": main}
	}
	for _, n := range []int{2, 8} {
		got := assembleRunArmModule(t, build(), "main", n)
		if got != 7 {
			t.Errorf("nAlloc=%d float-return call = %d, want 7", n, got)
		}
	}
}

// OpSelect via csel, with a and b kept live across the select so the operands
// exercise distinct registers: sel(cond,a,b) = (cond!=0 ? a : b) + (a - b).
// Diffed against ssa.Eval over both branches and several register counts.
func TestArmRunSelect(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		cond := f.AddParam()
		a := f.AddParam()
		b := f.AddParam()
		e := f.NewBlock()
		picked := f.AddOp(e, ssa.OpSelect, cond, a, b)
		diff := f.AddOp(e, ssa.OpSub, a, b)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, picked, diff))
		return f
	}
	for _, args := range [][]int64{{1, 40, 2}, {0, 40, 2}, {7, 5, 9}, {0, 5, 9}} {
		for _, n := range []int{2, 4, 8} {
			runMatchesEval(t, build(), n, args...)
		}
	}
}

// callPairOp adds a two-result direct call and returns (tag, payload).
func callPairOp(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) (ssa.Value, ssa.Value) {
	tag, payload := f.AddCallPair(b, args...)
	b.Ops[len(b.Ops)-1].Str = callee
	return tag, payload
}

// A pair-returning callee (TRetPair) reached via a two-result call (CallPair):
// split(x) returns (x, x+100); main sums them. Exercises the AArch64 pair-return
// convention (tag=x0, payload=x1) end to end.
func TestArmRunPairReturn(t *testing.T) {
	build := func() map[string]*ssa.Func {
		split := ssa.NewFunc("split")
		x := split.AddParam()
		se := split.NewBlock()
		hi := split.AddOp(se, ssa.OpAdd, x, constOp(split, se, 100))
		split.SetRetPair(se, x, hi)

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		tag, pay := callPairOp(main, me, "split", constOp(main, me, 5))
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, tag, pay))
		return map[string]*ssa.Func{"split": split, "main": main}
	}
	for _, n := range []int{2, 4, 8} {
		got := assembleRunArmModule(t, build(), "main", n) // 5 + 105 = 110
		if got != 110 {
			t.Errorf("nAlloc=%d pair-return sum = %d, want 110", n, got)
		}
	}
}

// Both pair results kept live across an intervening call, so the pair capture
// (x0/x1) and the caller-save handling must both hold: split(4) -> (4, 12), then
// id(tag) + pay = 4 + 12 = 16.
func TestArmRunPairReturnLiveAcrossCall(t *testing.T) {
	build := func() map[string]*ssa.Func {
		id := ssa.NewFunc("id")
		iv := id.AddParam()
		ie := id.NewBlock()
		id.SetRet(ie, iv)

		split := ssa.NewFunc("split")
		x := split.AddParam()
		se := split.NewBlock()
		hi := split.AddOp(se, ssa.OpMul, x, constOp(split, se, 3))
		split.SetRetPair(se, x, hi)

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		tag, pay := callPairOp(main, me, "split", constOp(main, me, 4))
		idt := callOp(main, me, "id", tag)
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, idt, pay))
		return map[string]*ssa.Func{"id": id, "split": split, "main": main}
	}
	for _, n := range []int{2, 4, 8} {
		got := assembleRunArmModule(t, build(), "main", n)
		if got != 16 {
			t.Errorf("nAlloc=%d pair-return-live-across = %d, want 16", n, got)
		}
	}
}

// makeClosureOp / callIndirectOp build a closure cell and an indirect dispatch.
func makeClosureOp(f *ssa.Func, b *ssa.Block, target string, caps ...ssa.Value) ssa.Value {
	v := f.AddOp(b, ssa.OpMakeClosure, caps...)
	b.Ops[len(b.Ops)-1].Str = target
	return v
}

func callIndirectOp(f *ssa.Func, b *ssa.Block, callee ssa.Value, args ...ssa.Value) ssa.Value {
	return f.AddOp(b, ssa.OpCallIndirect, append([]ssa.Value{callee}, args...)...)
}

// indirectFuncs: inc(x,env)=x+1, dbl(x,env)=x*2 (env unused, appended by the
// dispatch); apply(fn,x)=fn(x) via OpCallIndirect. table = [inc, dbl].
func indirectFuncs() (map[string]*ssa.Func, []string) {
	inc := ssa.NewFunc("inc")
	ix := inc.AddParam()
	inc.AddParam() // env
	ie := inc.NewBlock()
	inc.SetRet(ie, inc.AddOp(ie, ssa.OpAdd, ix, constOp(inc, ie, 1)))

	dbl := ssa.NewFunc("dbl")
	dx := dbl.AddParam()
	dbl.AddParam() // env
	de := dbl.NewBlock()
	dbl.SetRet(de, dbl.AddOp(de, ssa.OpMul, dx, constOp(dbl, de, 2)))

	apply := ssa.NewFunc("apply")
	fn := apply.AddParam()
	x := apply.AddParam()
	ae := apply.NewBlock()
	apply.SetRet(ae, callIndirectOp(apply, ae, fn, x))

	return map[string]*ssa.Func{"inc": inc, "dbl": dbl, "apply": apply}, []string{"inc", "dbl"}
}

// moduleMatchesEvalTable diffs the arm64 run against ssa.EvalInTable (the model
// resolves OpCallIndirect / OpMakeClosure through `table`). The asm derives its
// own fn_idx from the sorted emission order — each side is internally consistent,
// so the observable result agrees even though the index numbers differ.
func moduleMatchesEvalTable(t *testing.T, funcs map[string]*ssa.Func, table []string, entry string, entryArgs ...int64) {
	t.Helper()
	want, err := ssa.EvalInTable(funcs, table, funcs[entry], entryArgs...)
	if err != nil {
		t.Fatalf("EvalInTable: %v", err)
	}
	for _, n := range []int{2, 4, 8} {
		got := assembleRunArmModule(t, funcs, entry, n, entryArgs...)
		if got != int(uint8(want)) {
			t.Errorf("nAlloc=%d arm64 run exit=%d, want EvalInTable&0xFF=%d (=%d)", n, got, int(uint8(want)), want)
		}
	}
}

// Closure dispatch through apply: cInc/cDbl are {fn,env} cells dispatched via the
// function-address table; apply(cInc,10) + apply(cDbl,10) = 11 + 20 = 31.
func TestArmRunCallIndirect(t *testing.T) {
	funcs, table := indirectFuncs()
	main := ssa.NewFunc("main")
	me := main.NewBlock()
	cInc := makeClosureOp(main, me, "inc")
	cDbl := makeClosureOp(main, me, "dbl")
	r0 := callOp(main, me, "apply", cInc, constOp(main, me, 10))
	r1 := callOp(main, me, "apply", cDbl, constOp(main, me, 10))
	main.SetRet(me, main.AddOp(me, ssa.OpAdd, r0, r1))
	funcs["main"] = main
	moduleMatchesEvalTable(t, funcs, table, "main") // 31
}

// A runtime-chosen closure dispatched directly: the chosen cell pointer and the
// resolved target must survive the arg shuffle. main(sel,x) = (sel!=0 ? dbl : inc)(x).
func TestArmRunCallIndirectComputedTarget(t *testing.T) {
	build := func() (map[string]*ssa.Func, []string) {
		funcs, table := indirectFuncs()
		main := ssa.NewFunc("main")
		sel := main.AddParam()
		x := main.AddParam()
		me := main.NewBlock()
		cInc := makeClosureOp(main, me, "inc")
		cDbl := makeClosureOp(main, me, "dbl")
		cond := main.AddOp(me, ssa.OpNe, sel, constOp(main, me, 0))
		chosen := main.AddOp(me, ssa.OpSelect, cond, cDbl, cInc)
		main.SetRet(me, callIndirectOp(main, me, chosen, x))
		funcs["main"] = main
		return funcs, table
	}
	for _, args := range [][]int64{{0, 7}, {1, 7}, {0, 100}, {1, 50}} {
		funcs, table := build()
		moduleMatchesEvalTable(t, funcs, table, "main", args...)
	}
}

// A capturing closure dispatched through apply: g captures c=7, g(x)=x+c;
// apply(g, 35) = 42. Exercises MakeClosure with a real env + capture read-back.
func TestArmRunClosureCapture(t *testing.T) {
	add := ssa.NewFunc("addcap") // addcap(x, env): x + env[0]
	ax := add.AddParam()
	aenv := add.AddParam()
	ae := add.NewBlock()
	cap0 := add.AddOp(ae, ssa.OpLoad, aenv)
	ae.Ops[len(ae.Ops)-1].Imm = 0
	add.SetRet(ae, add.AddOp(ae, ssa.OpAdd, ax, cap0))

	apply := ssa.NewFunc("apply")
	fn := apply.AddParam()
	x := apply.AddParam()
	pe := apply.NewBlock()
	apply.SetRet(pe, callIndirectOp(apply, pe, fn, x))

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	g := makeClosureOp(main, me, "addcap", constOp(main, me, 7)) // capture c=7
	main.SetRet(me, callOp(main, me, "apply", g, constOp(main, me, 35)))

	funcs := map[string]*ssa.Func{"addcap": add, "apply": apply, "main": main}
	moduleMatchesEvalTable(t, funcs, []string{"addcap"}, "main") // 42
}

// A closure cell's DROP sub-pair is dispatchable on its own. The IR's generic
// __drop_arr_closure frees an array element's captures without knowing which
// closure it holds: it dispatches the cell at (element + 2*ptrW), which is why
// OpMakeClosure lays the cell out as {fn_idx, env_ptr, drop_idx, env_ptr} — the
// duplicated env makes the second half a well-formed cell of its own.
//
// A 2-slot cell made that walk read past the cell into the next heap block and
// call the LAMBDA as the element's drop routine, with the env in the wrong
// register — a SIGSEGV as soon as the lambda touched a capture (#6144). So
// dispatch both halves here: addcap(x, env) = x + env[0] through the cell,
// __closure_drop_addcap(env) = env[0] * 100 through the sub-pair. 42 + 700.
func TestArmRunClosureDropSubPair(t *testing.T) {
	add := ssa.NewFunc("addcap") // addcap(x, env): x + env[0]
	ax := add.AddParam()
	aenv := add.AddParam()
	ae := add.NewBlock()
	acap := add.AddOp(ae, ssa.OpLoad, aenv)
	ae.Ops[len(ae.Ops)-1].Imm = 0
	add.SetRet(ae, add.AddOp(ae, ssa.OpAdd, ax, acap))

	drop := ssa.NewFunc("__closure_drop_addcap") // drop(env): env[0] * 100
	denv := drop.AddParam()
	de := drop.NewBlock()
	dcap := drop.AddOp(de, ssa.OpLoad, denv)
	de.Ops[len(de.Ops)-1].Imm = 0
	drop.SetRet(de, drop.AddOp(de, ssa.OpMul, dcap, constOp(drop, de, 100)))

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	g := makeClosureOp(main, me, "addcap", constOp(main, me, 7)) // capture c=7
	sub := main.AddOp(me, ssa.OpAdd, g, constOp(main, me, 16))   // the drop sub-pair
	r0 := callIndirectOp(main, me, g, constOp(main, me, 35))     // 35 + 7
	r1 := callIndirectOp(main, me, sub)                          // 7 * 100
	main.SetRet(me, main.AddOp(me, ssa.OpAdd, r0, r1))

	funcs := map[string]*ssa.Func{"addcap": add, "__closure_drop_addcap": drop, "main": main}
	// 742 & 0xFF = 230; moduleMatchesEvalTable compares the exit byte, and the
	// model resolves the same sub-pair through its own copy of the layout.
	moduleMatchesEvalTable(t, funcs, []string{"addcap", "__closure_drop_addcap"}, "main")
}

// A zero-capture closure has no env block, so its cell must carry env_ptr = 0
// AND drop_idx = 0: with a live drop index the generic array walk would dispatch
// __closure_drop_<target> on a null env and fault reading its rc header. Read
// both slots back: 0 + 0 + 9 = 9.
func TestArmRunClosureZeroCaptureNullSlots(t *testing.T) {
	build := func() (map[string]*ssa.Func, []string) {
		bare := ssa.NewFunc("bare") // bare(x, env): x
		bx := bare.AddParam()
		bare.AddParam()
		be := bare.NewBlock()
		bare.SetRet(be, bx)

		drop := ssa.NewFunc("__closure_drop_bare") // never dispatched
		drop.AddParam()
		de := drop.NewBlock()
		drop.SetRet(de, constOp(drop, de, 1))

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		c := makeClosureOp(main, me, "bare")
		env := main.AddOp(me, ssa.OpLoad, c)
		me.Ops[len(me.Ops)-1].Imm = 8
		dropIdx := main.AddOp(me, ssa.OpLoad, c)
		me.Ops[len(me.Ops)-1].Imm = 16
		sum := main.AddOp(me, ssa.OpAdd, env, dropIdx)
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, sum, callIndirectOp(main, me, c, constOp(main, me, 9))))
		return map[string]*ssa.Func{"bare": bare, "__closure_drop_bare": drop, "main": main},
			[]string{"bare", "__closure_drop_bare"}
	}
	funcs, table := build()
	moduleMatchesEvalTable(t, funcs, table, "main") // 9
}

// enumSentinelOp adds an OpEnumSentinel for the given tag.
func enumSentinelOp(f *ssa.Func, b *ssa.Block, tag int64) ssa.Value {
	v := f.AddOp(b, ssa.OpEnumSentinel)
	b.Ops[len(b.Ops)-1].Imm = tag
	return v
}

// A payloadless enum sentinel: the value is a pointer to a shared static cell
// whose byte at offset 0 is the tag. Reading it back must yield the tag.
// sentinel(2) then load8u -> 2. Diffed against ssa.Eval (which models sentinels).
func TestArmRunEnumSentinel(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		s := enumSentinelOp(f, e, 2)
		f.SetRet(e, load8u(f, e, s, 0))
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// Two sentinels with the same tag share one static cell, so their pointers are
// equal; two different tags are not. (sent(1)==sent(1)) + (sent(1)==sent(3) ? 0)
// -> exercised as (a==b) below. Diffed against ssa.Eval (memoised sentinels).
func TestArmRunEnumSentinelIdentity(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		a := enumSentinelOp(f, e, 1)
		b := enumSentinelOp(f, e, 1)
		c := enumSentinelOp(f, e, 3)
		same := f.AddOp(e, ssa.OpEq, a, b) // 1 (same cell)
		diff := f.AddOp(e, ssa.OpEq, a, c) // 0 (different cells)
		// 1*10 + 0 = 10, plus the tag of c read back (3) -> 13.
		f.SetRet(e, f.AddOp(e, ssa.OpAdd,
			f.AddOp(e, ssa.OpMul, same, constOp(f, e, 10)),
			f.AddOp(e, ssa.OpAdd, diff, load8u(f, e, c, 0))))
		return f
	}
	for _, n := range []int{2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// UnNeg (unary minus) and UnOp (logical not, extends), diffed against ssa.Eval.
func TestArmRunUnaryOps(t *testing.T) {
	// neg: -(a - b) with a=2,b=9 -> -(2-9) = 7. Uses a param so no const-fold.
	negBuild := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		e := f.NewBlock()
		diff := f.AddOp(e, ssa.OpSub, a, constOp(f, e, 9))
		f.SetRet(e, f.AddOp(e, ssa.OpNeg, diff))
		return f
	}
	for _, n := range []int{2, 8} {
		runMatchesEval(t, negBuild(), n, 2)
	}
	// not: !(x != 0) style — OpNot(a) with a=0 -> 1, plus OpNot(b) with b=5 -> 0.
	notBuild := func(v int64) *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, ssa.OpNot, a))
		return f
	}
	for _, tc := range []struct{ in, want int64 }{{0, 1}, {5, 0}} {
		_ = tc.want
		for _, n := range []int{2, 8} {
			runMatchesEval(t, notBuild(tc.in), n, tc.in)
		}
	}
}

// Single-instruction f64 math helpers, each observed through FToIS. Also guards
// the helper-return width fix: an f64 return must not be i32-masked (which would
// zero its exponent bits). abs(-3.5)=3, sqrt(16)=4, floor(3.7)=3, ceil(3.2)=4,
// trunc(3.9)=3, round(3.5)=4 (ties away).
func TestArmRunFloatMathHelpers(t *testing.T) {
	cases := []struct {
		helper string
		in     float64
		want   int
	}{
		{"__abs_f64", -3.5, 3},
		{"__sqrt_f64", 16.0, 4},
		{"__floor_f64", 3.7, 3},
		{"__ceil_f64", 3.2, 4},
		{"__trunc_f64", 3.9, 3},
		{"__round_f64", 3.5, 4},
	}
	for _, tc := range cases {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		r := callOp(f, e, tc.helper, constFloat(f, e, tc.in))
		f.SetRet(e, f.AddOp(e, ssa.OpFToIS, r))
		got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8)
		if got != tc.want {
			t.Errorf("%s(%v) as i32 = %d, want %d", tc.helper, tc.in, got, tc.want)
		}
	}
}

// max via a comparison-selected branch: max(9, 4) = 9 -> exit 9.
func TestArmRunMax(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		a := constOp(f, e, 9)
		b := constOp(f, e, 4)
		agtb := f.AddOp(e, ssa.OpGt, a, b)
		ta := f.NewBlock()
		tb := f.NewBlock()
		f.SetBrIf(e, agtb, ta, tb)
		f.SetRet(ta, a)
		f.SetRet(tb, b)
		return f
	}
	for _, nreg := range []int{2, 4, 8} {
		runMatchesEval(t, build(), nreg)
	}
}

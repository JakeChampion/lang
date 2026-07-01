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
// segment and a separate R+W data segment), so the .bss bump heap is writable at
// runtime. Non-heap programs assemble fine too — their data blob is empty.
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

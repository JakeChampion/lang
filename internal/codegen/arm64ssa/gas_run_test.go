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

// constOp adds an integer constant op and returns its value.
func constOp(f *ssa.Func, b *ssa.Block, imm int64) ssa.Value {
	v := f.AddOp(b, ssa.OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = imm
	return v
}

// assembleRunArm assembles f's arm64 SSA output into a static AArch64 ELF and
// runs it under qemu-aarch64, returning the process exit code. Skips when qemu
// is unavailable.
func assembleRunArm(t *testing.T, f *ssa.Func, numAlloc int) int {
	t.Helper()
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		t.Skip("qemu-aarch64 not available")
	}
	asm, err := arm64ssa.EmitAsm(f, numAlloc)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	text, rodata, err := nativearm64.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram: %v\n--- asm ---\n%s", err, asm)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, nativeelf.StaticExecutableData(text, rodata), 0o755); err != nil {
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
// same model the x86-64 path uses.
func runMatchesEval(t *testing.T, f *ssa.Func, numAlloc int) {
	t.Helper()
	want, err := ssa.Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	got := assembleRunArm(t, f, numAlloc)
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

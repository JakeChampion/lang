package x86_64ssa

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/ssa"
)

// assembleRun renders f to real x86-64, links a static ELF, runs it, and
// returns the process exit code. Skips on non-amd64/linux hosts (the binary is
// native x86-64).
func assembleRun(t *testing.T, f *ssa.Func, numAlloc int) int {
	t.Helper()
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		t.Skipf("native x86-64 run needs amd64/linux, have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	asm, err := EmitAsm(f, numAlloc)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	text, rodata, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram: %v\n--- asm ---\n%s", err, asm)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, nativeelf.StaticExecutableDataX86(text, rodata), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	cmd := exec.Command(bin)
	err = cmd.Run()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("run: %v", err)
	return -1
}

// runMatchesEval asserts the real binary's exit code equals ssa.Eval mod 256.
func runMatchesEval(t *testing.T, f *ssa.Func, numAlloc int) {
	t.Helper()
	want, err := ssa.Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	got := assembleRun(t, f, numAlloc)
	if got != int(uint8(want)) {
		t.Errorf("real run exit=%d, want Eval&0xFF=%d (Eval=%d)", got, int(uint8(want)), want)
	}
}

// (3 + 4) * 5 = 35.
func TestAsmRunArithmetic(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		a := constOp(f, e, 3)
		b := constOp(f, e, 4)
		sum := f.AddOp(e, ssa.OpAdd, a, b)
		five := constOp(f, e, 5)
		prod := f.AddOp(e, ssa.OpMul, sum, five)
		f.SetRet(e, prod)
		return f
	}
	for _, n := range []int{2, 4, 8} {
		runMatchesEval(t, build(), n)
	}
}

// ((10 - 2) * 3) & 0x0F = 24 & 15 = 8 — exercises sub, imul, and, and a spill
// at nAlloc=1.
func TestAsmRunBitwiseSpill(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("f")
		e := f.NewBlock()
		ten := constOp(f, e, 10)
		two := constOp(f, e, 2)
		diff := f.AddOp(e, ssa.OpSub, ten, two)
		three := constOp(f, e, 3)
		prod := f.AddOp(e, ssa.OpMul, diff, three)
		mask := constOp(f, e, 0x0F)
		res := f.AddOp(e, ssa.OpAnd, prod, mask)
		f.SetRet(e, res)
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// Comparison: (5 < 9) -> 1 (exercises cmp + setcc + movzx).
func TestAsmRunComparison(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("lt")
		e := f.NewBlock()
		a := constOp(f, e, 5)
		b := constOp(f, e, 9)
		c := f.AddOp(e, ssa.OpLt, a, b)
		f.SetRet(e, c)
		return f
	}
	runMatchesEval(t, build(), 4)
}

// Control flow: if (1) return 7 else return 9 -> 7.
func TestAsmRunBranch(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("br")
		entry := f.NewBlock()
		thenB := f.NewBlock()
		elseB := f.NewBlock()
		cond := constOp(f, entry, 1)
		f.SetBrIf(entry, cond, thenB, elseB)
		seven := constOp(f, thenB, 7)
		f.SetRet(thenB, seven)
		nine := constOp(f, elseB, 9)
		f.SetRet(elseB, nine)
		return f
	}
	runMatchesEval(t, build(), 4)
}

// A real loop: i = 0; while (i < 5) i = i + 1; return i  -> 5. Header phi over
// the entry + back-edge, lowered to real branches + edge moves.
func TestAsmRunLoop(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("loop")
		entry := f.NewBlock()
		header := f.NewBlock()
		body := f.NewBlock()
		exit := f.NewBlock()
		init := constOp(f, entry, 0)
		f.SetBr(entry, header)
		iNext := f.NewValue()
		i := f.AddPhi(header, init, iNext)
		limit := constOp(f, header, 5)
		cond := f.AddOp(header, ssa.OpLt, i, limit)
		f.SetBrIf(header, cond, body, exit)
		one := constOp(f, body, 1)
		add := f.AddOpNoResult(body, ssa.OpAdd, i, one)
		add.Result = iNext
		f.SetBr(body, header)
		f.SetRet(exit, i)
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// A back-edge phi swap over a constant trip count: a,b = b,a for 3 iterations,
// return a -> 200 (odd count). Proves the parallel phi moves work in real code.
func TestAsmRunPhiSwap(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("swap")
		entry := f.NewBlock()
		header := f.NewBlock()
		body := f.NewBlock()
		exit := f.NewBlock()
		a0 := constOp(f, entry, 100)
		b0 := constOp(f, entry, 200)
		i0 := constOp(f, entry, 0)
		f.SetBr(entry, header)
		iNext := f.NewValue()
		a := f.AddPhi(header, a0, b0)
		b := f.AddPhi(header, b0, a0)
		i := f.AddPhi(header, i0, iNext)
		limit := constOp(f, header, 3)
		cond := f.AddOp(header, ssa.OpLt, i, limit)
		f.SetBrIf(header, cond, body, exit)
		one := constOp(f, body, 1)
		add := f.AddOpNoResult(body, ssa.OpAdd, i, one)
		add.Result = iNext
		f.SetBr(body, header)
		header.Ops[0].Args[1] = b
		header.Ops[1].Args[1] = a
		f.SetRet(exit, a)
		return f
	}
	runMatchesEval(t, build(), 8)
}

// Parameters are rejected (clear error) until the ABI slice.
func TestAsmRejectsParams(t *testing.T) {
	f := ssa.NewFunc("p")
	a := f.AddParam()
	e := f.NewBlock()
	f.SetRet(e, a)
	if _, err := EmitAsm(f, 4); err == nil {
		t.Error("expected EmitAsm to reject a function with parameters")
	}
}

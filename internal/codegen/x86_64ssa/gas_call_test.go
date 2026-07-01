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

// assembleRunModule renders a multi-function module to real x86-64, links a
// static ELF, runs it, and returns the exit code.
func assembleRunModule(t *testing.T, funcs map[string]*ssa.Func, entry string, numAlloc int, args []int64) int {
	t.Helper()
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		t.Skipf("native x86-64 run needs amd64/linux, have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	asm, err := EmitAsmModule(funcs, entry, numAlloc, args)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	text, rodata, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram: %v\n--- asm ---\n%s", err, asm)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, nativeelf.StaticExecutableDataX86(text, rodata), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	err = exec.Command(bin).Run()
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

// runModuleMatchesEval asserts the real binary's exit code equals
// ssa.EvalIn(entry, args) mod 256, exercising the direct-call ABI.
func runModuleMatchesEval(t *testing.T, funcs map[string]*ssa.Func, entry string, numAlloc int, args []int64) {
	t.Helper()
	want, err := ssa.EvalIn(funcs, funcs[entry], args...)
	if err != nil {
		t.Fatalf("EvalIn: %v", err)
	}
	got := assembleRunModule(t, funcs, entry, numAlloc, args)
	if got != int(uint8(want)) {
		t.Errorf("real run(%s, %v) exit=%d, want Eval&0xFF=%d (Eval=%d)", entry, args, got, int(uint8(want)), want)
	}
}

// add(a,b)=a+b ; main()=add(3,4)+add(10,20)=37. Cross-function direct calls.
func TestAsmRunModuleDirectCalls(t *testing.T) {
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

	funcs := map[string]*ssa.Func{"add": add, "main": main}
	for _, n := range []int{1, 2, 8} {
		runModuleMatchesEval(t, funcs, "main", n, nil)
	}
}

// factorial(n) = n<=1 ? 1 : n*factorial(n-1). Recursion — n is live across the
// recursive call, so the caller-saved preservation is exercised.
func TestAsmRunModuleRecursion(t *testing.T) {
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

	funcs := map[string]*ssa.Func{"factorial": f}
	for _, n := range []int{1, 2, 8} {
		for _, in := range [][]int64{{0}, {1}, {3}, {5}} {
			runModuleMatchesEval(t, funcs, "factorial", n, in)
		}
	}
}

// A caller with several values live across a call: h(x) = g(x) + g(x+1) + x.
// x survives both calls; g(x) survives the second. Stresses the caller-saved
// save/restore across multiple calls in one block.
func TestAsmRunModuleValuesLiveAcrossCalls(t *testing.T) {
	g := ssa.NewFunc("g")
	gx := g.AddParam()
	ge := g.NewBlock()
	g.SetRet(ge, g.AddOp(ge, ssa.OpMul, gx, constOp(g, ge, 2)))

	h := ssa.NewFunc("h")
	x := h.AddParam()
	he := h.NewBlock()
	c1 := callOp(h, he, "g", x)
	c2 := callOp(h, he, "g", h.AddOp(he, ssa.OpAdd, x, constOp(h, he, 1)))
	sum := h.AddOp(he, ssa.OpAdd, h.AddOp(he, ssa.OpAdd, c1, c2), x)
	h.SetRet(he, sum)

	funcs := map[string]*ssa.Func{"g": g, "h": h}
	for _, n := range []int{1, 2, 8} {
		runModuleMatchesEval(t, funcs, "h", n, []int64{10}) // 20 + 22 + 10 = 52
	}
}

// A six-argument callee reached through a call, so the arg-passing path fills
// every SysV integer arg register.
func TestAsmRunModuleSixArgs(t *testing.T) {
	sum6 := ssa.NewFunc("sum6")
	var ps []ssa.Value
	for i := 0; i < 6; i++ {
		ps = append(ps, sum6.AddParam())
	}
	se := sum6.NewBlock()
	acc := ps[0]
	for i := 1; i < 6; i++ {
		acc = sum6.AddOp(se, ssa.OpAdd, acc, ps[i])
	}
	sum6.SetRet(se, acc)

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	call := callOp(main, me, "sum6",
		constOp(main, me, 1), constOp(main, me, 2), constOp(main, me, 3),
		constOp(main, me, 4), constOp(main, me, 5), constOp(main, me, 6))
	main.SetRet(me, call)

	funcs := map[string]*ssa.Func{"sum6": sum6, "main": main}
	for _, n := range []int{1, 2, 8} {
		runModuleMatchesEval(t, funcs, "main", n, nil) // 21
	}
}

package x86_64ssa

import (
	"errors"
	"fmt"
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

// assembleRunArgs renders f to real x86-64 with the System V parameter ABI,
// baking `args` into the entry, runs it, and returns the process exit code.
func assembleRunArgs(t *testing.T, f *ssa.Func, numAlloc int, args []int64) int {
	t.Helper()
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		t.Skipf("native x86-64 run needs amd64/linux, have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	asm, err := EmitAsmArgs(f, numAlloc, args)
	if err != nil {
		t.Fatalf("EmitAsmArgs: %v", err)
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

// runMatchesEvalArgs asserts the real binary's exit code equals ssa.Eval(f,
// args) mod 256, exercising the parameter ABI.
func runMatchesEvalArgs(t *testing.T, f *ssa.Func, numAlloc int, args []int64) {
	t.Helper()
	want, err := ssa.Eval(f, args...)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	got := assembleRunArgs(t, f, numAlloc, args)
	if got != int(uint8(want)) {
		t.Errorf("real run(%v) exit=%d, want Eval&0xFF=%d (Eval=%d)", args, got, int(uint8(want)), want)
	}
}

// A single-parameter identity function returns its argument.
func TestAsmRunParamIdentity(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("id")
		a := f.AddParam()
		e := f.NewBlock()
		f.SetRet(e, a)
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{42})
	}
}

// sum4(a,b,c,d) = a+b+c+d — four params across rdi/rsi/rdx/rcx, kept live so
// the allocator must place them, incl. spills at nAlloc=1.
func TestAsmRunParamSum(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("sum4")
		a := f.AddParam()
		b := f.AddParam()
		c := f.AddParam()
		d := f.AddParam()
		e := f.NewBlock()
		ab := f.AddOp(e, ssa.OpAdd, a, b)
		cd := f.AddOp(e, ssa.OpAdd, c, d)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, ab, cd))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{10, 20, 30, 40})
	}
}

// A parameter used in a loop bound: countTo(n) = 0..n sum's trip count -> n.
// Exercises a param flowing into a phi + branch under the ABI.
func TestAsmRunParamLoop(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("countTo")
		limitP := f.AddParam()
		entry := f.NewBlock()
		header := f.NewBlock()
		body := f.NewBlock()
		exit := f.NewBlock()
		init := constOp(f, entry, 0)
		f.SetBr(entry, header)
		iNext := f.NewValue()
		i := f.AddPhi(header, init, iNext)
		cond := f.AddOp(header, ssa.OpLt, i, limitP)
		f.SetBrIf(header, cond, body, exit)
		one := constOp(f, body, 1)
		add := f.AddOpNoResult(body, ssa.OpAdd, i, one)
		add.Result = iNext
		f.SetBr(body, header)
		f.SetRet(exit, i)
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{7})
	}
}

// Six parameters exhaust the SysV integer arg registers (rdi..r9); pick the
// last so the ABI must thread r9 through.
func TestAsmRunParamSixth(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("sixth")
		var ps []ssa.Value
		for i := 0; i < 6; i++ {
			ps = append(ps, f.AddParam())
		}
		e := f.NewBlock()
		f.SetRet(e, ps[5])
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEvalArgs(t, build(), n, []int64{1, 2, 3, 4, 5, 99})
	}
}

// weightedSum builds a function of n parameters returning
// p0 + 2*p1 + 3*p2 + … — a result that changes if ANY argument is dropped,
// duplicated, or transposed. A function that ignored its later parameters
// would pass a stack-argument test while they were being lost (#8087).
func weightedSum(name string, n int) *ssa.Func {
	f := ssa.NewFunc(name)
	ps := make([]ssa.Value, n)
	for i := range ps {
		ps[i] = f.AddParam()
	}
	e := f.NewBlock()
	acc := ps[0]
	for i := 1; i < n; i++ {
		term := f.AddOp(e, ssa.OpMul, ps[i], constOp(f, e, int64(i+1)))
		acc = f.AddOp(e, ssa.OpAdd, acc, term)
	}
	f.SetRet(e, acc)
	return f
}

// countUp is the argument list 1..n.
func countUp(n int) []int64 {
	args := make([]int64, n)
	for i := range args {
		args[i] = int64(i + 1)
	}
	return args
}

// Arguments past the sixth arrive on the stack, at [rbp+16] and up (#8087).
// Seven crosses the boundary, eight is the aligned case, twelve is well past
// it — and the odd counts are the ones that would show a lost 16-byte
// alignment at the call.
func TestAsmRunStackParams(t *testing.T) {
	for _, n := range []int{7, 8, 9, 12} {
		for _, nAlloc := range []int{1, 2, 8} {
			runMatchesEvalArgs(t, weightedSum(fmt.Sprintf("p%d", n), n), nAlloc, countUp(n))
		}
	}
}

// The caller side: a direct call passing more than six arguments pushes the
// rest, and must leave rsp where it found it.
func TestAsmRunStackArgsDirectCall(t *testing.T) {
	for _, n := range []int{7, 8, 9, 12} {
		callee := weightedSum("callee", n)
		main := ssa.NewFunc("main")
		me := main.NewBlock()
		var args []ssa.Value
		for _, v := range countUp(n) {
			args = append(args, constOp(main, me, v))
		}
		// Call twice and add: a call that mismanaged rsp would land the second
		// one on a shifted stack rather than merely returning the wrong number.
		t1 := callOp(main, me, "callee", args...)
		t2 := callOp(main, me, "callee", args...)
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, t1, t2))

		funcs := map[string]*ssa.Func{"callee": callee, "main": main}
		for _, nAlloc := range []int{1, 2, 8} {
			runModuleMatchesEval(t, funcs, "main", nAlloc, nil)
		}
	}
}

// The indirect (closure) path: the env pointer rides as the final argument, so
// a six-argument call through a closure already needs a stack slot for it.
func TestAsmRunStackArgsIndirectCall(t *testing.T) {
	for _, n := range []int{6, 7, 10} {
		// target takes n call arguments plus the env pointer as its last param.
		target := weightedSum("target", n+1)

		apply := ssa.NewFunc("apply")
		ae := apply.NewBlock()
		c := makeClosureOp(apply, ae, "target")
		var args []ssa.Value
		for _, v := range countUp(n) {
			args = append(args, constOp(apply, ae, v))
		}
		apply.SetRet(ae, callIndirectOp(apply, ae, c, args...))

		funcs := map[string]*ssa.Func{"apply": apply, "target": target}
		table := sortedNames(funcs)
		for _, nAlloc := range []int{1, 2, 8} {
			runModuleTableMatchesEval(t, funcs, table, "apply", nAlloc, nil)
		}
	}
}

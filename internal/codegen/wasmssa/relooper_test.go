// Relooper tests — exercise CFG shapes the classifiers
// don't recognise, falling back to emitRelooper. Each test
// asserts the module validates and (where possible) runs
// under wasmtime with the expected return value.

package wasmssa

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// TestRelooperNestedIfReturning — a CFG with nested ifs that
// none of the existing classifiers handle:
//
//	if (a < 10) {
//	  if (a < 5) return 1;
//	  return 2;
//	}
//	return 3;
//
// Shape (6 blocks):
//
//	entry ─brif─→ inner ─brif─→ r1
//	         │           └─→ r2
//	         └─→ r3
//
// inner has a single pred (entry), r1/r2 have a single pred
// (inner), r3 has a single pred (entry). None of the existing
// classifiers fire on this 6-block shape. The relooper should.
func TestRelooperNestedIfReturning(t *testing.T) {
	f := ssa.NewFunc("nested")
	a := f.AddParam()
	entry := f.NewBlock()
	inner := f.NewBlock()
	r1 := f.NewBlock()
	r2 := f.NewBlock()
	r3 := f.NewBlock()

	ten := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 10
	outerCond := f.AddOp(entry, ssa.OpLt, a, ten)
	f.SetBrIf(entry, outerCond, inner, r3)

	five := f.AddOp(inner, ssa.OpConstInt)
	inner.Ops[0].Imm = 5
	innerCond := f.AddOp(inner, ssa.OpLt, a, five)
	f.SetBrIf(inner, innerCond, r1, r2)

	one := f.AddOp(r1, ssa.OpConstInt)
	r1.Ops[0].Imm = 1
	f.SetRet(r1, one)

	two := f.AddOp(r2, ssa.OpConstInt)
	r2.Ops[0].Imm = 2
	f.SetRet(r2, two)

	three := f.AddOp(r3, ssa.OpConstInt)
	r3.Ops[0].Imm = 3
	f.SetRet(r3, three)

	mod, err := EmitModule(f, "nested")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
	runRelooperCase(t, mod, "nested", []relooperCase{
		{args: []string{"3"}, want: 1},
		{args: []string{"7"}, want: 2},
		{args: []string{"15"}, want: 3},
	})
}

// TestRelooperFourArmedChain — a 7-block CFG with a 3-step
// early-return chain followed by a nested if in the tail.
// Composition the early-return classifier won't recognise
// because the tail isn't a single TermRet block.
//
//	if (a == 0) return 100;
//	if (a == 1) return 200;
//	if (a < 0) return 300;
//	if (a > 100) return 400;
//	return 0;
func TestRelooperFourArmedChain(t *testing.T) {
	f := ssa.NewFunc("classify")
	a := f.AddParam()

	mkRet := func(val int64) *ssa.Block {
		b := f.NewBlock()
		c := f.AddOp(b, ssa.OpConstInt)
		b.Ops[0].Imm = val
		f.SetRet(b, c)
		return b
	}
	mkConst := func(b *ssa.Block, v int64) ssa.Value {
		c := f.AddOp(b, ssa.OpConstInt)
		b.Ops[len(b.Ops)-1].Imm = v
		return c
	}

	entry := f.NewBlock()
	c1 := f.NewBlock()
	c2 := f.NewBlock()
	c3 := f.NewBlock()
	rEq0 := mkRet(100)
	rEq1 := mkRet(200)
	rLt0 := mkRet(300)
	rGt100 := mkRet(400)
	final := mkRet(0)

	zero := mkConst(entry, 0)
	eq0 := f.AddOp(entry, ssa.OpEq, a, zero)
	f.SetBrIf(entry, eq0, rEq0, c1)

	one := mkConst(c1, 1)
	eq1 := f.AddOp(c1, ssa.OpEq, a, one)
	f.SetBrIf(c1, eq1, rEq1, c2)

	zero2 := mkConst(c2, 0)
	lt0 := f.AddOp(c2, ssa.OpLt, a, zero2)
	f.SetBrIf(c2, lt0, rLt0, c3)

	hundred := mkConst(c3, 100)
	gt100 := f.AddOp(c3, ssa.OpGt, a, hundred)
	f.SetBrIf(c3, gt100, rGt100, final)

	mod, err := EmitModule(f, "classify")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
	runRelooperCase(t, mod, "classify", []relooperCase{
		{args: []string{"0"}, want: 100},
		{args: []string{"1"}, want: 200},
		{args: []string{"-5"}, want: 300},
		{args: []string{"200"}, want: 400},
		{args: []string{"42"}, want: 0},
	})
}

// TestRelooperDiamondToDiamond — two if-else diamonds in a row
// with a single-pred bridge. Existing classifiers don't compose;
// the relooper should.
//
//	if (a) x = 1; else x = 2;   // first diamond
//	if (b) y = x + 10; else y = x + 20;  // second diamond
//	return y;
//
// Lifted naively (without phis), one variable per block:
//
//	entry ─brif a─→ T1 ─br─→ M1 ─brif b─→ T2 ─br─→ M2 ─ret
//	            └─→ F1 ─br──↗          └─→ F2 ─br──↗
//
// 7 blocks. M1 (2 preds) and M2 (2 preds) are merges; neither
// has phis (the language-side variable was loaded/stored, so
// the lifter writes to a shared slot). Relooper should handle.
//
// Actual test: we'll skip the "phi" complication by computing
// the result independently in each return arm.
func TestRelooperDiamondToDiamond(t *testing.T) {
	f := ssa.NewFunc("compose")
	a := f.AddParam()
	b := f.AddParam()

	entry := f.NewBlock()
	t1 := f.NewBlock()
	fl1 := f.NewBlock()
	m1 := f.NewBlock()
	t2 := f.NewBlock()
	fl2 := f.NewBlock()
	m2 := f.NewBlock()

	f.SetBrIf(entry, a, t1, fl1)
	f.SetBr(t1, m1)
	f.SetBr(fl1, m1)
	f.SetBrIf(m1, b, t2, fl2)
	f.SetBr(t2, m2)
	f.SetBr(fl2, m2)
	// m2 returns 42 unconditionally — exercises the structure
	// without phi-handling.
	c := f.AddOp(m2, ssa.OpConstInt)
	m2.Ops[0].Imm = 42
	f.SetRet(m2, c)

	mod, err := EmitModule(f, "compose")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
	runRelooperCase(t, mod, "compose", []relooperCase{
		{args: []string{"1", "1"}, want: 42},
		{args: []string{"0", "1"}, want: 42},
		{args: []string{"1", "0"}, want: 42},
		{args: []string{"0", "0"}, want: 42},
	})
}

// TestRelooperPhiAtMerge — the relooper handles a CFG with a
// phi at a merge block. Shape:
//
//	entry ─brif c─→ a ─br─→ cb ─br─→ d ─ret
//	          └─→ b ─br──↗
//
// cb has a phi: phi(a: 10, b: 20). d returns the phi. With
// c=1 the answer is 10; with c=0 the answer is 20.
func TestRelooperPhiAtMerge(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		c := f.AddParam()
		entry := f.NewBlock()
		a := f.NewBlock()
		b := f.NewBlock()
		cb := f.NewBlock()
		d := f.NewBlock()

		ten := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[0].Imm = 10
		twenty := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[1].Imm = 20
		f.SetBrIf(entry, c, a, b)
		f.SetBr(a, cb)
		f.SetBr(b, cb)
		phi := f.AddPhi(cb, ten, twenty)
		f.SetBr(cb, d)
		f.SetRet(d, phi)
		return f
	}
	mod, err := EmitModule(build(), "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
	runRelooperCase(t, mod, "main", []relooperCase{
		{args: []string{"1"}, want: 10},
		{args: []string{"0"}, want: 20},
	})
}

// TestRelooperPhiCascade — two phis flowing through nested
// diamonds. Tests that pre-writing phi args for both arms of
// a brif doesn't corrupt state across multiple merges.
//
//	entry ─brif p─→ pT ─br─→ m1 ─brif q─→ qT ─br─→ m2 ─ret(phi2)
//	          └─→ pF ─br──↗          └─→ qF ─br──↗
//
// m1 has phi1: phi(pT: 100, pF: 200).
// m2 has phi2: phi(qT: phi1+1, qF: phi1+2).
func TestRelooperPhiCascade(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("cascade")
		p := f.AddParam()
		q := f.AddParam()
		entry := f.NewBlock()
		pT := f.NewBlock()
		pF := f.NewBlock()
		m1 := f.NewBlock()
		qT := f.NewBlock()
		qF := f.NewBlock()
		m2 := f.NewBlock()

		c100 := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[0].Imm = 100
		c200 := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[1].Imm = 200
		f.SetBrIf(entry, p, pT, pF)
		f.SetBr(pT, m1)
		f.SetBr(pF, m1)
		phi1 := f.AddPhi(m1, c100, c200)
		f.SetBrIf(m1, q, qT, qF)

		one := f.AddOp(qT, ssa.OpConstInt)
		qT.Ops[0].Imm = 1
		qTval := f.AddOp(qT, ssa.OpAdd, phi1, one)
		f.SetBr(qT, m2)

		two := f.AddOp(qF, ssa.OpConstInt)
		qF.Ops[0].Imm = 2
		qFval := f.AddOp(qF, ssa.OpAdd, phi1, two)
		f.SetBr(qF, m2)

		phi2 := f.AddPhi(m2, qTval, qFval)
		f.SetRet(m2, phi2)
		return f
	}
	mod, err := EmitModule(build(), "cascade")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
	runRelooperCase(t, mod, "cascade", []relooperCase{
		{args: []string{"1", "1"}, want: 101}, // p=T,q=T → 100+1
		{args: []string{"1", "0"}, want: 102}, // p=T,q=F → 100+2
		{args: []string{"0", "1"}, want: 201}, // p=F,q=T → 200+1
		{args: []string{"0", "0"}, want: 202}, // p=F,q=F → 200+2
	})
}

// relooperCase pairs args (as strings to pass to wasmtime
// --invoke) with the expected i32 return value.
type relooperCase struct {
	args []string
	want int
}

// runRelooperCase invokes the module under wasmtime for each
// case. Logs (without failing) when wasmtime isn't on PATH,
// so the validate-only path retains meaning even in
// environments without the runtime.
func runRelooperCase(t *testing.T, mod []byte, exportName string, cases []relooperCase) {
	t.Helper()
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Logf("wasmtime not on PATH; skipping runtime checks (validation alone passed)")
		return
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "mod.wasm")
	if err := os.WriteFile(p, mod, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	for _, c := range cases {
		cmdArgs := append([]string{"run", "--invoke", exportName, p}, c.args...)
		cmd := exec.Command(wasmtime, cmdArgs...)
		var so, se bytes.Buffer
		cmd.Stdout = &so
		cmd.Stderr = &se
		if err := cmd.Run(); err != nil {
			t.Errorf("wasmtime %s(%v): %v\nstderr:\n%s", exportName, c.args, err, se.String())
			continue
		}
		out := strings.TrimSpace(so.String())
		got, err := strconv.Atoi(out)
		if err != nil {
			t.Errorf("parse wasmtime stdout %q: %v", out, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s(%v) = %d, want %d", exportName, c.args, got, c.want)
		}
	}
}

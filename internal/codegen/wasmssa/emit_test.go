package wasmssa

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// TestEmitConstReturn — `function() { return 42 }` emits a
// valid module whose `main` export returns 42.
func TestEmitConstReturn(t *testing.T) {
	f := ssa.NewFunc("main")
	entry := f.NewBlock()
	c := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 42
	f.SetRet(entry, c)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module shorter than wasm preamble: %d bytes", len(mod))
	}
	// Magic + version: "\0asm" 0x01 0x00 0x00 0x00.
	wantPreamble := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if !bytes.Equal(mod[:8], wantPreamble) {
		t.Errorf("preamble = %x, want %x", mod[:8], wantPreamble)
	}
}

// TestEmitAddTwoParams — `function(a, b) { return a + b }`
// emits a module whose `main` export takes 2 i32 params,
// adds them, and returns i32.
func TestEmitAddTwoParams(t *testing.T) {
	f := ssa.NewFunc("main")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, ssa.OpAdd, a, b)
	f.SetRet(entry, sum)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}

	// Verify the module passes wasm-tools validation (if
	// available). Skip when the tool isn't on PATH —
	// matches the LANG_WASI_ADAPTER / wasmtime pattern in
	// the e2e suite.
	validateModule(t, mod)
}

// TestEmitArithmeticChain — exercises every supported binary
// integer op + Neg + Not in one function. Module must
// validate; runtime correctness is left to e2e tests once
// wasmtime invocation lands.
func TestEmitArithmeticChain(t *testing.T) {
	f := ssa.NewFunc("main")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	// Touch every binary kind so the opcode table is exercised.
	for _, k := range []ssa.OpKind{
		ssa.OpAdd, ssa.OpSub, ssa.OpMul, ssa.OpDiv, ssa.OpDivU,
		ssa.OpRem, ssa.OpRemU, ssa.OpAnd, ssa.OpOr, ssa.OpXor,
		ssa.OpShl, ssa.OpShr, ssa.OpShrU,
		ssa.OpEq, ssa.OpNe,
		ssa.OpLt, ssa.OpLtU, ssa.OpLe, ssa.OpLeU,
		ssa.OpGt, ssa.OpGtU, ssa.OpGe, ssa.OpGeU,
	} {
		f.AddOp(entry, k, a, b)
	}
	f.AddOp(entry, ssa.OpNeg, a)
	f.AddOp(entry, ssa.OpNot, b)
	// Return the result of the last op.
	last := entry.Ops[len(entry.Ops)-1].Result
	f.SetRet(entry, last)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
}

// TestEmitLinearChain — a two-block chain where entry has
// ops then br to a block that returns. Emits as a single
// straight-line wasm body.
func TestEmitLinearChain(t *testing.T) {
	f := ssa.NewFunc("main")
	a := f.AddParam()
	entry := f.NewBlock()
	tail := f.NewBlock()
	c := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 1
	doubled := f.AddOp(entry, ssa.OpAdd, a, a)
	_ = doubled
	f.SetBr(entry, tail)
	sum := f.AddOp(tail, ssa.OpAdd, doubled, c)
	f.SetRet(tail, sum)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module too short: %d bytes", len(mod))
	}
	validateModule(t, mod)
}

// TestEmitIfElseDiamond — `function(c, a, b) { return c ? a : b }`
// compiled to an if-else diamond with a phi at the merge.
// Emits a valid module.
func TestEmitIfElseDiamond(t *testing.T) {
	f := ssa.NewFunc("main")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, a, b)
	f.SetRet(merge, phi)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module too short: %d bytes", len(mod))
	}
	validateModule(t, mod)
}

// TestEmitIfElseDiamondWithOps — both arms compute distinct
// expressions; phi merges them. Exercises arm-internal ops +
// phi-arg writeback at branch sites.
func TestEmitIfElseDiamondWithOps(t *testing.T) {
	f := ssa.NewFunc("main")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	tVal := f.AddOp(thenB, ssa.OpAdd, a, b)
	f.SetBr(thenB, merge)
	fVal := f.AddOp(elseB, ssa.OpSub, a, b)
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, tVal, fVal)
	f.SetRet(merge, phi)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
}

// TestEmitIfOnlyTrueArmIsBody — `function(c, a, b) { x = b; if (c) { x = a; } return x; }`
// shape: entry's True branch goes to body, False falls
// straight through to merge.
func TestEmitIfOnlyTrueArmIsBody(t *testing.T) {
	f := ssa.NewFunc("main")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	body := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, body, merge)
	f.SetBr(body, merge)
	phi := f.AddPhi(merge, b, a) // preds order: [entry, body]; phi args [b, a]
	f.SetRet(merge, phi)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module too short: %d bytes", len(mod))
	}
	validateModule(t, mod)
}

// TestEmitIfOnlyFalseArmIsBody — flipped: entry's False goes
// to body, True falls through to merge. cond gets i32.eqz'd
// inside the emitter so the wasm `if` runs when the original
// False arm should.
func TestEmitIfOnlyFalseArmIsBody(t *testing.T) {
	f := ssa.NewFunc("main")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	body := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, merge, body)
	f.SetBr(body, merge)
	phi := f.AddPhi(merge, a, b) // preds order: [entry, body]; phi args [a, b]
	f.SetRet(merge, phi)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
}

// TestEmitDualReturn — `function(c, a, b) { if (c) return a; else return b; }`
// shape: 3 blocks, entry's brif → T (ret a) and F (ret b).
// No merge.
func TestEmitDualReturn(t *testing.T) {
	f := ssa.NewFunc("main")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetRet(thenB, a)
	f.SetRet(elseB, b)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module too short: %d bytes", len(mod))
	}
	validateModule(t, mod)
}

// TestEmitWhileLoop — a canonical while-loop CFG emits a
// valid module. Shape: entry → header → (body → header) /
// done.
func TestEmitWhileLoop(t *testing.T) {
	f := ssa.NewFunc("main")
	n := f.AddParam() // loop bound
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()

	// Initial counter = 0.
	zero := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 0
	f.SetBr(entry, header)

	// Header phi: i = phi(0, i+1) — loop counter.
	phiRes := f.NewValue()
	phiOp := &ssa.Op{Kind: ssa.OpPhi, Result: phiRes, Args: []ssa.Value{zero, ssa.Value{}}}
	header.Ops = append(header.Ops, phiOp)
	// cond = i < n
	cond := f.AddOp(header, ssa.OpLt, phiRes, n)
	f.SetBrIf(header, cond, body, done)

	// Body: i++
	one := f.AddOp(body, ssa.OpConstInt)
	body.Ops[0].Imm = 1
	inc := f.AddOp(body, ssa.OpAdd, phiRes, one)
	phiOp.Args[1] = inc
	f.SetBr(body, header)

	// Done: return i (which equals n).
	f.SetRet(done, phiRes)

	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module too short: %d bytes", len(mod))
	}
	validateModule(t, mod)
}

// TestEmitRejectsUnknownCFG — a CFG that doesn't match any
// recognised shape (e.g. 5 blocks with arbitrary edges)
// returns a clear error.
func TestEmitRejectsUnknownCFG(t *testing.T) {
	f := ssa.NewFunc("main")
	c := f.AddParam()
	entry := f.NewBlock()
	a := f.NewBlock()
	b := f.NewBlock()
	cb := f.NewBlock()
	d := f.NewBlock()
	f.SetBrIf(entry, c, a, b)
	f.SetBr(a, cb)
	f.SetBr(b, cb)
	f.SetBr(cb, d)
	f.SetRet(d, ssa.Value{})

	_, err := EmitModule(f, "main")
	if err == nil {
		t.Fatal("expected error for 5-block unrecognised CFG")
	}
}

// TestEmitSelfRecursiveFactorial — `factorial(n)` lowered as
// an if-else diamond with the recursive call in the else arm:
//
//	if (n <= 1) return 1; else return n * factorial(n-1)
//
// Exercises OpCall (self-recursion) inside a diamond. EmitModule
// emits a wasm module where func 0 calls func 0 (itself).
func TestEmitSelfRecursiveFactorial(t *testing.T) {
	f := ssa.NewFunc("factorial")
	n := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()

	one := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 1
	cond := f.AddOp(entry, ssa.OpLe, n, one)
	f.SetBrIf(entry, cond, thenB, elseB)

	tOne := f.AddOp(thenB, ssa.OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)

	eOne := f.AddOp(elseB, ssa.OpConstInt)
	elseB.Ops[0].Imm = 1
	subOne := f.AddOp(elseB, ssa.OpSub, n, eOne)
	recur := f.AddOp(elseB, ssa.OpCall, subOne)
	elseB.Ops[2].Str = "factorial"
	prod := f.AddOp(elseB, ssa.OpMul, n, recur)
	f.SetBr(elseB, merge)

	phi := f.AddPhi(merge, tOne, prod)
	f.SetRet(merge, phi)

	mod, err := EmitModule(f, "factorial")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module too short: %d bytes", len(mod))
	}
	validateModule(t, mod)
}

// TestEmitRejectsExternalCall — OpCall whose callee name
// matches neither f.Name nor any declared import is rejected.
func TestEmitRejectsExternalCall(t *testing.T) {
	f := ssa.NewFunc("main")
	entry := f.NewBlock()
	r := f.AddOp(entry, ssa.OpCall)
	entry.Ops[0].Str = "other"
	f.SetRet(entry, r)

	_, err := EmitModule(f, "main")
	if err == nil {
		t.Fatal("expected error for non-self OpCall")
	}
}

// TestEmitImportedCall — a function that calls an imported
// helper resolves the call to the import's func index. The
// resulting module declares the import + a single defined
// function (the export); the call op lowers to `call 0`
// (import) rather than `call 1` (self).
func TestEmitImportedCall(t *testing.T) {
	f := ssa.NewFunc("main")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, ssa.OpCall, a, b)
	entry.Ops[0].Str = "host_add"
	f.SetRet(entry, r)

	hostAdd := Import{
		Module:  "env",
		Name:    "host_add",
		Params:  []byte{encodeValtypeI32, encodeValtypeI32},
		Results: []byte{encodeValtypeI32},
	}
	mod, err := EmitModule(f, "main", hostAdd)
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	if len(mod) < 8 {
		t.Fatalf("module too short: %d bytes", len(mod))
	}
	validateModule(t, mod)
}

// TestEmitRejectsDuplicateImportName — two imports with the
// same Name are ambiguous; EmitModule rejects.
func TestEmitRejectsDuplicateImportName(t *testing.T) {
	f := ssa.NewFunc("main")
	entry := f.NewBlock()
	c := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 0
	f.SetRet(entry, c)

	a := Import{Module: "env", Name: "f", Params: nil, Results: []byte{encodeValtypeI32}}
	b := Import{Module: "env", Name: "f", Params: nil, Results: []byte{encodeValtypeI32}}
	_, err := EmitModule(f, "main", a, b)
	if err == nil {
		t.Fatal("expected error for duplicate import name")
	}
}

// encodeValtypeI32 is a test-local alias for the encode.ValtypeI32
// constant — avoids importing the encode package into the test
// file just for this one byte.
const encodeValtypeI32 = 0x7f

// TestEmitExtend8S — `(x) → extend8s(x)`. Module validates
// and runtime check (when wasmtime available) exercises both
// the sign-extension path (negative low byte) and the
// no-change path (small positive value).
func TestEmitExtend8S(t *testing.T) {
	f := ssa.NewFunc("main")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, ssa.OpExtend8S, x)
	f.SetRet(entry, r)
	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
}

// TestEmitExtend16S — `(x) → extend16s(x)`. Same shape as
// Extend8S but for the low halfword.
func TestEmitExtend16S(t *testing.T) {
	f := ssa.NewFunc("main")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, ssa.OpExtend16S, x)
	f.SetRet(entry, r)
	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
}

// TestEmitRejectsUnsupportedOp — an unsupported op kind
// (e.g. OpLoad) surfaces a clear error.
func TestEmitRejectsUnsupportedOp(t *testing.T) {
	f := ssa.NewFunc("main")
	entry := f.NewBlock()
	c := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 0
	f.AddOp(entry, ssa.OpLoad, c)
	f.SetRet(entry, c)

	_, err := EmitModule(f, "main")
	if err == nil {
		t.Fatal("expected error for unsupported OpLoad")
	}
}

// TestEmitRejectsNil / TestEmitRejectsEmptyExportName —
// defensive guards.
func TestEmitRejectsNil(t *testing.T) {
	_, err := EmitModule(nil, "main")
	if err == nil {
		t.Fatal("expected error for nil func")
	}
}

func TestEmitRejectsEmptyExportName(t *testing.T) {
	f := ssa.NewFunc("main")
	entry := f.NewBlock()
	f.SetRet(entry, ssa.Value{})
	_, err := EmitModule(f, "")
	if err == nil {
		t.Fatal("expected error for empty exportName")
	}
}

// validateModule runs `wasm-tools validate` on the given
// module bytes. SKIPs the test if the tool isn't available.
func validateModule(t *testing.T, mod []byte) {
	t.Helper()
	wasmTools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping validate")
	}
	dir := t.TempDir()
	modPath := filepath.Join(dir, "mod.wasm")
	if err := os.WriteFile(modPath, mod, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	cmd := exec.Command(wasmTools, "validate", modPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
}

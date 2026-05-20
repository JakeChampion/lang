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

// TestEmitRejectsMultiBlock — multi-block functions aren't
// supported yet; EmitModule returns a clear error.
func TestEmitRejectsMultiBlock(t *testing.T) {
	f := ssa.NewFunc("main")
	a := f.AddParam()
	entry := f.NewBlock()
	mid := f.NewBlock()
	f.SetBr(entry, mid)
	f.SetRet(mid, a)

	_, err := EmitModule(f, "main")
	if err == nil {
		t.Fatal("expected error for multi-block function")
	}
}

// TestEmitRejectsUnsupportedOp — an unsupported op kind
// (e.g. OpCall) surfaces a clear error.
func TestEmitRejectsUnsupportedOp(t *testing.T) {
	f := ssa.NewFunc("main")
	entry := f.NewBlock()
	c := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 0
	f.AddOp(entry, ssa.OpCall)
	f.SetRet(entry, c)

	_, err := EmitModule(f, "main")
	if err == nil {
		t.Fatal("expected error for unsupported OpCall")
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

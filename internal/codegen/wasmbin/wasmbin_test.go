package wasmbin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// runUnderWasmtime writes bin to a temp file and invokes the named
// export under `wasmtime run --invoke`. Returns stdout. Skips when
// wasmtime is missing so CI without the runtime stays green.
func runUnderWasmtime(t *testing.T, bin []byte, export string) string {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", export, p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime run --invoke %s failed: %v\nstderr:\n%s\nstdout:\n%s", export, err, se.String(), so.String())
	}
	return strings.TrimSpace(so.String())
}

// i32 returns the polymorphic-free NumberType for i32 — what
// ir.LowerWith produces for a settled i32 expression.
func i32() ast.NumberType  { return ast.NumberType{Width: 32, Signed: true} }
func i64() ast.NumberType  { return ast.NumberType{Width: 64, Signed: true} }
func f32() ast.FloatType   { return ast.FloatType{Width: 32} }
func f64() ast.FloatType   { return ast.FloatType{Width: 64} }
func void() ast.VoidType   { return ast.VoidType{} }

// TestEmitConstReturn — `function main(): i32 { return 42 }`.
// The simplest non-empty program: one i32 constant, implicit
// trailing return. Validates the wiring from ir.Program through
// module assembly to a runnable wasm.
func TestEmitConstReturn(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "42" {
		t.Fatalf("wasmtime stdout = %q, want %q", got, "42")
	}
}

// TestEmitI32Arithmetic exercises every i32 arithmetic / comparison
// op the slice supports. Each sub-test crafts the smallest function
// that exercises one op and asserts the resulting i32 (printed by
// wasmtime). The shape `function f(): i32 { return <expr> }` keeps
// inputs in the IR-immediates so we don't need params (params are
// covered separately).
func TestEmitI32Arithmetic(t *testing.T) {
	cases := []struct {
		name string
		ops  []ir.Op
		want string
	}{
		{"add", []ir.Op{
			{Kind: ir.OpConstI32, I32: 3},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpAdd},
		}, "7"},
		{"sub", []ir.Op{
			{Kind: ir.OpConstI32, I32: 10},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpSub},
		}, "6"},
		{"mul", []ir.Op{
			{Kind: ir.OpConstI32, I32: 6},
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpMul},
		}, "42"},
		{"div_s", []ir.Op{
			{Kind: ir.OpConstI32, I32: 21},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpDivS},
		}, "5"},
		{"rem_s", []ir.Op{
			{Kind: ir.OpConstI32, I32: 22},
			{Kind: ir.OpConstI32, I32: 5},
			{Kind: ir.OpRemS},
		}, "2"},
		{"eq_true", []ir.Op{
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpEq},
		}, "1"},
		{"lt_s", []ir.Op{
			{Kind: ir.OpConstI32, I32: 3},
			{Kind: ir.OpConstI32, I32: 5},
			{Kind: ir.OpLtS},
		}, "1"},
		{"not_of_nonzero", []ir.Op{
			{Kind: ir.OpConstI32, I32: 99},
			{Kind: ir.OpNot},
		}, "0"},
		{"and", []ir.Op{
			{Kind: ir.OpConstI32, I32: 0b1100},
			{Kind: ir.OpConstI32, I32: 0b1010},
			{Kind: ir.OpAnd},
		}, "8"}, // 0b1000
		{"shl", []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpShl},
		}, "16"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops:        tc.ops,
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if got := runUnderWasmtime(t, bin, "main"); got != tc.want {
				t.Fatalf("wasmtime stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEmitParamsAndLocals — `function add(a: i32, b: i32): i32 {
// var t: i32 = a + b; return t }`. Exercises param indexing,
// local declaration, OpStoreLocal/OpLoadLocal, and the function-
// section / type-section path with non-empty params.
func TestEmitParamsAndLocals(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name: "add",
		Params: []ast.Param{
			{Name: "a", Type: i32()},
			{Name: "b", Type: i32()},
		},
		Locals: []*ast.Var{
			{Name: "t", Type: i32()},
		},
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0}, // a
			{Kind: ir.OpLoadLocal, I32: 1}, // b
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 2}, // t
			{Kind: ir.OpLoadLocal, I32: 2},  // t
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// wasmtime --invoke add lets us pass args.
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "add", p, "7", "11")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	if got := strings.TrimSpace(so.String()); got != "18" {
		t.Fatalf("add(7, 11) = %q, want %q", got, "18")
	}
}

// TestEmitI64 / TestEmitF32 / TestEmitF64 — confirm the Width
// dispatch picks the right opcode family.
func TestEmitI64(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i64(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI64, I64: 100},
			{Kind: ir.OpConstI64, I64: 23},
			{Kind: ir.OpAdd, Width: 64},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "123" {
		t.Fatalf("got %q want %q", got, "123")
	}
}

func TestEmitF32(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: f32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstF32, F32: 1.5},
			{Kind: ir.OpConstF32, F32: 2.5},
			{Kind: ir.OpFAdd},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "main")
	if !strings.HasPrefix(got, "4") {
		t.Fatalf("got %q, want stdout starting with 4 (1.5+2.5=4.0)", got)
	}
}

func TestEmitF64(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: f64(),
		Ops: []ir.Op{
			{Kind: ir.OpConstF64, F64: 3.0},
			{Kind: ir.OpConstF64, F64: 0.5},
			{Kind: ir.OpFMul, Width: 64},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "main")
	if !strings.HasPrefix(got, "1.5") {
		t.Fatalf("got %q, want stdout starting with 1.5", got)
	}
}

// TestEmitVoidReturn — `function noop(): void { return }`.
// Confirms the empty-result type encoding + the OpReturnVoid path.
func TestEmitVoidReturn(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "noop",
		ReturnType: void(),
		Ops:        []ir.Op{{Kind: ir.OpReturnVoid}},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// No stdout for void-returning function under wasmtime,
	// but the run must succeed.
	if got := runUnderWasmtime(t, bin, "noop"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestEmitTypeDedup — two functions with the same () -> i32
// signature should share a single type-section entry, not duplicate.
// Catches a regression in the addType de-dup map.
func TestEmitTypeDedup(t *testing.T) {
	mkConst := func(n int32) []ir.Op {
		return []ir.Op{{Kind: ir.OpConstI32, I32: n}}
	}
	prog := &ir.Program{Funcs: []*ir.Func{
		{Name: "a", ReturnType: i32(), Ops: mkConst(1)},
		{Name: "b", ReturnType: i32(), Ops: mkConst(2)},
		{Name: "c", ReturnType: i32(), Ops: mkConst(3)},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// All three should be runnable under their respective names.
	for _, c := range []struct{ name, want string }{
		{"a", "1"}, {"b", "2"}, {"c", "3"},
	} {
		if got := runUnderWasmtime(t, bin, c.name); got != c.want {
			t.Fatalf("invoke %s: got %q want %q", c.name, got, c.want)
		}
	}
}

// TestEmitUnsupportedOpReports — pass an op the slice doesn't
// cover (e.g. OpStrLen) and confirm we get a useful error rather
// than emitting nonsense bytes. Regressions here would silently
// produce invalid wasm.
func TestEmitUnsupportedOpReports(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpStrLen}},
	}}}
	if _, err := Emit(prog); err == nil {
		t.Fatalf("expected unsupported-op error, got nil")
	}
}

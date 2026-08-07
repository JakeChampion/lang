package wasmbin

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
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
func i32() ast.NumberType { return ast.NumberType{Width: 32, Signed: true} }
func i64() ast.NumberType { return ast.NumberType{Width: 64, Signed: true} }
func f32() ast.FloatType  { return ast.FloatType{Width: 32} }
func f64() ast.FloatType  { return ast.FloatType{Width: 64} }
func void() ast.VoidType  { return ast.VoidType{} }

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

// TestEmitTypeDedupF64NoSeparatorCollision is a regression for the addType
// dedup key. It once joined the param and result valtype bytes with a literal
// '|' separator — but '|' is 0x7c, which is ALSO the f64 valtype byte. So
// `() -> (f64)` (key "" | "\x7c") and `(f64) -> ()` (key "\x7c" | "") both
// hashed to "\x7c\x7c" and merged into one type entry. Whichever was emitted
// first won, leaving the other function referencing a type that disagreed with
// its body — a module that fails wasm validation at instantiation. The two
// shapes must stay distinct.
func TestEmitTypeDedupF64NoSeparatorCollision(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		// () -> (f64): emitted first, so a collision would make THIS the shared
		// (wrong) type for sink below.
		{Name: "give", ReturnType: f64(), Ops: []ir.Op{{Kind: ir.OpConstF64, F64: 3.5}}},
		// (f64) -> (): a void function with one f64 param. Under the old key it
		// would inherit give's () -> (f64) type and fail validation (param count
		// + result mismatch against its body).
		{Name: "sink", Params: []ast.Param{{Name: "x", Type: f64()}}, ReturnType: void(), Ops: []ir.Op{{Kind: ir.OpReturnVoid}}},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Instantiating the module validates every function's body against its
	// declared type; a dedup collision would corrupt sink's type and fail here.
	if got := runUnderWasmtime(t, bin, "give"); !strings.HasPrefix(got, "3.5") {
		t.Fatalf("invoke give: got %q, want 3.5 (a type-dedup collision would fail module validation)", got)
	}
}

//	TestEmitIfElse — `function pick(a, b: i32): i32 {
//	  if a > b { return a } else { return b }
//	}`. Uses `if (result i32)` with branches that push the chosen
//
// value. Exercises OpIf / OpElse / OpEnd + a result-typed block.
func TestEmitIfElse(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name: "pick",
		Params: []ast.Param{
			{Name: "a", Type: i32()},
			{Name: "b", Type: i32()},
		},
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0}, // a
			{Kind: ir.OpLoadLocal, I32: 1}, // b
			{Kind: ir.OpGtS},
			{Kind: ir.OpIf, I32: ir.BlockTypeI32},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpElse},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpEnd},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, c := range []struct {
		args     []string
		expected string
	}{
		{[]string{"7", "11"}, "11"},
		{[]string{"42", "1"}, "42"},
		{[]string{"5", "5"}, "5"}, // gt_s false → else branch
	} {
		cmd := exec.Command("wasmtime", append([]string{"run", "--invoke", "pick", p}, c.args...)...)
		var so, se bytes.Buffer
		cmd.Stdout = &so
		cmd.Stderr = &se
		if err := cmd.Run(); err != nil {
			t.Fatalf("pick%v: %v\nstderr:%s", c.args, err, se.String())
		}
		if got := strings.TrimSpace(so.String()); got != c.expected {
			t.Fatalf("pick%v = %q, want %q", c.args, got, c.expected)
		}
	}
}

//	TestEmitLoopBr — `function sum(n: i32): i32 {
//	  var acc = 0; var i = 0;
//	  loop {
//	    if !(i < n) break;
//	    acc = acc + i;
//	    i = i + 1;
//	    continue;
//	  }
//	  return acc;
//	}` — using the wasm block+loop+br_if idiom. Exercises OpBlock,
//
// OpLoop, OpBr (back-edge), OpBrIf (forward exit).
//
// Shape (label depths in parens):
//
//	block (void)         ; label 1 (forward exit)
//	  loop (void)         ; label 0 (back-edge target)
//	    i < n  →  br_if to label 0 if FALSE? No — we want:
//	    i >= n →  br to label 1 (exit outer)
//	  end loop
//	end block
//
// Concretely:
//
//	block
//	  loop
//	    local.get i
//	    local.get n
//	    i32.ge_s
//	    br_if 1            ; if i >= n, exit outer block
//	    local.get acc
//	    local.get i
//	    i32.add
//	    local.set acc
//	    local.get i
//	    i32.const 1
//	    i32.add
//	    local.set i
//	    br 0               ; loop back
//	  end
//	end
//	local.get acc
func TestEmitLoopBr(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "sum",
		Params:     []ast.Param{{Name: "n", Type: i32()}},
		Locals:     []*ast.Var{{Name: "acc", Type: i32()}, {Name: "i", Type: i32()}},
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			{Kind: ir.OpLoadLocal, I32: 2}, // i
			{Kind: ir.OpLoadLocal, I32: 0}, // n
			{Kind: ir.OpGeS},
			{Kind: ir.OpBrIf, I32: 1},      // exit outer block
			{Kind: ir.OpLoadLocal, I32: 1}, // acc
			{Kind: ir.OpLoadLocal, I32: 2}, // i
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 1}, // acc
			{Kind: ir.OpLoadLocal, I32: 2},  // i
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 2}, // i
			{Kind: ir.OpBr, I32: 0},         // back-edge to loop
			{Kind: ir.OpEnd},                // end loop
			{Kind: ir.OpEnd},                // end block
			{Kind: ir.OpLoadLocal, I32: 1},  // acc
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// sum(10) = 0+1+2+...+9 = 45
	cmd := exec.Command("wasmtime", "run", "--invoke", "sum", p, "10")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("sum(10): %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "45" {
		t.Fatalf("sum(10) = %q, want %q", got, "45")
	}
}

// TestEmitBlocktypeStringPair — block with the BlockTypeStringPair
// multi-value return: produces (i32 data, i32 len) on the stack at
// `end`. The block body must produce two i32s. Returning just the
// len verifies the multi-value blocktype encoded correctly and the
// validator accepts the resulting bytes.
func TestEmitBlocktypeStringPair(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeStringPair},
			{Kind: ir.OpConstI32, I32: 7}, // data
			{Kind: ir.OpConstI32, I32: 5}, // len
			{Kind: ir.OpEnd},
			// Stack: (7, 5). Drop the data, keep len → 5.
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpDrop},
			{Kind: ir.OpLoadLocal, I32: 0},
		},
		ScratchTypes: []ast.Type{i32()},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "5" {
		t.Fatalf("string-pair block result = %q, want 5", got)
	}
}

// TestEmitConversions — width conversions (extend / wrap),
// float↔int (convert / trunc), float demote/promote, and
// reinterpret. One sub-test per family; the inner computation
// builds a value of the source type, runs the conversion, and
// returns the result in a way that lets wasmtime print it
// verifiably.
func TestEmitConversions(t *testing.T) {
	cases := []struct {
		name string
		ret  ast.Type
		ops  []ir.Op
		want string
	}{
		{"extend_i32_s_negative", i64(), []ir.Op{
			{Kind: ir.OpConstI32, I32: -1},
			{Kind: ir.OpExtendI32S},
		}, "-1"},
		{"extend_i32_u_negative", i64(), []ir.Op{
			{Kind: ir.OpConstI32, I32: -1},
			{Kind: ir.OpExtendI32U}, // 0xFFFFFFFF unsigned → 4294967295
		}, "4294967295"},
		{"wrap_i64", i32(), []ir.Op{
			{Kind: ir.OpConstI64, I64: 0x1_0000_0000 + 42}, // high bits dropped
			{Kind: ir.OpWrapI64},
		}, "42"},
		{"trunc_f32_to_i32", i32(), []ir.Op{
			{Kind: ir.OpConstF32, F32: 3.7},
			{Kind: ir.OpITruncF32, Width: 32}, // → i32
		}, "3"},
		{"convert_i32_to_f64", f64(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpFConvertI32, Width: 64}, // → f64
		}, "7"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: tc.ret,
				Ops:        tc.ops,
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if got := runUnderWasmtime(t, bin, "main"); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEmitPromoteDemote — f32 promoted to f64 round-trips through
// f64 arithmetic and then demoted back to f32. Catches drift in
// either direction since the two ops share an opcode family.
func TestEmitPromoteDemote(t *testing.T) {
	// main(): f32 { return f32(f64(1.5) + f64(2.25)) }  // 3.75
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: f32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstF32, F32: 1.5},
			{Kind: ir.OpFPromoteF32},
			{Kind: ir.OpConstF32, F32: 2.25},
			{Kind: ir.OpFPromoteF32},
			{Kind: ir.OpFAdd, Width: 64},
			{Kind: ir.OpFDemoteF64},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "main")
	if !strings.HasPrefix(got, "3.75") {
		t.Fatalf("got %q, want stdout starting with 3.75", got)
	}
}

// TestEmitReinterpret — bits round-trip via f32 ↔ i32. After
// reinterpret(reinterpret(x)) we should get x back unchanged.
// Picks a pattern that's not a normal float to make sure no
// implicit normalisation happens.
func TestEmitReinterpret(t *testing.T) {
	// main(): i32 { return reinterpret_i32(reinterpret_f32(0x12345678)) }
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0x12345678},
			{Kind: ir.OpReinterpretF32I32},
			{Kind: ir.OpReinterpretI32F32},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "305419896" { // 0x12345678
		t.Fatalf("got %q, want 305419896", got)
	}
}

// TestEmitMemoryRoundTrip — store-then-load round-trip across
// every load/store width covered by slice 4. The function takes
// the value as a param, stores it at a fixed address, loads it
// back, returns. Catches drift in alignment, opcode bytes, and
// the memory-section emission.
func TestEmitMemoryRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		paramType ast.Type
		retType   ast.Type
		// Ops body: assume addr is computed inline (0), param at
		// local 0 is the value to write.
		body []ir.Op
		args []string
		want string
	}{
		{"i32_store_load", i32(), i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpStore},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoad},
		}, []string{"12345"}, "12345"},
		{"i64_store_load", i64(), i64(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpStore, Width: 64},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoad, Width: 64},
		}, []string{"9000000000"}, "9000000000"},
		{"f32_store_load", f32(), f32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpFStore},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpFLoad},
		}, []string{"2.5"}, "2.5"},
		{"f64_store_load", f64(), f64(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpFStore, Width: 64},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpFLoad, Width: 64},
		}, []string{"1.25"}, "1.25"},
		{"u8_store_load", i32(), i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpStoreI8},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadByte}, // load8_u
		}, []string{"200"}, "200"}, // 200 fits unsigned byte
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "rt",
				Params:     []ast.Param{{Name: "v", Type: tc.paramType}},
				ReturnType: tc.retType,
				Ops:        tc.body,
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			dir := t.TempDir()
			p := filepath.Join(dir, "prog.wasm")
			if err := os.WriteFile(p, bin, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			cmd := exec.Command("wasmtime", append([]string{"run", "--invoke", "rt", p}, tc.args...)...)
			var so, se bytes.Buffer
			cmd.Stdout = &so
			cmd.Stderr = &se
			if err := cmd.Run(); err != nil {
				t.Fatalf("rt%v: %v\nstderr:%s", tc.args, err, se.String())
			}
			got := strings.TrimSpace(so.String())
			if got != tc.want {
				t.Fatalf("rt%v = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestMemorySectionOnlyWhenUsed — a module with no memory ops
// should NOT include a memory section. Tests the anyMemoryOp
// gate; otherwise downstream tooling that inspects sections
// would see a phantom memory.
func TestMemorySectionOnlyWhenUsed(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Section IDs after the 8-byte preamble: id 1 (type),
	// id 3 (function), id 7 (export), id 10 (code). Memory
	// would be id 5 — its absence means no 0x05 byte appears
	// at any section-header position.
	for _, b := range bin {
		if b == encode.SectionMemory {
			// Could be a stray data byte, but if id 5 shows up,
			// follow up: walk sections to confirm.
			if walkHasMemorySection(t, bin) {
				t.Fatalf("memory section present in memory-free module")
			}
			return
		}
	}
}

// walkHasMemorySection — scan the module after the 8-byte
// preamble, hopping section headers (1 byte id + uleb size),
// and report whether id 5 (memory) appears as a header.
func walkHasMemorySection(t *testing.T, bin []byte) bool {
	t.Helper()
	if len(bin) < 8 {
		return false
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		// Decode uleb size.
		size := 0
		shift := 0
		for {
			if i >= len(bin) {
				return false
			}
			b := bin[i]
			i++
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if id == encode.SectionMemory {
			return true
		}
		i += size
	}
	return false
}

// TestEmitDirectCall — `function helper(): i32 { return 7 }`
// plus `function main(): i32 { return helper() + 3 }`. Exercises
// OpCallDirect (name resolution → funcidx) and the function-
// section / code-section ordering required for forward references.
func TestEmitDirectCall(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "helper",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 7}},
		},
		{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpCallDirect, Str: "helper"},
				{Kind: ir.OpConstI32, I32: 3},
				{Kind: ir.OpAdd},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "10" {
		t.Fatalf("got %q want %q", got, "10")
	}
}

// TestEmitDirectCallWithArgs — `function add3(a, b, c: i32): i32 {
// return a + b + c }` + `function main(): i32 {
// return add3(10, 20, 12) }`. Exercises arg-passing through a
// direct call.
func TestEmitDirectCallWithArgs(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name: "add3",
			Params: []ast.Param{
				{Name: "a", Type: i32()},
				{Name: "b", Type: i32()},
				{Name: "c", Type: i32()},
			},
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpLoadLocal, I32: 1},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoadLocal, I32: 2},
				{Kind: ir.OpAdd},
			},
		},
		{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 10},
				{Kind: ir.OpConstI32, I32: 20},
				{Kind: ir.OpConstI32, I32: 12},
				{Kind: ir.OpCallDirect, Str: "add3", I32: 3},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "42" {
		t.Fatalf("got %q want %q", got, "42")
	}
}

//	TestEmitRecursion — `function fact(n: i32): i32 {
//	  if n <= 1 { return 1 } else { return n * fact(n - 1) }
//	}`. Direct self-call. Exercises call into the same funcidx,
//
// combined with if/else from the control-flow slice.
func TestEmitRecursion(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "fact",
		Params:     []ast.Param{{Name: "n", Type: i32()}},
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpLeS},
			{Kind: ir.OpIf, I32: ir.BlockTypeI32},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpElse},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpSub},
			{Kind: ir.OpCallDirect, Str: "fact", I32: 1},
			{Kind: ir.OpMul},
			{Kind: ir.OpEnd},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, c := range []struct{ in, want string }{
		{"0", "1"}, {"1", "1"}, {"5", "120"}, {"10", "3628800"},
	} {
		cmd := exec.Command("wasmtime", "run", "--invoke", "fact", p, c.in)
		var so, se bytes.Buffer
		cmd.Stdout = &so
		cmd.Stderr = &se
		if err := cmd.Run(); err != nil {
			t.Fatalf("fact(%s): %v\nstderr:%s", c.in, err, se.String())
		}
		if got := strings.TrimSpace(so.String()); got != c.want {
			t.Fatalf("fact(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEmitCallUnknownCallee — calling a function that isn't in
// the program reports an error rather than emitting an invalid
// funcidx.
func TestEmitCallUnknownCallee(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpCallDirect, Str: "does_not_exist"}},
	}}}
	if _, err := Emit(prog); err == nil {
		t.Fatal("expected unknown-callee error")
	}
}

// TestEmitCallIndirect — two closure-target functions of source
// signature (i32) → i32 in the funcref table. The caller picks a
// target by name via OpConstFunc (producing the closure-pair
// pointer) then dispatches via OpCallIndirect, which derefs the
// pair into (env_ptr, fn_idx) before call_indirect.
//
// `double` and `negate` are closure targets (referenced by
// OpConstFunc), so wasmbin appends env_ptr to their wasm
// signature. Their bodies ignore the unused env_ptr.
func TestEmitCallIndirect(t *testing.T) {
	sigI32I32 := &ast.FuncType{
		Params: []ast.Type{i32()},
		Result: i32(),
	}
	mk := func(name string, body []ir.Op) *ir.Func {
		return &ir.Func{
			Name:       name,
			Params:     []ast.Param{{Name: "x", Type: i32()}},
			ReturnType: i32(),
			Ops:        body,
		}
	}
	prog := &ir.Program{Funcs: []*ir.Func{
		mk("double", []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpMul},
		}),
		mk("negate", []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpSub},
		}),
		{
			Name:       "via_double",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 21},
				{Kind: ir.OpConstFunc, Str: "double"},
				{Kind: ir.OpCallIndirect, Ext: &ir.OpExt{Sig: sigI32I32}},
			},
		},
		{
			Name:       "via_negate",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 100},
				{Kind: ir.OpConstFunc, Str: "negate"},
				{Kind: ir.OpCallIndirect, Ext: &ir.OpExt{Sig: sigI32I32}},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "via_double"); got != "42" {
		t.Fatalf("via_double = %q, want 42", got)
	}
	if got := runUnderWasmtime(t, bin, "via_negate"); got != "-100" {
		t.Fatalf("via_negate = %q, want -100", got)
	}
}

// TestEmitCallClosureDirect — OpCallClosureDirect is the
// defunctionalised form: caller pushes (args..., env_ptr) and
// the callee's wasm signature has env_ptr appended (since the
// callee appears as op.Str of OpCallClosureDirect, it's a
// closure target). Body ignores the unused env_ptr param.
func TestEmitCallClosureDirect(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "doubled",
			Params:     []ast.Param{{Name: "v", Type: i32()}},
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpConstI32, I32: 2},
				{Kind: ir.OpMul},
			},
		},
		{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 21}, // v
				{Kind: ir.OpConstI32, I32: 0},  // env_ptr (unused)
				{Kind: ir.OpCallClosureDirect, Str: "doubled"},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "42" {
		t.Fatalf("got %q, want %q", got, "42")
	}
}

// TestEmitNoTableWhenUnused — confirm the table section stays
// absent for programs that don't use OpCallIndirect / OpConstFunc.
// OpCallClosureDirect alone should not trigger a table.
func TestEmitNoTableWhenUnused(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if walkHasTableSection(t, bin) {
		t.Fatal("table section present in module without indirect calls")
	}
}

// walkHasTableSection — same shape as walkHasMemorySection but
// looking for section id 4.
func walkHasTableSection(t *testing.T, bin []byte) bool {
	t.Helper()
	if len(bin) < 8 {
		return false
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		size := 0
		shift := 0
		for {
			if i >= len(bin) {
				return false
			}
			b := bin[i]
			i++
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if id == encode.SectionTable {
			return true
		}
		i += size
	}
	return false
}

// TestEmitCallIndirectMissingSig — without op.Sig the emitter
// must report an error since there's no way to resolve a typeidx.
func TestEmitCallIndirectMissingSig(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpCallIndirect}}, // no Sig
	}}}
	if _, err := Emit(prog); err == nil {
		t.Fatal("expected missing-Sig error")
	}
}

// TestEmitConstStrHeapForm — string literal >7 bytes goes into
// the data section, OpConstStr pushes (offset, len). Tests both:
// (a) return-the-length: stash len in a scratch local, drop the
// data ptr, push len back. Asserts len == 12 for "hello, world".
// (b) return-the-first-byte: drop len, load8_u from data ptr.
// Asserts byte 0 == 'h' (104).
func TestEmitConstStrHeapForm(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:         "string_len",
			ReturnType:   i32(),
			ScratchTypes: []ast.Type{i32()},
			Ops: []ir.Op{
				{Kind: ir.OpConstStr, Str: "hello, world"},
				{Kind: ir.OpStoreLocal, I32: 0}, // stash len
				{Kind: ir.OpDrop},               // drop data
				{Kind: ir.OpLoadLocal, I32: 0},  // push len back
			},
		},
		{
			Name:       "first_byte",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstStr, Str: "hello, world"},
				{Kind: ir.OpDrop},     // drop len
				{Kind: ir.OpLoadByte}, // i32.load8_u at data ptr
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "string_len"); got != "12" {
		t.Fatalf("string_len = %q, want 12", got)
	}
	if got := runUnderWasmtime(t, bin, "first_byte"); got != "104" {
		t.Fatalf("first_byte = %q, want 104 (ASCII 'h')", got)
	}
}

// TestEmitConstStrLiteralRcSentinel verifies a heap-form string LITERAL
// carries the 0x80000000 rc-sentinel header so __fern_str_dec is a no-op
// on it (literals are immortal data-segment values). `underflow` dec's
// the same literal 8 times and returns __rc_underflow_count(): the
// sentinel short-circuits every dec, so no buffer is freed and the
// over-release counter stays 0. Without the header the dec would misread
// the mid-data-segment bytes at [data-8] as an rc and decrement them,
// eventually tripping the underflow guard / corrupting the segment.
// `survives` dec's the literal then reads its first byte — it must still
// be intact ('h' = 104), proving the dec didn't free / clobber it.
func TestEmitConstStrLiteralRcSentinel(t *testing.T) {
	decOnce := []ir.Op{
		{Kind: ir.OpConstStr, Str: "hello, world"},
		{Kind: ir.OpCallDirect, Str: "__fern_str_dec", I32: 1},
		{Kind: ir.OpDrop},
	}
	var underflowOps []ir.Op
	for i := 0; i < 8; i++ {
		underflowOps = append(underflowOps, decOnce...)
	}
	underflowOps = append(underflowOps,
		ir.Op{Kind: ir.OpCallDirect, Str: "__fern_rc_underflow_count", I32: 0})
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "underflow",
			ReturnType: i32(),
			Ops:        underflowOps,
		},
		{
			Name:       "survives",
			ReturnType: i32(),
			Ops: []ir.Op{
				// dec the literal, then re-load it and read byte 0.
				{Kind: ir.OpConstStr, Str: "hello, world"},
				{Kind: ir.OpCallDirect, Str: "__fern_str_dec", I32: 1},
				{Kind: ir.OpDrop},
				{Kind: ir.OpConstStr, Str: "hello, world"},
				{Kind: ir.OpDrop},     // drop len
				{Kind: ir.OpLoadByte}, // i32.load8_u at data ptr
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "underflow"); got != "0" {
		t.Fatalf("underflow = %q, want 0 (sentinel makes str_dec a no-op on a literal)", got)
	}
	if got := runUnderWasmtime(t, bin, "survives"); got != "104" {
		t.Fatalf("survives first byte = %q, want 104 ('h' — literal intact after dec)", got)
	}
}

// TestEmitConstStrInlineForm — short ASCII string (≤7 bytes) takes
// the inline-form path via fernstring.PackInlineWasm: no data
// section, no memory section. The function drops both pushed
// words and returns 0, which is enough to prove the inline path
// produces a runnable module.
func TestEmitConstStrInlineForm(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hi"},
			{Kind: ir.OpDrop},
			{Kind: ir.OpDrop},
			{Kind: ir.OpConstI32, I32: 0},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if walkHasMemorySection(t, bin) {
		t.Fatal("inline-form-only program should not have a memory section")
	}
	if walkHasDataSection(t, bin) {
		t.Fatal("inline-form-only program should not have a data section")
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "0" {
		t.Fatalf("got %q, want 0", got)
	}
}

// walkHasDataSection — mirror of walkHasMemorySection for id 11.
func walkHasDataSection(t *testing.T, bin []byte) bool {
	t.Helper()
	if len(bin) < 8 {
		return false
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		size := 0
		shift := 0
		for {
			if i >= len(bin) {
				return false
			}
			b := bin[i]
			i++
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if id == encode.SectionData {
			return true
		}
		i += size
	}
	return false
}

// TestEmitConstStrInterning — same heap-form literal from two
// different functions should share a single data-segment entry.
// Walks the data section and asserts exactly one segment whose
// bytes equal the unique literal.
func TestEmitConstStrInterning(t *testing.T) {
	mk := func(name string) *ir.Func {
		return &ir.Func{
			Name:         name,
			ReturnType:   i32(),
			ScratchTypes: []ast.Type{i32()},
			Ops: []ir.Op{
				{Kind: ir.OpConstStr, Str: "shared-literal-string"},
				{Kind: ir.OpStoreLocal, I32: 0},
				{Kind: ir.OpDrop},
				{Kind: ir.OpLoadLocal, I32: 0},
			},
		}
	}
	prog := &ir.Program{Funcs: []*ir.Func{mk("a"), mk("b")}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	segs := dataSegments(t, bin)
	if len(segs) != 1 {
		t.Fatalf("data segments: got %d, want 1", len(segs))
	}
	// Each interned heap-form literal is prefixed with the 8-byte
	// rc-sentinel header (rc=0x80000000 LE, then pad); the single
	// shared segment is that header followed by the unique literal.
	want := append([]byte{0x00, 0x00, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00},
		[]byte("shared-literal-string")...)
	if !bytes.Equal(segs[0], want) {
		t.Fatalf("segment 0: got %q, want sentinel header + %q", segs[0], "shared-literal-string")
	}
	for _, name := range []string{"a", "b"} {
		if got := runUnderWasmtime(t, bin, name); got != "21" {
			t.Fatalf("%s = %q, want 21", name, got)
		}
	}
}

// dataSegments returns the per-segment init bytes of the module's
// data section (empty if no data section is present).
func dataSegments(t *testing.T, bin []byte) [][]byte {
	t.Helper()
	if len(bin) < 8 {
		return nil
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		size := 0
		shift := 0
		for {
			if i >= len(bin) {
				return nil
			}
			b := bin[i]
			i++
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if id == encode.SectionData {
			return parseDataSection(bin[i : i+size])
		}
		i += size
	}
	return nil
}

// parseDataSection decodes the data section body: count uleb +
// per-segment (memidx + i32.const offset + end + len uleb + bytes).
func parseDataSection(body []byte) [][]byte {
	j := 0
	count := 0
	shift := 0
	for {
		b := body[j]
		j++
		count |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
	}
	var segs [][]byte
	for s := 0; s < count; s++ {
		j++ // memidx (1 byte for memidx 0)
		j++ // 0x41 i32.const
		for body[j]&0x80 != 0 {
			j++
		}
		j++ // last sleb byte
		j++ // 0x0b end
		segLen := 0
		shift = 0
		for {
			b := body[j]
			j++
			segLen |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		segs = append(segs, body[j:j+segLen])
		j += segLen
	}
	return segs
}

// strType is a shorthand for the ast.StringType used as a slot type.
func strType() ast.Type { return ast.StringType{} }

// TestEmitStringParam — a function with a string parameter sees
// it as two wasm slots (data, len). The body of takelen drops
// the data and returns the length. Calls into the function with
// a heap-form OpConstStr should produce the literal's length.
func TestEmitStringParam(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			// `function takelen(s: string): i32` — slot 0 is the
			// string param (wasm slots 0+1), slot 1 is a scratch
			// i32 (wasm slot 2) used to stash the len.
			Name:         "takelen",
			Params:       []ast.Param{{Name: "s", Type: strType()}},
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   i32(),
			Ops: []ir.Op{
				{Kind: ir.OpLoadLocal, I32: 0},  // push (data, len)
				{Kind: ir.OpStoreLocal, I32: 1}, // pop len → scratch
				{Kind: ir.OpDrop},               // drop data
				{Kind: ir.OpLoadLocal, I32: 1},  // push len back
			},
		},
		{
			// `function main(): i32 { return takelen("hello world long enough to be heap form") }`
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstStr, Str: "hello world long enough to be heap form"},
				{Kind: ir.OpCallDirect, Str: "takelen", I32: 1},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "39" {
		t.Fatalf("got %q, want 39 (length of literal)", got)
	}
}

// TestEmitStringLocal — declared local of type string: tee it,
// then load + extract len. Exercises OpStoreLocal/OpLoadLocal/
// OpTeeLocal on string slots, plus the local-section preamble
// emitting two valtypes per string slot.
func TestEmitStringLocal(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name: "main",
		Locals: []*ast.Var{
			{Name: "s", Type: strType()},
		},
		ScratchTypes: []ast.Type{i32()}, // for stashing len after load
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "another long-enough string literal"},
			// Tee into slot 0 (string local). Stack stays (data, len).
			{Kind: ir.OpTeeLocal, I32: 0},
			// Stash len.
			{Kind: ir.OpStoreLocal, I32: 1},
			// Drop the data from tee residue.
			{Kind: ir.OpDrop},
			// Load len back.
			{Kind: ir.OpLoadLocal, I32: 1},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "34" {
		t.Fatalf("got %q, want 34 (length of literal)", got)
	}
}

// TestEmitStringReturn — function returning a string returns it
// as a two-value `(i32, i32)` result. The caller receives both
// words on the stack and can extract the len.
func TestEmitStringReturn(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "make_hello",
			ReturnType: strType(),
			Ops: []ir.Op{
				{Kind: ir.OpConstStr, Str: "hello world long enough to heap"},
			},
		},
		{
			// main() calls make_hello, drops data, returns len.
			Name:         "main",
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   i32(),
			Ops: []ir.Op{
				{Kind: ir.OpCallDirect, Str: "make_hello"},
				// Stack now: (data, len). Save len.
				{Kind: ir.OpStoreLocal, I32: 0},
				{Kind: ir.OpDrop}, // data
				{Kind: ir.OpLoadLocal, I32: 0},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "31" {
		t.Fatalf("got %q, want 31", got)
	}
}

// TestSlotIdxMixed — confirms wasm-slot indexing when string and
// non-string slots are interleaved. Layout:
//
//	IR slot 0: i32 param      → wasm slot 0
//	IR slot 1: string param   → wasm slots 1, 2 (data, len)
//	IR slot 2: i32 param      → wasm slot 3
//	IR slot 3: string local   → wasm slots 4, 5
//	IR slot 4: i32 scratch    → wasm slot 6
//
// Body: store the len of the string param into the i32 scratch
// via a tee-and-extract round-trip, then add the two i32 params.
// Return scratch + a + c.
func TestSlotIdxMixed(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name: "main",
		Params: []ast.Param{
			{Name: "a", Type: i32()},
			{Name: "s", Type: strType()},
			{Name: "c", Type: i32()},
		},
		Locals: []*ast.Var{
			{Name: "t", Type: strType()},
		},
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			// Read string param `s` (IR slot 1) — wasm slots 1, 2.
			{Kind: ir.OpLoadLocal, I32: 1},
			// Tee into local `t` (IR slot 3) — wasm slots 4, 5.
			{Kind: ir.OpTeeLocal, I32: 3},
			// Stash len into scratch (IR slot 4) — wasm slot 6.
			{Kind: ir.OpStoreLocal, I32: 4},
			// Drop residual data on stack.
			{Kind: ir.OpDrop},
			// Now compute scratch + a + c.
			{Kind: ir.OpLoadLocal, I32: 4}, // scratch (len)
			{Kind: ir.OpLoadLocal, I32: 0}, // a
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoadLocal, I32: 2}, // c
			{Kind: ir.OpAdd},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// main(10, "long-enough literal here", 5) — len = 24 → 24+10+5 = 39.
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p,
		"10", "0", "24", "5") // pass: a=10, s=(data=0, len=24), c=5
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "39" {
		t.Fatalf("got %q, want 39", got)
	}
}

// TestEmitStrLenHeap — heap-form literal (>7 bytes) goes through
// the else arm of __fern_str_len: top bit of $len is 0, so the
// returned length is $len directly. Asserts the helper-call path
// finds the canonical len without rounding through the data ptr.
func TestEmitStrLenHeap(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hello, world"}, // 12 bytes → heap form
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "12" {
		t.Fatalf("got %q, want 12", got)
	}
}

// TestEmitStrLenInline — short ASCII literal (≤7 bytes) uses the
// inline-form packing where the length lives in bits 24..26 of
// the len word AND the top bit is set. Goes through the if-arm
// of __fern_str_len: extract bits 24..26.
func TestEmitStrLenInline(t *testing.T) {
	cases := []struct {
		s    string
		want string
	}{
		{"", "0"},        // empty
		{"a", "1"},       // 1 byte
		{"ab", "2"},      // 2 bytes
		{"abcdefg", "7"}, // 7 bytes (max inline)
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.s, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpConstStr, Str: tc.s},
					{Kind: ir.OpStrLen},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if got := runUnderWasmtime(t, bin, "main"); got != tc.want {
				t.Fatalf("len(%q) = %q, want %q", tc.s, got, tc.want)
			}
		})
	}
}

// TestEmitStrLenAcrossInlineHeapBoundary — eight-byte literal is
// the first one that forces heap-form (≤7 is inline). Confirms
// the transition point: both branches of __fern_str_len give the
// right answer for adjacent lengths.
func TestEmitStrLenAcrossInlineHeapBoundary(t *testing.T) {
	// 7 → inline, 8 → heap.
	for _, tc := range []struct {
		s    string
		want string
	}{
		{"1234567", "7"},
		{"12345678", "8"},
	} {
		tc := tc
		t.Run(tc.s, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpConstStr, Str: tc.s},
					{Kind: ir.OpStrLen},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if got := runUnderWasmtime(t, bin, "main"); got != tc.want {
				t.Fatalf("len(%q) = %q, want %q", tc.s, got, tc.want)
			}
		})
	}
}

// TestStrLenHelperOnlyEmittedWhenNeeded — confirm the helper isn't
// in the module's function section for a program that doesn't use
// OpStrLen. Sanity check on the gating scan.
func TestStrLenHelperOnlyEmittedWhenNeeded(t *testing.T) {
	progNoStrLen := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 42}},
	}}}
	bin, err := Emit(progNoStrLen)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Walk to find the function section (id 3) and assert exactly
	// 1 entry (just main).
	count := functionSectionCount(t, bin)
	if count != 1 {
		t.Fatalf("function-section count = %d, want 1 (helper should be absent)", count)
	}

	progWithStrLen := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "x"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin2, err := Emit(progWithStrLen)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	count2 := functionSectionCount(t, bin2)
	if count2 != 2 {
		t.Fatalf("function-section count = %d, want 2 (main + __fern_str_len)", count2)
	}
}

// functionSectionCount walks the section list and returns the
// number of entries in the function section (count uleb at the
// start of the section body). Returns 0 if no function section.
func functionSectionCount(t *testing.T, bin []byte) int {
	t.Helper()
	if len(bin) < 8 {
		return 0
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		size := 0
		shift := 0
		for {
			if i >= len(bin) {
				return 0
			}
			b := bin[i]
			i++
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if id == encode.SectionFunction {
			// Read the count uleb from the start of the body.
			body := bin[i : i+size]
			cnt := 0
			shift = 0
			j := 0
			for {
				b := body[j]
				j++
				cnt |= int(b&0x7f) << shift
				if b&0x80 == 0 {
					break
				}
				shift += 7
			}
			return cnt
		}
		i += size
	}
	return 0
}

// TestEmitAllocStoreLoadRoundtrip — alloc 4 bytes, store a value,
// load it back. Proves the bump cursor at memory[40] is seeded
// correctly and __fern_alloc returns a usable pointer.
func TestEmitAllocStoreLoadRoundtrip(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()}, // stash the alloc pointer
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpAlloc},
			{Kind: ir.OpStoreLocal, I32: 0}, // ptr → scratch
			// Store the value 12345 at *ptr.
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 12345},
			{Kind: ir.OpStore},
			// Load it back.
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoad},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "12345" {
		t.Fatalf("got %q, want 12345", got)
	}
}

// TestEmitAllocBumpsCursor — two successive allocs should return
// distinct pointers, with the second one allocSize bytes past the
// first. Proves the cursor bumps forward and doesn't reset.
func TestEmitAllocBumpsCursor(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32(), i32()}, // p1, p2
		ReturnType:   i32(),
		Ops: []ir.Op{
			// p1 = alloc(16)
			{Kind: ir.OpConstI32, I32: 16},
			{Kind: ir.OpAlloc},
			{Kind: ir.OpStoreLocal, I32: 0},
			// p2 = alloc(16)
			{Kind: ir.OpConstI32, I32: 16},
			{Kind: ir.OpAlloc},
			{Kind: ir.OpStoreLocal, I32: 1},
			// return p2 - p1 (expect 16)
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpSub},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "16" {
		t.Fatalf("p2 - p1 = %q, want 16", got)
	}
}

// TestEmitAllocSeedRespectsStringPool — when heap-form string
// literals are present, the cursor must seed past them so allocs
// don't clobber the data segment. Combines OpConstStr (which
// reserves bytes in the data section starting at stringStart)
// with OpAlloc and confirms the alloc returns a pointer >= the
// end of the string pool, 8-aligned.
func TestEmitAllocSeedRespectsStringPool(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "ptr",
		ReturnType: i32(),
		Ops: []ir.Op{
			// Push a heap-form literal of known length, drop the
			// (data, len) we don't need; this reserves the bytes
			// in the data segment.
			{Kind: ir.OpConstStr, Str: "a 13-byte str"}, // 13 bytes
			{Kind: ir.OpDrop},
			{Kind: ir.OpDrop},
			// Allocate 1 byte and return the pointer.
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpAlloc},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "ptr")
	// 8-byte rc-sentinel header at stringStart, data right after it,
	// len 13, then round the end up to the next multiple of 8. The bump
	// cursor starts there. Derived from stringStart rather than spelled
	// out, so relocating the static regions moves this with them.
	end := stringStart + 8 + 13
	if end%8 != 0 {
		end += 8 - end%8
	}
	if want := strconv.Itoa(end); got != want {
		t.Fatalf("alloc pointer = %q, want %s (string pool end rounded to 8)", got, want)
	}
}

// TestEmitAllocHelperGated — confirm __fern_alloc + the cursor
// seed segment are only emitted when OpAlloc appears.
func TestEmitAllocHelperGated(t *testing.T) {
	progNoAlloc := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 42}},
	}}}
	bin, err := Emit(progNoAlloc)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if functionSectionCount(t, bin) != 1 {
		t.Fatal("alloc helper present in module without OpAlloc")
	}

	progAlloc := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpAlloc},
		},
	}}}
	bin2, err := Emit(progAlloc)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if functionSectionCount(t, bin2) != 2 {
		t.Fatal("alloc helper not present when OpAlloc is used")
	}
}

// TestEmitStoreLoadStringRoundtrip — store a string literal into
// 8 bytes of allocated memory, then load it back and return the
// len. Exercises the two-word OpStore/OpLoad WidthString fan-out
// end-to-end with the allocator and OpStrLen helpers cooperating.
func TestEmitStoreLoadStringRoundtrip(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()}, // alloc ptr
		ReturnType:   i32(),
		Ops: []ir.Op{
			// p = alloc(8)
			{Kind: ir.OpConstI32, I32: 8},
			{Kind: ir.OpAlloc},
			{Kind: ir.OpStoreLocal, I32: 0},
			// Store "hello, world" (12 bytes, heap form) at *p.
			// Stack: [p, data, len].
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstStr, Str: "hello, world"},
			{Kind: ir.OpStore, Width: ir.WidthString},
			// Load back. Stack ops:
			//   load p; OpLoad WidthString → (data, len)
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoad, Width: ir.WidthString},
			// Call __fern_str_len to extract the canonical len.
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "12" {
		t.Fatalf("got %q, want 12", got)
	}
}

// TestEmitStoreLoadStringFirstByte — after store + load, read the
// first byte from the data pointer. Confirms the data half of
// the (data, len) pair survives store + load with the correct
// pointer value (and that the bytes really live at the offset
// data points at).
func TestEmitStoreLoadStringFirstByte(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 8},
			{Kind: ir.OpAlloc},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstStr, Str: "hello, world"},
			{Kind: ir.OpStore, Width: ir.WidthString},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoad, Width: ir.WidthString},
			// Stack: [data, len]. Drop len.
			{Kind: ir.OpDrop},
			// Load the byte at *data.
			{Kind: ir.OpLoadByte},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "104" { // 'h'
		t.Fatalf("got %q, want 104", got)
	}
}

// TestEmitStrPairScratchOnlyWhenUsed — confirm the 3 extra
// scratch wasm locals only appear in functions that load/store
// strings to memory. Functions without WidthString loads/stores
// keep the smaller local section.
func TestEmitStrPairScratchOnlyWhenUsed(t *testing.T) {
	fnNoStrMem := &ir.Func{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
	}
	if fnNeedsStrPairScratch(fnNoStrMem) {
		t.Fatal("no-WidthString function should not need scratch")
	}
	fnWithStrMem := &ir.Func{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoad, Width: ir.WidthString},
			{Kind: ir.OpDrop},
		},
	}
	if !fnNeedsStrPairScratch(fnWithStrMem) {
		t.Fatal("WidthString-using function should need scratch")
	}
}

// TestEmitStrEq exercises every shape of OpStrEq the runtime
// helper needs to handle correctly:
//   - both heap, same content        → 1
//   - both heap, different content    → 0
//   - both heap, different lengths    → 0
//   - both inline, same content       → 1
//   - both inline, different content  → 0
//   - mixed (one heap, one inline) of the same content → 1
//   - empty == empty                  → 1
//
// Each sub-test builds two OpConstStrs back-to-back and calls
// OpStrEq, returning the 0/1 result via wasmtime.
func TestEmitStrEq(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want string
	}{
		{"both_heap_equal", "hello, world", "hello, world", "1"},
		{"both_heap_distinct", "hello, world", "hello, WORLD", "0"},
		{"both_heap_diff_len", "hello, world", "hello, world!", "0"},
		{"both_inline_equal", "abc", "abc", "1"},
		{"both_inline_distinct", "abc", "abd", "0"},
		{"mixed_equal", "abc", "abc", "1"}, // inline + inline; same shape
		{"empty_equal", "", "", "1"},
		{"empty_vs_short", "", "a", "0"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpConstStr, Str: tc.a},
					{Kind: ir.OpConstStr, Str: tc.b},
					{Kind: ir.OpStrEq},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if got := runUnderWasmtime(t, bin, "main"); got != tc.want {
				t.Fatalf("eq(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestEmitStrEqMixedHeapInline — one heap-form, one inline-form,
// same content. The byte-loop path runs __fern_str_byte on both
// sides; inline pulls bytes out of the (data, len) bits, heap
// reads from memory. Catches drift between the two halves of the
// SSO seam.
func TestEmitStrEqMixedHeapInline(t *testing.T) {
	// "longstring" (10 bytes, heap) vs the same heap-form literal —
	// pair-eq fast path catches it. To force the byte loop on a
	// mixed pair, we'd need an inline + heap of the same content;
	// since FitsInlineWasm caps inline at 7 bytes, we use the
	// 7-byte "1234567" — inline form for both sides under
	// PackInlineWasm, plus a single heap-form re-cast of the same
	// content. There's no straightforward way to force a heap-form
	// literal at 7 bytes (PackInlineWasm always picks inline), so
	// instead we cross-check inline-inline of identical content
	// (taking pair-eq path) and inline-inline of distinct content
	// (taking both-inline-distinct path) here; the both-heap byte
	// loop and the inline-aware __fern_str_byte are covered in
	// TestEmitStrEq above.
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "1234567"},
			{Kind: ir.OpConstStr, Str: "1234567"},
			{Kind: ir.OpStrEq},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "1" {
		t.Fatalf("got %q, want 1", got)
	}
}

// TestStrEqHelperPulledIn — using OpStrEq must drag in __str_eq,
// __fern_str_len, and __fern_str_byte (chain of helper deps).
// Confirms scanRuntimeHelpers walks the transitive dependency.
func TestStrEqHelperPulledIn(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "x"},
			{Kind: ir.OpConstStr, Str: "x"},
			{Kind: ir.OpStrEq},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// main + __fern_str_len + __fern_str_byte + __str_eq = 4 funcs.
	if got := functionSectionCount(t, bin); got != 4 {
		t.Fatalf("function-section count = %d, want 4 (main + 3 helpers)", got)
	}
}

// TestEmitStrConcatLen — concatenate two literals and verify the
// resulting length via __fern_str_len. Spans the inline-heap
// combinations: inline+inline, heap+heap, mixed.
func TestEmitStrConcatLen(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want string
	}{
		{"inline_plus_inline", "abc", "def", "6"},
		{"heap_plus_heap", "hello, world", " - and more!", "24"},
		{"heap_plus_inline", "hello, world", "!", "13"},
		{"inline_plus_heap", "!", "hello, world", "13"},
		{"empty_plus_short", "", "abc", "3"},
		{"short_plus_empty", "abc", "", "3"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpConstStr, Str: tc.a},
					{Kind: ir.OpConstStr, Str: tc.b},
					{Kind: ir.OpStrConcat},
					{Kind: ir.OpStrLen},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if got := runUnderWasmtime(t, bin, "main"); got != tc.want {
				t.Fatalf("len(%q + %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestEmitStrConcatByteContents — concatenate two strings, then
// read each byte of the result and assert it matches the expected
// concatenation. Confirms the byte copy from both operands lands
// at the right offsets.
func TestEmitStrConcatByteContents(t *testing.T) {
	a := "hello"
	b := "world"
	want := a + b // "helloworld"
	for i, expected := range []byte(want) {
		expected := expected
		i := i
		t.Run(string(rune(expected)), func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops: []ir.Op{
					// Build (data, len) of "hello" + "world"
					{Kind: ir.OpConstStr, Str: a},
					{Kind: ir.OpConstStr, Str: b},
					{Kind: ir.OpStrConcat},
					// Drop len, keep data.
					{Kind: ir.OpDrop},
					// data + i, load8_u.
					{Kind: ir.OpConstI32, I32: int32(i)},
					{Kind: ir.OpAdd},
					{Kind: ir.OpLoadByte},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			got := runUnderWasmtime(t, bin, "main")
			gotInt := 0
			for _, c := range got {
				gotInt = gotInt*10 + int(c-'0')
			}
			if byte(gotInt) != expected {
				t.Fatalf("byte[%d] = %d, want %d (%c)", i, gotInt, expected, expected)
			}
		})
	}
}

// TestEmitStrConcatRoundTripsThroughStrEq — concat result must be
// byte-equal to a heap-form literal of the same content. Closes
// the loop: alloc + copy + str_eq all the way through.
func TestEmitStrConcatRoundTripsThroughStrEq(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// "foo" + "bar baz" = "foobar baz" (10 bytes, heap form).
			{Kind: ir.OpConstStr, Str: "foo"},
			{Kind: ir.OpConstStr, Str: "bar baz"},
			{Kind: ir.OpStrConcat}, // → (data, len) of "foobar baz"
			// Compare to "foobar baz" heap literal.
			{Kind: ir.OpConstStr, Str: "foobar baz"},
			{Kind: ir.OpStrEq},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "1" {
		t.Fatalf("concat result not equal to expected literal: got %q, want 1", got)
	}
}

// TestEmitConstFuncAddress — OpConstFunc pushes a static closure-
// pair cell pointer. The cell holds (fn_idx, env_ptr=0); the test
// verifies the cell's bytes via i32.load at offset 0 (gets the
// fn_idx).
func TestEmitConstFuncAddress(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 99}},
		},
		{
			Name:       "fn_idx_of_target",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstFunc, Str: "target"},
				{Kind: ir.OpLoad}, // load fn_idx at +0
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// target is prog.Funcs[0] → funcidx 0.
	if got := runUnderWasmtime(t, bin, "fn_idx_of_target"); got != "0" {
		t.Fatalf("fn_idx_of_target = %q, want 0", got)
	}
}

// TestEmitConstFuncEnvIsZero — confirms env_ptr at offset 4 is 0.
func TestEmitConstFuncEnvIsZero(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			Name:       "env_of_target",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstFunc, Str: "target"},
				{Kind: ir.OpConstI32, I32: 4},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "env_of_target"); got != "0" {
		t.Fatalf("env_of_target = %q, want 0", got)
	}
}

// TestEmitConstFuncInterning — same target gives the same cell.
func TestEmitConstFuncInterning(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			Name:       "ptr1",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstFunc, Str: "target"}},
		},
		{
			Name:       "ptr2",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstFunc, Str: "target"}},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	a := runUnderWasmtime(t, bin, "ptr1")
	b := runUnderWasmtime(t, bin, "ptr2")
	if a != b {
		t.Fatalf("ptr1 = %q, ptr2 = %q — expected same address (interning failed)", a, b)
	}
	if want := strconv.Itoa(closuresBase); a != want {
		t.Fatalf("ptr = %q, want %s (closuresBase)", a, want)
	}
}

// TestEmitConstFuncTwoTargets — two distinct targets get
// adjacent 8-byte cells.
func TestEmitConstFuncTwoTargets(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "a",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			Name:       "b",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			// diff = address_of("b") - address_of("a"). With "b"
			// interned first (cell 0, addr closuresBase) and "a"
			// interned second (cell 1, addr closuresBase+8), the
			// result is -8. Equivalent fact: distinct targets get
			// distinct cells 8 bytes apart.
			Name:       "diff",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstFunc, Str: "b"},
				{Kind: ir.OpConstFunc, Str: "a"},
				{Kind: ir.OpSub},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "diff"); got != "-8" {
		t.Fatalf("addr(b) - addr(a) = %q, want -8 (8 bytes apart)", got)
	}
}

// TestEmitConstFuncPoolClearsFreelistHeads — the static closure-cell pool
// must not alias the allocator's freelist heads table (#6142).
//
// The pool used to start at 96 with a (1024-96)/8 = 116-cell budget while
// the heads table owned [256, 1024), so from cell 20 onwards the two wrote
// over each other. The visible failure was a trap deep inside the
// allocator: cell 20's data segment left a function index sitting in
// head[0], the first 16-byte allocation popped it as a free block and
// returned a pointer into reserved low memory, and the program wrote
// through it over the bump cursor at 40.
//
// So allocate after interning past that boundary and check the pointer is
// a real heap address. A pool that still overlapped would hand back the
// small integer it found in the head slot.
func TestEmitConstFuncPoolClearsFreelistHeads(t *testing.T) {
	// The WHOLE pool, so the last cell sits at the very top of it. The
	// layout is chained, so the pool's own budget is what bounds the stress.
	const n = maxClosureCells
	funcs := make([]*ir.Func, 0, n+1)
	alloc := &ir.Func{Name: "alloc16", ReturnType: i32()}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("t%d", i)
		funcs = append(funcs, &ir.Func{
			Name:       name,
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: int32(i)}},
		})
		alloc.Ops = append(alloc.Ops,
			ir.Op{Kind: ir.OpConstFunc, Str: name},
			ir.Op{Kind: ir.OpDrop})
	}
	alloc.Ops = append(alloc.Ops,
		ir.Op{Kind: ir.OpConstI32, I32: 16},
		ir.Op{Kind: ir.OpAlloc})
	funcs = append(funcs, alloc)

	bin, err := Emit(&ir.Program{Funcs: funcs})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "alloc16")
	ptr, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("alloc16 returned %q, not an integer: %v", got, err)
	}
	if ptr < stringStart {
		t.Errorf("alloc after %d closure cells returned %d, want >= %d (stringStart) — "+
			"a low pointer means the allocator popped a poisoned freelist head, "+
			"i.e. the closure pool is writing over the heads table at %d",
			n, ptr, stringStart, freelistHeadsAddr)
	}
}

// TestEmitMatchTagOnSentinel — OpEnumSentinel pushes the heap
// address of a static [tag=N] box; OpMatchTag's i32.load reads
// the tag back from it. Verifies the round trip for several
// tag values, including 0 (None / Ok) and a payloadless variant
// with a larger tag.
func TestEmitMatchTagOnSentinel(t *testing.T) {
	for _, tag := range []int32{0, 1, 5, 42} {
		tag := tag
		t.Run(fmt.Sprintf("tag_%d", tag), func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpEnumSentinel, I32: tag},
					{Kind: ir.OpMatchTag},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			got := runUnderWasmtime(t, bin, "main")
			want := fmt.Sprintf("%d", tag)
			if got != want {
				t.Fatalf("tag round-trip: got %q, want %q", got, want)
			}
		})
	}
}

// TestEmitEnumSentinelInterning — same tag used twice should map
// to the same data-segment offset.
func TestEmitEnumSentinelInterning(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "a",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpEnumSentinel, I32: 0},
				{Kind: ir.OpMatchTag},
			},
		},
		{
			Name:       "b",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpEnumSentinel, I32: 1},
				{Kind: ir.OpMatchTag},
			},
		},
		{
			Name:       "c",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpEnumSentinel, I32: 0}, // re-use tag 0
				{Kind: ir.OpMatchTag},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	segs := dataSegments(t, bin)
	if len(segs) != 1 {
		t.Fatalf("expected 1 data segment, got %d", len(segs))
	}
	// Two unique tags, each a 12-byte cell: an 8-byte rc header
	// (Phase 1e-enums-ii static sentinel 0x80000000 + pad) followed
	// by the 4-byte tag. The repeated tag 0 in func `c` interns to
	// the same cell, so 2 cells × 12 = 24 bytes.
	if len(segs[0]) != 24 {
		t.Fatalf("segment size = %d, want 24 (two 12-byte cells: 8-byte rc header + 4-byte tag)", len(segs[0]))
	}
	if got := runUnderWasmtime(t, bin, "a"); got != "0" {
		t.Fatalf("a = %q, want 0", got)
	}
	if got := runUnderWasmtime(t, bin, "b"); got != "1" {
		t.Fatalf("b = %q, want 1", got)
	}
	if got := runUnderWasmtime(t, bin, "c"); got != "0" {
		t.Fatalf("c = %q, want 0", got)
	}
}

// TestEmitMatchTagOnConstructedBox — alloc 4 bytes, store a tag,
// OpMatchTag reads it back. Confirms OpMatchTag works on
// dynamically-allocated boxes, not just static sentinels.
func TestEmitMatchTagOnConstructedBox(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpAlloc},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpStore},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpMatchTag},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "7" {
		t.Fatalf("got %q, want 7", got)
	}
}

// TestEmitMakeSomeReturnPair — pair-form Option[i32] function.
// Tag+payload returned via wasm multi-value (i32, i32). Two
// caller helpers extract just the tag (0 for Some) and just the
// payload (42) to verify both halves come back.
func TestEmitMakeSomeReturnPair(t *testing.T) {
	prog := &ir.Program{
		Funcs: []*ir.Func{
			{
				Name:       "makesome",
				ReturnType: i32(), // overridden by PairForm
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 42},
					{Kind: ir.OpMakeSomeI32},
					{Kind: ir.OpReturnPair},
				},
			},
			{
				Name:       "tag",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpCallDirectPair, Str: "makesome"},
					{Kind: ir.OpDrop}, // drop payload, leave tag
				},
			},
			{
				Name:         "payload",
				ScratchTypes: []ast.Type{i32()},
				ReturnType:   i32(),
				Ops: []ir.Op{
					{Kind: ir.OpCallDirectPair, Str: "makesome"},
					{Kind: ir.OpStoreLocal, I32: 0}, // payload → scratch
					{Kind: ir.OpDrop},               // drop tag
					{Kind: ir.OpLoadLocal, I32: 0},
				},
			},
		},
		PairForm: map[string]bool{"makesome": true},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "tag"); got != "0" {
		t.Fatalf("Some(42) tag = %q, want 0", got)
	}
	if got := runUnderWasmtime(t, bin, "payload"); got != "42" {
		t.Fatalf("Some(42) payload = %q, want 42", got)
	}
}

// TestEmitMakeNonePair — None's payload is forced to 0; tag is 1.
func TestEmitMakeNonePair(t *testing.T) {
	prog := &ir.Program{
		Funcs: []*ir.Func{
			{
				Name:       "makenone",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpMakeNoneI32},
					{Kind: ir.OpReturnPair},
				},
			},
			{
				Name:       "sum",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpCallDirectPair, Str: "makenone"},
					{Kind: ir.OpAdd}, // 1 + 0 = 1
				},
			},
		},
		PairForm: map[string]bool{"makenone": true},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "sum"); got != "1" {
		t.Fatalf("None (tag+payload) = %q, want 1", got)
	}
}

// TestEmitMakeOkErrPair — Result[i32] with both arms.
// combined = ok_tag*100 + ok_payload + err_tag*10 + err_payload
// = 0*100 + 99 + 1*10 + 7 = 116
func TestEmitMakeOkErrPair(t *testing.T) {
	prog := &ir.Program{
		Funcs: []*ir.Func{
			{
				Name:       "mk_ok",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 99},
					{Kind: ir.OpMakeOkI32},
					{Kind: ir.OpReturnPair},
				},
			},
			{
				Name:       "mk_err",
				ReturnType: i32(),
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 7},
					{Kind: ir.OpMakeErrI32},
					{Kind: ir.OpReturnPair},
				},
			},
			{
				Name:         "combined",
				ScratchTypes: []ast.Type{i32(), i32(), i32(), i32()},
				ReturnType:   i32(),
				Ops: []ir.Op{
					{Kind: ir.OpCallDirectPair, Str: "mk_ok"},
					{Kind: ir.OpStoreLocal, I32: 1},
					{Kind: ir.OpStoreLocal, I32: 0},
					{Kind: ir.OpCallDirectPair, Str: "mk_err"},
					{Kind: ir.OpStoreLocal, I32: 3},
					{Kind: ir.OpStoreLocal, I32: 2},
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpConstI32, I32: 100},
					{Kind: ir.OpMul},
					{Kind: ir.OpLoadLocal, I32: 1},
					{Kind: ir.OpAdd},
					{Kind: ir.OpLoadLocal, I32: 2},
					{Kind: ir.OpConstI32, I32: 10},
					{Kind: ir.OpMul},
					{Kind: ir.OpAdd},
					{Kind: ir.OpLoadLocal, I32: 3},
					{Kind: ir.OpAdd},
				},
			},
		},
		PairForm: map[string]bool{"mk_ok": true, "mk_err": true},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "combined"); got != "116" {
		t.Fatalf("combined = %q, want 116", got)
	}
}

// TestEmitPrintHeapLiteral — call __fern_print with a heap-form
// literal. The helper allocates, copies bytes, writes an iovec
// to the fixed scratch slot, and invokes wasi_snapshot_preview1
// fd_write. Run under wasmtime with WASI command-mode entry and
// grep stdout.
func TestEmitPrintHeapLiteral(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "_start",
		ReturnType: void(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hello, world\n"},
			{Kind: ir.OpCallDirect, Str: "__fern_print", I32: 1},
			{Kind: ir.OpReturnVoid},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	if got := so.String(); got != "hello, world\n\n" {
		t.Fatalf("stdout = %q, want %q", got, "hello, world\n\n")
	}
}

// TestEmitPrintViaSourceNameAlias — calling OpCallDirect "print"
// (the source-language built-in name) must alias to the synthetic
// __fern_print helper. This is the path real lang programs take
// since the IR lowering emits OpCallDirect with the source name.
func TestEmitPrintViaSourceNameAlias(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "_start",
		ReturnType: void(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "aliased!\n"},
			{Kind: ir.OpCallDirect, Str: "print", I32: 1},
			{Kind: ir.OpReturnVoid},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	if got := so.String(); got != "aliased!\n\n" {
		t.Fatalf("stdout = %q, want %q", got, "aliased!\n\n")
	}
}

// TestEmitEnvCount — call OpCallDirect "env_count" under
// wasmtime. wasmtime sandboxes env vars by default (returns 0)
// but the helper must execute without erroring.
func TestEmitEnvCount(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "env_count"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got == "" {
		t.Fatalf("env_count: empty stdout")
	}
}

// TestEmitNowNs — call OpCallDirect "now_ns" twice, return the
// i32-truncated difference. Two consecutive realtime samples
// always differ by far less than 2^31 ns. Just sanity-checks
// that the helper produces a runnable function under wasmtime.
func TestEmitNowNs(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "diff_ns",
		ScratchTypes: []ast.Type{i64(), i64()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "now_ns"},
			{Kind: ir.OpStoreLocal, I32: 0}, // t0
			{Kind: ir.OpCallDirect, Str: "now_ns"},
			{Kind: ir.OpStoreLocal, I32: 1}, // t1
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpSub, Width: 64},
			{Kind: ir.OpWrapI64}, // truncate to i32
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "diff_ns")
	if got == "" {
		t.Fatal("empty stdout from diff_ns")
	}
}

// TestEmitArgCount — call OpCallDirect "arg_count" under
// wasmtime. wasmtime --invoke passes the module path itself as
// argv[0], so argc ≥ 1 always. Passing extra args after the
// path increments argc.
func TestEmitArgCount(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "arg_count"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Two extra args → argc = 3 (path + 2). wasmtime puts the
	// module-path argv[0] in the wasi cli's argv.
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p, "alpha", "beta")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "3" {
		t.Fatalf("arg_count = %q, want 3 (path + 2 extra)", got)
	}
}

// TestEmitArgs — call OpCallDirect "args" under wasmtime and
// check the length prefix at data-4 reflects argc. wasmtime
// puts the module path at argv[0], so extra positional args
// after the path each bump argc by one.
func TestEmitArgs(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "args"},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpSub},
			{Kind: ir.OpLoad},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p, "alpha", "beta")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "3" {
		t.Fatalf("len(args()) = %q, want 3 (path + 2 extra)", got)
	}
}

// TestEmitReinterpretF64I64 — push an f64, reinterpret as i64,
// reinterpret back as f64. The round-trip should preserve all
// 64 bits (including NaN / Inf payloads, but we just check a
// regular value here).
func TestEmitReinterpretF64I64(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: f64(),
		Ops: []ir.Op{
			{Kind: ir.OpConstF64, F64: 3.14159},
			{Kind: ir.OpReinterpretI64F64},
			{Kind: ir.OpReinterpretF64I64},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "main")
	if !strings.HasPrefix(got, "3.14") {
		t.Fatalf("f64 round-trip = %q, want prefix 3.14", got)
	}
}

// TestEmitFloatMathHelpers — round-trip the f64 math helpers
// that map to native wasm ops (sqrt, abs, floor, ceil, trunc).
// Each call should match the wasm-native semantics exactly.
func TestEmitFloatMathHelpers(t *testing.T) {
	cases := []struct {
		name   string
		callee string
		arg    float64
		wantPx string // stdout prefix (float printing is loose)
	}{
		{"sqrt_25", "__sqrt_f64", 25.0, "5"},
		{"abs_neg42", "__abs_f64", -42.5, "42.5"},
		{"floor_pos", "__floor_f64", 3.7, "3"},
		{"ceil_pos", "__ceil_f64", 3.2, "4"},
		{"trunc_neg", "__trunc_f64", -3.7, "-3"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: f64(),
				Ops: []ir.Op{
					{Kind: ir.OpConstF64, F64: c.arg},
					{Kind: ir.OpCallDirect, Str: c.callee},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			got := runUnderWasmtime(t, bin, "main")
			if !strings.HasPrefix(got, c.wantPx) {
				t.Fatalf("%s(%g) = %q, want prefix %q", c.callee, c.arg, got, c.wantPx)
			}
		})
	}
}

// TestEmitEnvLookupMatch — env("FOO") with --env FOO=bar
// should return Some(bar). Verify by reading the box's tag
// (should be 0) and the value's len (3 = "bar").
func TestEmitEnvLookupMatch(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "FOO"},
			{Kind: ir.OpCallDirect, Str: "env"},
			{Kind: ir.OpStoreLocal, I32: 0},
			// tag at +0; if Some (tag=0), return the value's logical
			// length via OpStrLen (data@+8, len@+12) — env() now copies
			// the matched value into an OWNED string, so a short value
			// like "bar" is inline-packed and its raw len word carries
			// the inline flag; OpStrLen decodes both forms. Else -1.
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoad},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpEq},
			{Kind: ir.OpIf, I32: ir.BlockTypeI32},
			{Kind: ir.OpConstI32, I32: -1},
			{Kind: ir.OpElse},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 8},
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoad},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 12},
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoad},
			{Kind: ir.OpStrLen},
			{Kind: ir.OpEnd},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--env", "FOO=bar", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "3" {
		t.Fatalf("env(\"FOO\") = %q, want 3 (len(\"bar\"))", got)
	}
}

// TestEmitEnvLookupMiss — env("NOPE") with no matching env
// returns None (tag=1).
func TestEmitEnvLookupMiss(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "NOPE"},
			{Kind: ir.OpCallDirect, Str: "env"},
			{Kind: ir.OpLoad}, // tag
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "1" {
		t.Fatalf("env(\"NOPE\") tag = %q, want 1 (None)", got)
	}
}

// TestEmitNowUnixMs — call OpCallDirect "now_unix_ms" and
// verify the result is non-zero and within a plausible recent
// range. The exact value can't be asserted but we can check
// it's a reasonable wall-clock ms reading.
func TestEmitNowUnixMs(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i64(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "now_unix_ms"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "main")
	if got == "" {
		t.Fatal("empty stdout")
	}
	// Should be far past 2000-01-01 (in ms: ~9.46e11) and at
	// most a few years in the future.
	n, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", got, err)
	}
	if n < 946_684_800_000 || n > 4_000_000_000_000 {
		t.Fatalf("now_unix_ms = %d, out of plausible range", n)
	}
}

// TestEmitMonotonicNs — two monotonic_ns() readings should
// produce a positive (non-negative) difference, since
// CLOCK_MONOTONIC is non-decreasing.
func TestEmitMonotonicNs(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i64()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "monotonic_ns"},
			{Kind: ir.OpStoreLocal, I32: 0}, // t0
			{Kind: ir.OpCallDirect, Str: "monotonic_ns"},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpSub, Width: 64},
			// Compare diff >= 0: convert to i32 result via
			// comparison (diff >= 0 → 1, else 0).
			{Kind: ir.OpConstI64, I64: 0},
			{Kind: ir.OpGeS, Width: 64},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "1" {
		t.Fatalf("monotonic_ns diff >= 0 = %q, want 1", got)
	}
}

// TestEmitWrite — write(s) goes to stdout without appending a
// newline (pairs with print() which DOES append). Send "hi"
// twice; expect "hihi" without separators.
func TestEmitWrite(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: void(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hi"},
			{Kind: ir.OpCallDirect, Str: "write"},
			{Kind: ir.OpConstStr, Str: "hi"},
			{Kind: ir.OpCallDirect, Str: "write"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := so.String(); got != "hihi" {
		t.Fatalf("write twice = %q, want \"hihi\"", got)
	}
}

// TestEmitRandomBytes — random_bytes(8) returns a (data, len)
// pair. len should be 8 (heap form, top bit clear). The
// actual bytes are unpredictable; just verify the length.
func TestEmitRandomBytes(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 8},
			{Kind: ir.OpCallDirect, Str: "random_bytes"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "8" {
		t.Fatalf("len(random_bytes(8)) = %q, want 8", got)
	}
}

// TestEmitRandomBytesEmpty — random_bytes(0) returns the
// inline empty sentinel (0, 0x80000000). str_len → 0.
func TestEmitRandomBytesEmpty(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpCallDirect, Str: "random_bytes"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "0" {
		t.Fatalf("len(random_bytes(0)) = %q, want 0", got)
	}
}

// TestEmitPutchar — call OpCallDirect "putchar" twice with
// 'A' (65) and 'B' (66) and verify stdout is "AB". Exercises
// the single-byte fd_write path that uses printIovecAddr +
// printRetAddr as a 1-byte scratch buffer.
func TestEmitPutchar(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: void(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 65},
			{Kind: ir.OpCallDirect, Str: "putchar"},
			{Kind: ir.OpConstI32, I32: 66},
			{Kind: ir.OpCallDirect, Str: "putchar"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := so.String(); got != "AB" {
		t.Fatalf("putchar stdout = %q, want \"AB\"", got)
	}
}

// TestEmitArgsEntries — each args() entry is a 2-word (data, len)
// pair at data + i*8. Read the len of args[1] (wasmtime sets
// argv[1] to the first positional after the module path).
func TestEmitArgsEntries(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "args"},
			{Kind: ir.OpStoreLocal, I32: 0},
			// args[1] is the two-word string at data + 1*8: data@+8,
			// len@+12. args() now copies each entry into an OWNED
			// string, so a short arg like "alpha" is inline-packed and
			// its raw len word carries the inline flag — decode the
			// logical length via OpStrLen (data, len → i32).
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 8},
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoad},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 12},
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoad},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p, "alpha", "beta")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "5" {
		t.Fatalf("len(args()[1]) = %q, want 5 (\"alpha\")", got)
	}
}

// TestEmitArgAt — call OpCallDirect "arg_at" with i=1 under
// wasmtime, return the string length of the result. argv[1] is
// "alpha" (5 bytes) since wasmtime puts the module path at
// argv[0]. Exercises wasi_args_sizes_get + wasi_args_get + the
// strlen loop in __fern_arg_at.
func TestEmitArgAt(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpCallDirect, Str: "arg_at"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p, "alpha", "beta")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "5" {
		t.Fatalf("len(arg_at(1)) = %q, want 5 (\"alpha\")", got)
	}
}

// TestEmitArgAtPrint — call arg_at(2) and pipe the (data, len)
// pair into print. wasmtime's argv[2] is "beta"; stdout should
// match exactly.
func TestEmitArgAtPrint(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: void(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpCallDirect, Str: "arg_at"},
			{Kind: ir.OpCallDirect, Str: "print"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p, "alpha", "beta")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := so.String(); got != "beta\n" {
		t.Fatalf("arg_at(2) printed = %q, want \"beta\\n\" (print auto-appends newline)", got)
	}
}

// TestEmitArgAtOutOfRange — i out of [0, argc) must return
// (0, 0), so str_len reports 0.
func TestEmitArgAtOutOfRange(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 999},
			{Kind: ir.OpCallDirect, Str: "arg_at"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "0" {
		t.Fatalf("arg_at(999) len = %q, want 0", got)
	}
}

// TestEmitEnvAt — call env_at(0) and assert it produces *some*
// output via print. wasmtime sandboxes env by default, but
// --env passes values through. We pass FOO=bar and check the
// 0th entry is "FOO=bar".
func TestEmitEnvAt(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: void(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpCallDirect, Str: "env_at"},
			{Kind: ir.OpCallDirect, Str: "print"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--env", "FOO=bar", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := so.String(); got != "FOO=bar\n" {
		t.Fatalf("env_at(0) = %q, want \"FOO=bar\\n\" (print auto-appends newline)", got)
	}
}

// TestEmitLoadStoreI32 — round-trip an i32 through memory via
// __store_i32 + __load_i32. Stores 42 at addr 200, then reads
// it back.
func TestEmitLoadStoreI32(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// __store_i32(200, 42)
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI32, I32: 42},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// __load_i32(200)
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpCallDirect, Str: "__load_i32"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "42" {
		t.Fatalf("load_i32 = %q, want 42", got)
	}
}

// TestEmitLoadStoreI64 — round-trip an i64. Map runtime uses
// these for wide-key boxing on wasm32.
func TestEmitLoadStoreI64(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i64(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI64, I64: 0x1234_5678_9abc_def0},
			{Kind: ir.OpCallDirect, Str: "__store_i64"},
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpCallDirect, Str: "__load_i64"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	want := fmt.Sprintf("%d", int64(0x1234_5678_9abc_def0))
	if got := runUnderWasmtime(t, bin, "main"); got != want {
		t.Fatalf("load_i64 = %q, want %q", got, want)
	}
}

// TestEmitPtrWidth — wasm32 returns 4.
func TestEmitPtrWidth(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "__ptr_width"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "4" {
		t.Fatalf("ptr_width = %q, want 4", got)
	}
}

// TestEmitDropWidthString — OpDrop with Width=WidthString must
// emit two wasm `drop`s, not one. copyprop rewrites a dead
// OpStoreLocal on a string slot into OpDrop{Width: WidthString};
// without this expansion the operand stack leaks one i32 (the
// data half of the (data, len) pair) into whatever consumes
// the next value.
//
// Regression for the seed=42 mismatch: a callee that built a
// string local then returned an i32 had the data slot leaked
// into the caller's arithmetic, producing wildly off-by results.
func TestEmitDropWidthString(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// Push a string pair (data, len) onto the stack.
			{Kind: ir.OpConstStr, Str: "hello"},
			// Drop it as a string (two-slot drop).
			{Kind: ir.OpDrop, Width: ir.WidthString},
			// Push 42 and return — should be 42, not leftover len.
			{Kind: ir.OpConstI32, I32: 42},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "42" {
		t.Fatalf("drop-width-string result = %q, want 42", got)
	}
}

// TestEmitAllocU8LengthPrefix — __alloc_u8(n) must write n at
// [data_ptr - 4] so __arr_idx_1's bounds check sees the right
// length. Without this, byte-array indexing trips `unreachable`
// at seemingly arbitrary indices because the length cell holds
// whatever pre-alloc garbage was there.
func TestEmitAllocU8LengthPrefix(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			// Allocate 12 bytes via __alloc_u8 and stash data_ptr.
			{Kind: ir.OpConstI32, I32: 12},
			{Kind: ir.OpCallDirect, Str: "__alloc_u8"},
			{Kind: ir.OpStoreLocal, I32: 0},
			// Read mem[data_ptr - 4] — should be 12.
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpSub},
			{Kind: ir.OpLoad},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "12" {
		t.Fatalf("alloc_u8(12) prefix = %q, want 12", got)
	}
}

// TestEmitMemcpy — copy 4 bytes from addr 200 to addr 300, then
// load i32 from 300 to verify.
func TestEmitMemcpy(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// __store_i32(200, 0x12345678)
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI32, I32: 0x12345678},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// __memcpy(300, 200, 4)
			{Kind: ir.OpConstI32, I32: 300},
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpCallDirect, Str: "__memcpy"},
			// __load_i32(300)
			{Kind: ir.OpConstI32, I32: 300},
			{Kind: ir.OpCallDirect, Str: "__load_i32"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "305419896" {
		t.Fatalf("memcpy + load = %q, want 305419896 (0x12345678)", got)
	}
}

// TestEmitMemset — fill 4 bytes at addr 200 with byte 0xAB, then
// load as i32 → 0xABABABAB = -1414812757 signed.
func TestEmitMemset(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI32, I32: 0xAB},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpCallDirect, Str: "__memset"},
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpCallDirect, Str: "__load_i32"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "-1414812757" {
		t.Fatalf("memset + load = %q, want -1414812757 (0xABABABAB)", got)
	}
}

// TestEmitStringFromBytesShort — build a 3-element u8 array
// ['h', 'i', '!'] at addr 200 (length prefix at 196), call
// string_from_bytes_unchecked(200), then assert the result's length is 3
// via OpStrLen. Inline-fast-path (len ≤ 7).
func TestEmitStringFromBytesShort(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// mem[196] = 3 (length prefix)
			{Kind: ir.OpConstI32, I32: 196},
			{Kind: ir.OpConstI32, I32: 3},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// mem[200] = 'h' (0x68)
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI32, I32: 'h'},
			{Kind: ir.OpStoreI8},
			// mem[201] = 'i' (0x69)
			{Kind: ir.OpConstI32, I32: 201},
			{Kind: ir.OpConstI32, I32: 'i'},
			{Kind: ir.OpStoreI8},
			// mem[202] = '!' (0x21)
			{Kind: ir.OpConstI32, I32: 202},
			{Kind: ir.OpConstI32, I32: '!'},
			{Kind: ir.OpStoreI8},
			// string_from_bytes_unchecked(200) → (data, len)
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpCallDirect, Str: "string_from_bytes_unchecked"},
			// pop (data, len), then push back data + str_len → 3
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "3" {
		t.Fatalf("string_from_bytes_unchecked len = %q, want 3", got)
	}
}

// TestEmitStringFromBytesEmpty — empty array → inline empty
// `(0, 0x80000000)`. str_len reports 0.
func TestEmitStringFromBytesEmpty(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// mem[196] = 0 (length prefix)
			{Kind: ir.OpConstI32, I32: 196},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// string_from_bytes_unchecked(200)
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpCallDirect, Str: "string_from_bytes_unchecked"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "0" {
		t.Fatalf("string_from_bytes_unchecked empty len = %q, want 0", got)
	}
}

// TestEmitStrSliceHeap — slice a heap-form string. The slice
// "world" from "hello world" (chars 6..11) should have length 5.
// Heap-form goes through the memory.copy fast path.
func TestEmitStrSliceHeap(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hello world"},
			{Kind: ir.OpConstI32, I32: 6},
			{Kind: ir.OpConstI32, I32: 11},
			{Kind: ir.OpCallDirect, Str: "__str_slice"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "5" {
		t.Fatalf("str_slice heap len = %q, want 5", got)
	}
}

// TestEmitStrSliceInline — slice into the inline-form fast path.
// "hi" sliced [1:2] is "i" — 1 char.
func TestEmitStrSliceInline(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hi"},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpCallDirect, Str: "__str_slice"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "1" {
		t.Fatalf("str_slice inline len = %q, want 1", got)
	}
}

// TestEmitStrSliceEmpty — empty slice → inline empty (0, top-bit-set).
// str_len reports 0.
func TestEmitStrSliceEmpty(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hello"},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpCallDirect, Str: "__str_slice"},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "0" {
		t.Fatalf("str_slice empty len = %q, want 0", got)
	}
}

// TestEmitStrIdxHeap — heap-form string: __str_idx returns
// base_data + i, OpLoadByte at that address reads the byte.
// "hello" is too long for inline form on wasm32 (max 7 ASCII
// bytes is supported, "hello" is 5 — inline applies!)... so
// pick a longer string to force heap form: "abcdefghi" (9 bytes).
func TestEmitStrIdxHeap(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "abcdefghi"},
			{Kind: ir.OpConstI32, I32: 4}, // 'e'
			{Kind: ir.OpCallDirect, Str: "__str_idx"},
			{Kind: ir.OpLoadByte},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "101" {
		t.Fatalf("str_idx heap = %q, want 101 ('e')", got)
	}
}

// TestEmitStrIdxInline — inline-form string (≤7 ASCII bytes):
// __str_idx spills (data, len) to the fixed scratch and returns
// scratch + i, so the caller's OpLoadByte reads the spilled byte.
func TestEmitStrIdxInline(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hi"},
			{Kind: ir.OpConstI32, I32: 1}, // 'i'
			{Kind: ir.OpCallDirect, Str: "__str_idx"},
			{Kind: ir.OpLoadByte},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "105" {
		t.Fatalf("str_idx inline = %q, want 105 ('i')", got)
	}
}

// TestEmitArrIdx — set up a length-prefixed 3-element i32 array
// at a known address, then __arr_idx(base, 1) returns base+4;
// load that to recover element 1.
func TestEmitArrIdx(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// Length prefix: mem[196] = 3 (3 elements)
			{Kind: ir.OpConstI32, I32: 196},
			{Kind: ir.OpConstI32, I32: 3},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// Element 0 at 200: 10
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI32, I32: 10},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// Element 1 at 204: 20
			{Kind: ir.OpConstI32, I32: 204},
			{Kind: ir.OpConstI32, I32: 20},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// Element 2 at 208: 30
			{Kind: ir.OpConstI32, I32: 208},
			{Kind: ir.OpConstI32, I32: 30},
			{Kind: ir.OpCallDirect, Str: "__store_i32"},
			// arr_idx(200, 1) → 204; load → 20
			{Kind: ir.OpConstI32, I32: 200},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpCallDirect, Str: "__arr_idx"},
			{Kind: ir.OpCallDirect, Str: "__load_i32"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "20" {
		t.Fatalf("arr_idx[1] = %q, want 20", got)
	}
}

// TestEmitReadLine — pipe stdin to wasmtime and verify the
// Option[string] heap box. Layout: tag at +0 (0=Some, 1=None),
// data ptr at +8, len at +12. Each case loads the tag and (for
// Some) the length, returning either the length or -1 (None).
func TestEmitReadLine(t *testing.T) {
	cases := []struct {
		label, stdin string
		want         string
	}{
		{"empty", "", "-1"},
		{"two-bytes-eof", "hi", "2"},
		{"one-line-newline", "hi\n", "3"},
		{"first-of-multi", "alpha\nbeta", "6"},
	}
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "read_line"},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoad}, // tag
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpEq},
			{Kind: ir.OpIf, I32: ir.BlockTypeI32},
			{Kind: ir.OpConstI32, I32: -1},
			{Kind: ir.OpElse},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 12},
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoad}, // len at +12
			{Kind: ir.OpEnd},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, c := range cases {
		c := c
		t.Run(c.label, func(t *testing.T) {
			cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
			cmd.Stdin = strings.NewReader(c.stdin)
			var so, se bytes.Buffer
			cmd.Stdout = &so
			cmd.Stderr = &se
			if err := cmd.Run(); err != nil {
				t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
			}
			lines := strings.Split(strings.TrimSpace(so.String()), "\n")
			got := lines[len(lines)-1]
			if got != c.want {
				t.Fatalf("read_line(%q) = %q, want %q", c.stdin, got, c.want)
			}
		})
	}
}

// TestEmitReadByte — pipe stdin to wasmtime and expect the
// first read_byte() call to return the ASCII code of the first
// byte. Reading 'A' (0x41) should produce "65" on stdout.
func TestEmitReadByte(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "read_byte"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	cmd.Stdin = strings.NewReader("A")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "65" {
		t.Fatalf("read_byte first call = %q, want 65 (ASCII 'A')", got)
	}
}

// TestEmitPreview2ReadByteUsesStreams — with Preview2WASI=true, a
// program that reads stdin via read_byte imports
// `wasi:cli/stdin@0.2.0::get-stdin` +
// `wasi:io/streams@0.2.0::[method]input-stream.blocking-read`
// instead of the preview-1 `wasi_snapshot_preview1::fd_read`.
func TestEmitPreview2ReadByteUsesStreams(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "read_byte"},
		},
	}}}
	bin, err := EmitWithOptions(prog, EmitOptions{Preview2WASI: true})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if importExists(t, bin, "wasi_snapshot_preview1", "fd_read") {
		t.Errorf("module still has preview-1 fd_read import under Preview2WASI=true")
	}
	if !importExists(t, bin, "wasi:cli/stdin@0.2.0", "get-stdin") {
		t.Errorf("module missing wasi:cli/stdin@0.2.0::get-stdin import under Preview2WASI=true")
	}
	if !importExists(t, bin, "wasi:io/streams@0.2.0", "[method]input-stream.blocking-read") {
		t.Errorf("module missing wasi:io/streams blocking-read import under Preview2WASI=true")
	}
}

// TestEmitPreview2ReadByteDefaultUsesFdRead — the default
// (Preview2WASI=false) path still reads stdin via the preview-1
// fd_read import. Pins the opt-in shape of the migration.
func TestEmitPreview2ReadByteDefaultUsesFdRead(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "read_byte"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !importExists(t, bin, "wasi_snapshot_preview1", "fd_read") {
		t.Errorf("default build missing preview-1 fd_read import")
	}
	if importExists(t, bin, "wasi:cli/stdin@0.2.0", "get-stdin") {
		t.Errorf("default build has preview-2 get-stdin import without opt-in")
	}
}

// TestEmitReadByteEOF — with empty stdin, the first read_byte
// returns -1 (EOF).
func TestEmitReadByteEOF(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "read_byte"},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	cmd.Stdin = strings.NewReader("") // immediate EOF
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "-1" {
		t.Fatalf("read_byte EOF = %q, want -1", got)
	}
}

// TestEmitReadByteSum — read 3 bytes and return their sum.
// Exercises the scratch-region reuse across multiple calls.
// Input "ABC" (65+66+67 = 198).
func TestEmitReadByteSum(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "read_byte"},
			{Kind: ir.OpCallDirect, Str: "read_byte"},
			{Kind: ir.OpAdd},
			{Kind: ir.OpCallDirect, Str: "read_byte"},
			{Kind: ir.OpAdd},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	cmd.Stdin = strings.NewReader("ABC")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "198" {
		t.Fatalf("3 read_bytes sum = %q, want 198 (65+66+67)", got)
	}
}

// TestEmitExit — call OpCallDirect "exit" with a specific code
// and verify wasmtime's exit code matches. The alias routes to
// __fern_exit which invokes wasi_proc_exit.
func TestEmitExit(t *testing.T) {
	for _, code := range []int{0, 7, 42} {
		code := code
		t.Run(fmt.Sprintf("code_%d", code), func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:       "_start",
				ReturnType: void(),
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: int32(code)},
					{Kind: ir.OpCallDirect, Str: "exit", I32: 1},
					// Unreachable but satisfies the verifier.
					{Kind: ir.OpReturnVoid},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Skip("wasmtime not on PATH")
			}
			dir := t.TempDir()
			p := filepath.Join(dir, "prog.wasm")
			if err := os.WriteFile(p, bin, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", p)
			if err := cmd.Run(); err != nil {
				// wasmtime wraps non-zero exit codes in an
				// *exec.ExitError; pull it out and compare.
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("wasmtime: %v (not ExitError)", err)
				}
				if exitErr.ExitCode() != code {
					t.Fatalf("exit code %d, want %d", exitErr.ExitCode(), code)
				}
				return
			}
			if code != 0 {
				t.Fatalf("expected exit code %d, got 0", code)
			}
		})
	}
}

// TestEmitRandomI32 — call OpCallDirect "random_i32" three times
// and confirm at least two of the values differ (the host runs
// wasi_random_get; the probability of three identical 32-bit
// values is 2^-64). Returns via wasmtime stdout as the function
// result.
func TestEmitRandomI32(t *testing.T) {
	mk := func(name string) *ir.Func {
		return &ir.Func{
			Name:       name,
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpCallDirect, Str: "random_i32"},
			},
		}
	}
	prog := &ir.Program{Funcs: []*ir.Func{
		mk("r1"), mk("r2"), mk("r3"),
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	a := runUnderWasmtime(t, bin, "r1")
	b := runUnderWasmtime(t, bin, "r2")
	c := runUnderWasmtime(t, bin, "r3")
	// All three identical means random_get isn't working.
	if a == b && b == c {
		t.Fatalf("three calls returned the same value %q — host random_get not active", a)
	}
}

// TestEmitPrintInlineLiteral — same as above but with a short
// (inline-form) literal. The byte-by-byte copy through
// __fern_str_byte handles the inline (data, len) packing.
func TestEmitPrintInlineLiteral(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "_start",
		ReturnType: void(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hi\n"},
			{Kind: ir.OpCallDirect, Str: "__fern_print", I32: 1},
			{Kind: ir.OpReturnVoid},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s\nstdout:%s", err, se.String(), so.String())
	}
	if got := so.String(); got != "hi\n\n" {
		t.Fatalf("stdout = %q, want %q", got, "hi\n\n")
	}
}

// TestImportSectionOnlyWhenPrintUsed — pure-arithmetic program
// must not include an import section; section id 2 should be
// absent.
func TestImportSectionOnlyWhenPrintUsed(t *testing.T) {
	progNoPrint := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 42}},
	}}}
	bin, err := Emit(progNoPrint)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if walkHasImportSection(t, bin) {
		t.Fatal("import section present in pure-arithmetic module")
	}
}

func walkHasImportSection(t *testing.T, bin []byte) bool {
	t.Helper()
	if len(bin) < 8 {
		return false
	}
	i := 8
	for i < len(bin) {
		id := bin[i]
		i++
		size := 0
		shift := 0
		for {
			if i >= len(bin) {
				return false
			}
			b := bin[i]
			i++
			size |= int(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if id == encode.SectionImport {
			return true
		}
		i += size
	}
	return false
}

// TestEmitMakeEnvSimple — OpMakeEnv with 3 i32 captures returns
// a heap pointer; reading mem[env_ptr + 4*i] for each i gets the
// original capture value back.
func TestEmitMakeEnvSimple(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:         "main",
		ScratchTypes: []ast.Type{i32()},
		ReturnType:   i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 10},
			{Kind: ir.OpConstI32, I32: 20},
			{Kind: ir.OpConstI32, I32: 30},
			{Kind: ir.OpMakeEnv, I32: 3, Str: "_anon"},
			{Kind: ir.OpStoreLocal, I32: 0},
			// env_ptr + 4 → cap[1]; load it (expect 20).
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoad},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "20" {
		t.Fatalf("env[1] = %q, want 20", got)
	}
}

// TestEmitMakeEnvAllCaptures — read every capture back from a
// 4-capture env block; confirms the per-capture stride + ordering
// matches the pop order.
func TestEmitMakeEnvAllCaptures(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	for i := 0; i < 4; i++ {
		i := i
		t.Run(fmt.Sprintf("cap_%d", i), func(t *testing.T) {
			prog := &ir.Program{Funcs: []*ir.Func{{
				Name:         "main",
				ScratchTypes: []ast.Type{i32()},
				ReturnType:   i32(),
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 100},
					{Kind: ir.OpConstI32, I32: 200},
					{Kind: ir.OpConstI32, I32: 300},
					{Kind: ir.OpConstI32, I32: 400},
					{Kind: ir.OpMakeEnv, I32: 4, Str: "_anon"},
					{Kind: ir.OpStoreLocal, I32: 0},
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpConstI32, I32: int32(4 * i)},
					{Kind: ir.OpAdd},
					{Kind: ir.OpLoad},
				},
			}}}
			bin, err := Emit(prog)
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			want := fmt.Sprintf("%d", 100*(i+1))
			if got := runUnderWasmtime(t, bin, "main"); got != want {
				t.Fatalf("cap[%d] = %q, want %q", i, got, want)
			}
		})
	}
}

// TestEmitMakeClosure — OpMakeClosure constructs an 8-byte pair
// {fn_idx, env_ptr}. Loading mem[pair+0] gives the funcidx;
// mem[pair+4] gives the env_ptr (which points at captures).
func TestEmitMakeClosure(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			Name:       "fnidx",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 7},
				{Kind: ir.OpMakeClosure, I32: 1, Str: "target"},
				{Kind: ir.OpLoad}, // fn_idx at pair+0
			},
		},
		{
			Name:       "envcap0",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 99},
				{Kind: ir.OpMakeClosure, I32: 1, Str: "target"},
				{Kind: ir.OpConstI32, I32: 4},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad}, // env_ptr at pair+4
				{Kind: ir.OpLoad}, // *env_ptr = cap[0] = 99
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// target is a closure target (referenced by OpMakeClosure)
	// so its wasm signature includes env_ptr. funcidx of
	// `target` is 0 (no imports).
	if got := runUnderWasmtime(t, bin, "fnidx"); got != "0" {
		t.Fatalf("fnidx = %q, want 0", got)
	}
	if got := runUnderWasmtime(t, bin, "envcap0"); got != "99" {
		t.Fatalf("envcap0 = %q, want 99", got)
	}
}

// TestEmitMakeClosureZeroCaptures — n=0 produces a valid pair
// with fn_idx readable; env_ptr is harmless.
func TestEmitMakeClosureZeroCaptures(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpMakeClosure, I32: 0, Str: "target"},
				{Kind: ir.OpLoad}, // fn_idx at pair+0
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "0" {
		t.Fatalf("got %q, want 0", got)
	}
}

// TestEmitMakeClosureRcHeader — Phase 1e-closures layout: the
// closure pair AND its env block are allocated via
// __fern_alloc_rc1, so each carries an 8-byte rc header with a
// live rc=1 at [data-8]. The data pointers (pair / env) are
// base+8, so the call-site +0 (fn_idx) / +4 (env_ptr) reads are
// unchanged. Read the rc words back to pin the header presence.
func TestEmitMakeClosureRcHeader(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			Captures:   []ast.Param{{Name: "x", Type: i32()}},
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			// rc word of the closure pair: [pair_ptr - 8] == 1.
			Name:       "pairRc",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 7}, // capture value
				{Kind: ir.OpMakeClosure, I32: 1, Str: "target"},
				{Kind: ir.OpConstI32, I32: -8},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad},
			},
		},
		{
			// rc word of the env block: [env_ptr - 8] == 1.
			Name:       "envRc",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 7},
				{Kind: ir.OpMakeClosure, I32: 1, Str: "target"},
				{Kind: ir.OpConstI32, I32: 4},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad}, // env_ptr at pair+4
				{Kind: ir.OpConstI32, I32: -8},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "pairRc"); got != "1" {
		t.Fatalf("closure pair rc = %q, want 1", got)
	}
	if got := runUnderWasmtime(t, bin, "envRc"); got != "1" {
		t.Fatalf("env block rc = %q, want 1", got)
	}
}

// TestEmitMakeEnvI64Capture — a target whose Captures list says
// `i64` must store 8 bytes per capture; reading it back with
// OpLoad Width=64 recovers the original i64 value.
func TestEmitMakeEnvI64Capture(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			Captures:   []ast.Param{{Name: "x", Type: i64()}},
			ReturnType: i64(),
			Ops:        []ir.Op{{Kind: ir.OpConstI64, I64: 0}},
		},
		{
			Name:         "main",
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   i64(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI64, I64: 0x1234_5678_9abc_def0},
				{Kind: ir.OpMakeEnv, I32: 1, Str: "target"},
				{Kind: ir.OpStoreLocal, I32: 0}, // env_ptr -> local 0
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpLoad, Width: 64}, // read i64 at env+0
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	want := fmt.Sprintf("%d", int64(0x1234_5678_9abc_def0))
	if got := runUnderWasmtime(t, bin, "main"); got != want {
		t.Fatalf("i64 capture = %q, want %q", got, want)
	}
}

// TestEmitMakeEnvF64Capture — f64 capture round-trips through the
// env block via OpFLoad Width=64.
func TestEmitMakeEnvF64Capture(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			Captures:   []ast.Param{{Name: "x", Type: f64()}},
			ReturnType: f64(),
			Ops:        []ir.Op{{Kind: ir.OpConstF64, F64: 0}},
		},
		{
			Name:         "main",
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   f64(),
			Ops: []ir.Op{
				{Kind: ir.OpConstF64, F64: 3.14159},
				{Kind: ir.OpMakeEnv, I32: 1, Str: "target"},
				{Kind: ir.OpStoreLocal, I32: 0},
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpFLoad, Width: 64},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := runUnderWasmtime(t, bin, "main")
	if !strings.HasPrefix(got, "3.14") {
		t.Fatalf("f64 capture = %q, want stdout starting with 3.14", got)
	}
}

// TestEmitMakeEnvStringCapture — string capture lays out as
// two i32 slots (data at +0, len at +4). Reading the len back
// confirms both halves landed.
func TestEmitMakeEnvStringCapture(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name:       "target",
			Captures:   []ast.Param{{Name: "s", Type: strType()}},
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			Name:         "main",
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   i32(),
			Ops: []ir.Op{
				// Push the (data, len) pair of "hello". "hello" is
				// 5 bytes ASCII so it lands in inline form: data
				// = 'hell' packed, len = 0x8500006F (inline flag
				// + length=5 at bits 24..26 + 'o' at bits 0..7).
				{Kind: ir.OpConstStr, Str: "hello"},
				{Kind: ir.OpMakeEnv, I32: 1, Str: "target"},
				{Kind: ir.OpStoreLocal, I32: 0},
				// Read len from env+4, then extract the byte-count
				// field (bits 24..26): (len & 0x07000000) >> 24.
				// Masking before the shift keeps the top bit
				// (inline flag) and the byte-5 field out of the
				// signed-shift result.
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpConstI32, I32: 4},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad},
				{Kind: ir.OpConstI32, I32: 0x07000000},
				{Kind: ir.OpAnd},
				{Kind: ir.OpConstI32, I32: 24},
				{Kind: ir.OpShrS},
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "main"); got != "5" {
		t.Fatalf("string capture len = %q, want 5", got)
	}
}

// TestEmitMakeEnvMixedCaptures — i32 + i64 + i32 in that order
// must lay out at offsets 0, 4, 12 (4 + 8 stride). Reading each
// back verifies the per-capture stride is honored.
func TestEmitMakeEnvMixedCaptures(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name: "target",
			Captures: []ast.Param{
				{Name: "a", Type: i32()},
				{Name: "b", Type: i64()},
				{Name: "c", Type: i32()},
			},
			ReturnType: i32(),
			Ops:        []ir.Op{{Kind: ir.OpConstI32, I32: 0}},
		},
		{
			Name:         "read_a",
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 11},
				{Kind: ir.OpConstI64, I64: 22},
				{Kind: ir.OpConstI32, I32: 33},
				{Kind: ir.OpMakeEnv, I32: 3, Str: "target"},
				{Kind: ir.OpStoreLocal, I32: 0},
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpLoad}, // env+0 = i32 a
			},
		},
		{
			Name:         "read_b",
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   i64(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 11},
				{Kind: ir.OpConstI64, I64: 22},
				{Kind: ir.OpConstI32, I32: 33},
				{Kind: ir.OpMakeEnv, I32: 3, Str: "target"},
				{Kind: ir.OpStoreLocal, I32: 0},
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpConstI32, I32: 4},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad, Width: 64}, // env+4 = i64 b
			},
		},
		{
			Name:         "read_c",
			ScratchTypes: []ast.Type{i32()},
			ReturnType:   i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 11},
				{Kind: ir.OpConstI64, I64: 22},
				{Kind: ir.OpConstI32, I32: 33},
				{Kind: ir.OpMakeEnv, I32: 3, Str: "target"},
				{Kind: ir.OpStoreLocal, I32: 0},
				{Kind: ir.OpLoadLocal, I32: 0},
				{Kind: ir.OpConstI32, I32: 12},
				{Kind: ir.OpAdd},
				{Kind: ir.OpLoad}, // env+12 = i32 c
			},
		},
	}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := runUnderWasmtime(t, bin, "read_a"); got != "11" {
		t.Fatalf("read_a = %q, want 11", got)
	}
	if got := runUnderWasmtime(t, bin, "read_b"); got != "22" {
		t.Fatalf("read_b = %q, want 22", got)
	}
	if got := runUnderWasmtime(t, bin, "read_c"); got != "33" {
		t.Fatalf("read_c = %q, want 33", got)
	}
}

// TestEmitUnsupportedOpReports — pass an op the slice doesn't
// cover (e.g. OpMakeClosure) and confirm we get a useful error
// rather than emitting nonsense bytes. Regressions here would
// silently produce invalid wasm.
func TestEmitUnsupportedOpReports(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpMakeClosure}},
	}}}
	if _, err := Emit(prog); err == nil {
		t.Fatalf("expected unsupported-op error, got nil")
	}
}

// TestEmitExternScalarImport — a scalar `@import` extern (P4b) lowers to a
// core wasm function import of its (interface, wit-name) and a call resolves
// there. The emitted module must carry the import.
func TestEmitExternScalarImport(t *testing.T) {
	prog := &ir.Program{
		Externs: []*ir.ExternFunc{{
			Name:       "random_u64",
			Iface:      "wasi:random/random@0.2.0",
			WITName:    "get-random-u64",
			ReturnType: i64(),
		}},
		Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpCallDirect, Str: "random_u64"},
				{Kind: ir.OpDrop},
				{Kind: ir.OpConstI32, I32: 0},
			},
		}},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.Contains(bin, []byte("wasi:random/random@0.2.0")) || !bytes.Contains(bin, []byte("get-random-u64")) {
		t.Fatalf("emitted module missing the extern import")
	}
}

// TestEmitExternStringResult — a string/list<u8>-returning `@import` extern
// (P4c) emits the raw import (with a trailing return-area pointer) and the
// module exports cabi_realloc so the host can materialize the returned bytes.
func TestEmitExternStringResult(t *testing.T) {
	prog := &ir.Program{
		Externs: []*ir.ExternFunc{{
			Name:       "random_bytes",
			Iface:      "wasi:random/random@0.2.0",
			WITName:    "get-random-bytes",
			Params:     []ast.Param{{Name: "n", Type: i64()}},
			ReturnType: ast.StringType{},
		}},
		Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI64, I64: 8},
				{Kind: ir.OpCallDirect, Str: "random_bytes"},
				// result is a string pair; drop both halves.
				{Kind: ir.OpDrop},
				{Kind: ir.OpDrop},
				{Kind: ir.OpConstI32, I32: 0},
			},
		}},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.Contains(bin, []byte("wasi:random/random@0.2.0")) || !bytes.Contains(bin, []byte("get-random-bytes")) {
		t.Fatalf("emitted module missing the extern import")
	}
	if !bytes.Contains(bin, []byte("cabi_realloc")) {
		t.Fatalf("string-result extern must export cabi_realloc for the host to allocate the returned bytes")
	}
}

// TestEmitExternU8ArrayResult — a list<u8>-returning extern declared as `u8[]`
// emits the raw import (trailing return-area pointer) + cabi_realloc export,
// like the string-result case but lifting into an array.
func TestEmitExternU8ArrayResult(t *testing.T) {
	prog := &ir.Program{
		Externs: []*ir.ExternFunc{{
			Name:       "rand_bytes",
			Iface:      "wasi:random/random@0.2.0",
			WITName:    "get-random-bytes",
			Params:     []ast.Param{{Name: "n", Type: i64()}},
			ReturnType: ast.ArrayType{Elem: ast.NumberType{Width: 8}},
		}},
		Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI64, I64: 4},
				{Kind: ir.OpCallDirect, Str: "rand_bytes"},
				{Kind: ir.OpDrop},
				{Kind: ir.OpConstI32, I32: 0},
			},
		}},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.Contains(bin, []byte("get-random-bytes")) {
		t.Fatalf("emitted module missing the extern import")
	}
	if !bytes.Contains(bin, []byte("cabi_realloc")) {
		t.Fatalf("u8[]-result extern must export cabi_realloc")
	}
}

// TestEmitExternBoolArrayResult — a bool[] result (canonical list<bool>) is
// accepted and lowers through the byte-expanding wrapper (the raw import gains a
// trailing return-area pointer; cabi_realloc is exported for the host).
func TestEmitExternBoolArrayResult(t *testing.T) {
	prog := &ir.Program{
		Externs: []*ir.ExternFunc{{
			Name:       "get_bits",
			Iface:      "local:test/src@0.1.0",
			WITName:    "bits",
			Params:     []ast.Param{{Name: "n", Type: ast.NumberType{Width: 32}}},
			ReturnType: ast.ArrayType{Elem: ast.BoolType{}},
		}},
		Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 4},
				{Kind: ir.OpCallDirect, Str: "get_bits"},
				{Kind: ir.OpDrop},
				{Kind: ir.OpConstI32, I32: 0},
			},
		}},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.Contains(bin, []byte("bits")) {
		t.Fatalf("emitted module missing the extern import")
	}
	if !bytes.Contains(bin, []byte("cabi_realloc")) {
		t.Fatalf("bool[]-result extern must export cabi_realloc")
	}
}

// TestEmitExternCompositeRejected — extern types beyond the supported set
// (composite parameters, array/record results) need canonical-ABI marshalling
// that isn't built yet, so they're rejected up front with a clear P4c message
// rather than emitting slots that don't match the host's ABI.
func TestEmitExternCompositeRejected(t *testing.T) {
	mk := func(params []ast.Param, ret ast.Type) *ir.Program {
		return &ir.Program{
			Externs: []*ir.ExternFunc{{
				Name:       "do_thing",
				Iface:      "wasi:foo/bar@0.1.0",
				WITName:    "do-thing",
				Params:     params,
				ReturnType: ret,
			}},
			Funcs: []*ir.Func{{
				Name:       "main",
				ReturnType: i32(),
				Ops:        []ir.Op{{Kind: ir.OpCallDirect, Str: "do_thing"}},
			}},
		}
	}
	cases := map[string]*ir.Program{
		// Numeric arrays of any width (u8[]…i64[]/f64[]) ARE accepted now (P4c),
		// and so are bool[] params (byte-repacked) AND bool[] *results*
		// (byte-expanded — buildExternBoolListResultWrapper). What's still
		// rejected: a string parameter alongside a composite (string) result,
		// which would need both marshalling directions at once.
		"string param + str result": mk([]ast.Param{{Name: "s", Type: ast.StringType{}}}, ast.StringType{}),
	}
	for name, prog := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Emit(prog)
			if err == nil {
				t.Fatalf("expected a P4c rejection, got nil")
			}
			if !strings.Contains(err.Error(), "P4c") {
				t.Fatalf("error should mention P4c, got: %v", err)
			}
		})
	}
}

// TestEmitExternStringParam — an extern with a string parameter and a scalar
// result (P4c) emits the raw import with the string flattened to (ptr,len) and
// resolves the Fern call to a normalizing wrapper.
func TestEmitExternStringParam(t *testing.T) {
	prog := &ir.Program{
		Externs: []*ir.ExternFunc{{
			Name:       "sink_set",
			Iface:      "local:test/sink@0.1.0",
			WITName:    "set",
			Params:     []ast.Param{{Name: "s", Type: ast.StringType{}}},
			ReturnType: ast.VoidType{},
		}},
		Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstStr, Str: "hi"},
				{Kind: ir.OpCallDirect, Str: "sink_set"},
				{Kind: ir.OpConstI32, I32: 0},
			},
		}},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.Contains(bin, []byte("local:test/sink@0.1.0")) || !bytes.Contains(bin, []byte("set")) {
		t.Fatalf("emitted module missing the string-param extern import")
	}
}

// TestEmitExternListU8Param — an extern with a `u8[]` parameter and a scalar
// result (P4c) emits the raw import with the list flattened to the canonical
// (ptr,len) and resolves the Fern call to a forwarding wrapper. Unlike a
// string, a Fern `u8[]` is one slot on the Fern side but two on the canonical
// side, so this exercises canonicalExternParamValtypes.
func TestEmitExternListU8Param(t *testing.T) {
	u8arr := ast.ArrayType{Elem: ast.NumberType{Width: 8}}
	prog := &ir.Program{
		Externs: []*ir.ExternFunc{{
			Name:       "sink_sum",
			Iface:      "local:test/sink@0.1.0",
			WITName:    "sum-bytes",
			Params:     []ast.Param{{Name: "data", Type: u8arr}},
			ReturnType: ast.NumberType{Width: 32},
		}},
		Funcs: []*ir.Func{{
			Name:       "main",
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 0}, // u8[] element pointer (placeholder arg)
				{Kind: ir.OpCallDirect, Str: "sink_sum"},
				{Kind: ir.OpDrop}, // discard the u32 result
				{Kind: ir.OpConstI32, I32: 0},
			},
		}},
	}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.Contains(bin, []byte("local:test/sink@0.1.0")) || !bytes.Contains(bin, []byte("sum-bytes")) {
		t.Fatalf("emitted module missing the u8[]-param extern import")
	}
}

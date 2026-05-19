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

// TestEmitIfElse — `function pick(a, b: i32): i32 {
//   if a > b { return a } else { return b }
// }`. Uses `if (result i32)` with branches that push the chosen
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

// TestEmitLoopBr — `function sum(n: i32): i32 {
//   var acc = 0; var i = 0;
//   loop {
//     if !(i < n) break;
//     acc = acc + i;
//     i = i + 1;
//     continue;
//   }
//   return acc;
// }` — using the wasm block+loop+br_if idiom. Exercises OpBlock,
// OpLoop, OpBr (back-edge), OpBrIf (forward exit).
//
// Shape (label depths in parens):
//   block (void)         ; label 1 (forward exit)
//     loop (void)         ; label 0 (back-edge target)
//       i < n  →  br_if to label 0 if FALSE? No — we want:
//       i >= n →  br to label 1 (exit outer)
//     end loop
//   end block
//
// Concretely:
//   block
//     loop
//       local.get i
//       local.get n
//       i32.ge_s
//       br_if 1            ; if i >= n, exit outer block
//       local.get acc
//       local.get i
//       i32.add
//       local.set acc
//       local.get i
//       i32.const 1
//       i32.add
//       local.set i
//       br 0               ; loop back
//     end
//   end
//   local.get acc
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
			{Kind: ir.OpBrIf, I32: 1}, // exit outer block
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

// TestEmitBlocktypeUnsupported — string-pair blocktype isn't
// covered yet; confirm we report it rather than emitting bytes.
func TestEmitBlocktypeUnsupported(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "f",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpBlock, I32: ir.BlockTypeStringPair}},
	}}}
	if _, err := Emit(prog); err == nil {
		t.Fatal("expected error for string-pair blocktype")
	}
}

// TestEmitConversions — width conversions (extend / wrap),
// float↔int (convert / trunc), float demote/promote, sign-extend
// of sub-i32 widths, and reinterpret. One sub-test per family;
// the inner computation builds a value of the source type, runs
// the conversion, and returns the result in a way that lets
// wasmtime print it verifiably.
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
		{"sign_extend_8_negative", i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0xff}, // 0xff → -1 when sign-extended from i8
			{Kind: ir.OpSignExtend8},
		}, "-1"},
		{"sign_extend_16_negative", i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0xffff}, // 0xffff → -1 when sign-extended from i16
			{Kind: ir.OpSignExtend16},
		}, "-1"},
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
		{"i8_store_load_u", i32(), i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpStoreI8},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadByte}, // load8_u
		}, []string{"200"}, "200"}, // 200 fits unsigned byte
		{"i8_store_load_s_negative", i32(), i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpStoreI8},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadI8S}, // sign-extending load
		}, []string{"-3"}, "-3"},
		{"i16_store_load_s_negative", i32(), i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpStoreI16},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadI16S}, // sign-extending load
		}, []string{"-100"}, "-100"},
		{"i16_store_load_u", i32(), i32(), []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpStoreI16},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadI16U},
		}, []string{"50000"}, "50000"},
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

// TestEmitRecursion — `function fact(n: i32): i32 {
//   if n <= 1 { return 1 } else { return n * fact(n - 1) }
// }`. Direct self-call. Exercises call into the same funcidx,
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

// TestEmitCallIndirect — three functions of signature (i32) → i32
// in the table, with the caller picking a funcidx by parameter and
// dispatching via call_indirect. Exercises the table/element
// section emission AND op.Sig → typeidx resolution.
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
		// funcidx 0: double
		mk("double", []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpMul},
		}),
		// funcidx 1: negate
		mk("negate", []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpSub},
		}),
		// funcidx 2: dispatch — takes (funcidx, value) and
		// calls the chosen function with value, returning the
		// result. Stack on call: [value, funcidx]; we'll push
		// in that order.
		{
			Name: "dispatch",
			Params: []ast.Param{
				{Name: "which", Type: i32()},
				{Name: "v", Type: i32()},
			},
			ReturnType: i32(),
			Ops: []ir.Op{
				{Kind: ir.OpLoadLocal, I32: 1}, // v
				{Kind: ir.OpLoadLocal, I32: 0}, // funcidx
				{Kind: ir.OpCallIndirect, Sig: sigI32I32},
			},
		},
	}}
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
		which, v, want string
	}{
		{"0", "21", "42"},  // double(21) = 42
		{"1", "100", "-100"}, // negate(100) = -100
	} {
		cmd := exec.Command("wasmtime", "run", "--invoke", "dispatch", p, c.which, c.v)
		var so, se bytes.Buffer
		cmd.Stdout = &so
		cmd.Stderr = &se
		if err := cmd.Run(); err != nil {
			t.Fatalf("dispatch(%s,%s): %v\nstderr:%s", c.which, c.v, err, se.String())
		}
		if got := strings.TrimSpace(so.String()); got != c.want {
			t.Fatalf("dispatch(%s,%s) = %q, want %q", c.which, c.v, got, c.want)
		}
	}
}

// TestEmitCallClosureDirect — OpCallClosureDirect is the
// defunctionalised form: env_ptr is already on the stack as the
// last arg, so it's a plain `call funcidx`. Verify it routes to
// the named target.
func TestEmitCallClosureDirect(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{
		{
			Name: "doubled",
			Params: []ast.Param{
				{Name: "v", Type: i32()},
				{Name: "env", Type: i32()},
			},
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

// TestEmitConstStrInlineForm — short ASCII string (≤7 bytes) takes
// the inline-form path via langstring.PackInlineWasm: no data
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
	if string(segs[0]) != "shared-literal-string" {
		t.Fatalf("segment 0: got %q, want %q", segs[0], "shared-literal-string")
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
				{Kind: ir.OpLoadLocal, I32: 0}, // push (data, len)
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
//   IR slot 0: i32 param      → wasm slot 0
//   IR slot 1: string param   → wasm slots 1, 2 (data, len)
//   IR slot 2: i32 param      → wasm slot 3
//   IR slot 3: string local   → wasm slots 4, 5
//   IR slot 4: i32 scratch    → wasm slot 6
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
			{Kind: ir.OpLoadLocal, I32: 4},  // scratch (len)
			{Kind: ir.OpLoadLocal, I32: 0},  // a
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoadLocal, I32: 2},  // c
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
// the else arm of __lang_str_len: top bit of $len is 0, so the
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
// of __lang_str_len: extract bits 24..26.
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
// the transition point: both branches of __lang_str_len give the
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
		t.Fatalf("function-section count = %d, want 2 (main + __lang_str_len)", count2)
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

// TestEmitUnsupportedOpReports — pass an op the slice doesn't
// cover (e.g. OpAlloc) and confirm we get a useful error rather
// than emitting nonsense bytes. Regressions here would silently
// produce invalid wasm.
func TestEmitUnsupportedOpReports(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops:        []ir.Op{{Kind: ir.OpAlloc}},
	}}}
	if _, err := Emit(prog); err == nil {
		t.Fatalf("expected unsupported-op error, got nil")
	}
}

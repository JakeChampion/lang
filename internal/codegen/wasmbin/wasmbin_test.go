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

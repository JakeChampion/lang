package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedArrayIRCases pin array-of-arrays element reads (`T[][]`) to the self-host
// IR path on x86-64 + wasm. A nested array element is a leak-only heap-array
// pointer (one slot, like a struct/string/tuple element), so `m[i][j]` lowers
// through the IR path and the module stays IR-eligible. This shape was previously
// only exercised indirectly (string-array byte indexing in the csv pin, `f[2][0]`,
// which is a string-array + byte load — not a true T[][] element load), so the
// genuine nested-array load had no dedicated routing pin.
//
// Covers i32[][], string[][], and the 8-byte inner element kinds i64[][] /
// f64[][]. The i64/f64 nested element read previously truncated to 32 bits
// (silent miscompile): local_is_arrarr recorded only THAT a slot was T[][], not
// what T was, so a nested read m[i][j] couldn't recover the inner width. #2691
// adds local_arrarr_elem (the inner kind) so arr_index_is_i64/_f64 pick the
// 8-byte load. Each case is oracle-checked against the interpreter and returns
// <= 126 (wasmtime exit-code truncation, cf. #2908). Mirrors
// self_host_nested_tuple_ir_test.go.
var nestedArrayIRCases = []struct {
	name string
	main string
}{
	// Read across two rows of a 2x2 i32[][]: m[0][1] + m[1][0] = 2 + 3 = 5.
	{"read-2x2", `function main(): i32 { var m: i32[][] = [[1, 2], [3, 4]]; return m[0][1] + m[1][0]; }`},
	// Outer + inner length: m.len() (2) + m[0].len() (3) = 5.
	{"row-len", `function main(): i32 { var m: i32[][] = [[1, 2, 3], [4, 5]]; return m.len() + m[0].len(); }`},
	// Loop-sum every element via per-row binding: 1+2+3+4+5+6 = 21.
	{"loop-sum", `function main(): i32 { var m: i32[][] = [[1, 2], [3, 4], [5, 6]]; var s = 0; var i = 0; while (i < m.len()) { var r = m[i]; var j = 0; while (j < r.len()) { s = s + r[j]; j = j + 1; } i = i + 1; } return s; }`},
	// string[][]: nested string elements round-trip; sum their byte lengths.
	// "ab"(2) + "c"(1) + "de"(2) = 5.
	{"string-nested", `function main(): i32 { var g: string[][] = [["ab", "c"], ["de"]]; return g[0][0].len() + g[0][1].len() + g[1][0].len(); }`},
	// f64[][] direct nested read — the parallel f64 8-byte fix. 2.5 * 2 = 5.
	{"f64-2x2", `function main(): i32 { var m: f64[][] = [[2.5], [3.5]]; return (m[0][0] * 2.0) as i32; }`},
}

// nestedArrayI64IRCases pin the i64[][] read AND construction fixes on x86-64 +
// wasm. The READ side (local_arrarr_elem → arr_get_i64) and the CONSTRUCTION side
// both now work on every backend: previously an i64 array LITERAL (`[5000000000]`)
// emitted its 64-bit element as `i32.const` on the wasm backend (out of range, so
// the module wouldn't parse), because infer_expr_width treated every bare integer
// literal as i32. infer_expr_width now classifies a literal that exceeds i32-max as
// i64 by value (it has no valid i32 reading — checker E047), so the inner array
// literal takes the arr_make_i64 path and the element is emitted i64.const.
// (An UNANNOTATED 1-D big-literal array — `var a = [7000000000]` — would exercise
// the same compiler path, but the tree-walking interpreter still defaults an
// unannotated big literal to i32 and wraps, so it can't serve as an oracle here;
// that checker/interp divergence is tracked separately. The annotated cases below
// pin the construction fix unambiguously.)
var nestedArrayI64IRCases = []struct {
	name string
	main string
}{
	// i64[][] direct nested read — the 8-byte element must NOT truncate to 32 bits.
	// 5000000000 / 1e9 = 5.
	{"i64-2x2", `function main(): i32 { var m: i64[][] = [[5000000000], [6000000000]]; return (m[0][0] / 1000000000) as i32; }`},
	// i64[][] via a row binding (var r = m[0]; r[j]) — r tracked as i64[]. 5+1 = 6.
	{"i64-row-binding", `function main(): i32 { var m: i64[][] = [[5000000000, 1000000000], [2000000000]]; var r = m[0]; return (r[0] / 1000000000) as i32 + (r[1] / 1000000000) as i32; }`},
	// i64[][] via nested for-in (for row in m { for x in row }). (5+6)e9 / 1e9 = 11.
	{"i64-forin", `function main(): i32 { var m: i64[][] = [[5000000000], [6000000000]]; var t: i64 = 0; for row in m { for x in row { t = t + x; } } return (t / 1000000000) as i32; }`},
}

// nestedArrayStructIRCases pin field/method access on a struct/enum element of an
// array-of-arrays (`a[i][j].field`, `a[i][j].method()`) to the self-host IR path on
// x86-64 + wasm. Value lowering of `a[i][j]` already worked (the temp-bound form
// `var p = a[i][j]; p.x` lowers), but `expr_struct_type` couldn't recover the
// element type for a doubly-indexed `a[i][j]` (its ExprIndex arm only matched an
// ExprIdent/ExprFieldAccess array, never a nested ExprIndex), so the field-read and
// method-dispatch paths bailed to the AST emitter. #2691 adds the depth-2 ExprIndex
// case so the innermost struct/enum element type recorded on the T[][] slot is
// recovered. Each case is oracle-checked against the interpreter and returns <= 126.
var nestedArrayStructIRCases = []struct {
	name string
	main string
}{
	// Two field reads off a struct element of a P[][]. 3 + 4 = 7.
	{"struct-field", `struct P{x:i32,y:i32} function main(): i32 { var a: P[][] = [[P{x:3,y:4}]]; return a[0][0].x + a[0][0].y; }`},
	// Variable indices into the nested struct array. a[0][1].x = 6.
	{"struct-field-var-idx", `struct P{x:i32} function main(): i32 { var a: P[][] = [[P{x:5},P{x:6}]]; var i = 0; var j = 1; return a[i][j].x; }`},
	// Method dispatch on the struct element. 3*2 = 6.
	{"struct-method", `struct P{x:i32} function (p: P) val(): i32 { return p.x*2; } function main(): i32 { var a: P[][] = [[P{x:3}]]; return a[0][0].val(); }`},
	// 8-byte i64 field on the nested element (field read picks the 8-byte load). 5.
	{"struct-i64-field", `struct P{x:i64} function main(): i32 { var a: P[][] = [[P{x:5000000000}]]; return (a[0][0].x / 1000000000) as i32; }`},
	// Method dispatch on an enum element of a Sh[][] (`<Enum>.<method>`). 3*3 = 9.
	{"enum-method", `enum Sh{C(i32)} function (s: Sh) area(): i32 { match(s){Sh.C(r)=>{return r*r;}} } function main(): i32 { var a: Sh[][] = [[Sh.C(3)]]; return a[0][0].area(); }`},
}

// TestSelfHostNestedArrayIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedArrayIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	// x86-64 runs the cross-backend cases plus the i64[][] cases (both the read
	// and construction fixes; the wasm half now runs them too) plus the struct/enum
	// nested-element field/method cases.
	x86Cases := append(append(append([]struct {
		name string
		main string
	}{}, nestedArrayIRCases...), nestedArrayI64IRCases...), nestedArrayStructIRCases...)

	for _, tc := range x86Cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostNestedArrayIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostNestedArrayIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-array wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// wasm runs the cross-backend cases plus the i64[][] cases — the i64-literal
	// construction fix (infer_expr_width value-based i64) makes these build here too —
	// plus the struct/enum nested-element field/method cases.
	wasmCases := append(append(append([]struct {
		name string
		main string
	}{}, nestedArrayIRCases...), nestedArrayI64IRCases...), nestedArrayStructIRCases...)

	for _, tc := range wasmCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "nested_array_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested-array wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

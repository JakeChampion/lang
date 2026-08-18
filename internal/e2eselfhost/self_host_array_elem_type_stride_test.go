package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An array literal's element slot width came from the FIRST ELEMENT'S
// EXPRESSION SHAPE (`expr_is_f64(arr.elements[0])`) instead of the element
// TYPE. Every destination the settle pass did not reach therefore built the
// literal for i32 slots while every reader used the declared element width —
// wrong values, not a crash (#4366):
//
//	pick([1, 2]) on `pick(xs: i64[])`  → wasm read 0, want 2
//	Holder { xs: [1, 2] } on i64[]     → wasm read 1, want 2
//	b.pick([1, 2]) on `f64[]`          → 0 on ALL THREE backends: the literals
//	                                     were never settled to floats either
//	lib.pick([1, 2]) across a module   → 0 on all three
//
// The layout now comes from `parser.ExprArray.elem_ty`, the destination's
// element type stamped by settle_to_type — the fact native sizes elements from
// (ast.ElemSizeBytesFor on ast.ArrayLit.ElemType). The expression probe survives
// only as the fallback for a literal no destination reaches.
//
// The register backends give every slot 8 bytes, so the pure-stride cases were
// wasm-only; the unsettled-float cases were wrong everywhere. Both legs are
// pinned here, plus arm64.
var arrayElemTypeStrideCases = []struct {
	name  string
	files map[string]string
}{
	// Free-function parameter: the destination the parser settle pass reaches
	// but only stamped floats for — an i64[] literal stayed i32-strided.
	{"free_fn_i64_param", map[string]string{"main.fern": `function pick(xs: i64[]): i64 { return xs[1]; }
function main(): i32 { var v: i64 = pick([1, 2]); return (v as i32) + 10; }`}},
	// Struct-literal field.
	{"struct_field_i64", map[string]string{"main.fern": `struct Holder { xs: i64[] }
function main(): i32 { var h: Holder = Holder { xs: [1, 2] }; return (h.xs[1] as i32) + 10; }`}},
	// Method argument on an annotated receiver: nothing settled these at all,
	// so the f64 case lowered integer elements into an f64-read array.
	{"method_arg_f64", map[string]string{"main.fern": `struct Bag { tag: i32 }
function (b: Bag) pick(xs: f64[]): f64 { return xs[1]; }
function main(): i32 { var b: Bag = Bag { tag: 0 }; var v: f64 = b.pick([1, 2]); return (v as i32) + 10; }`}},
	{"method_arg_i64", map[string]string{"main.fern": `struct Bag { tag: i32 }
function (b: Bag) pick(xs: i64[]): i64 { return xs[1]; }
function main(): i32 { var b: Bag = Bag { tag: 0 }; var v: i64 = b.pick([1, 2]); return (v as i32) + 10; }`}},
	// Mixed `[1, 2.5]`: the unsettled integer element made the wasm module
	// itself invalid (i32.store of an f64), not merely wrong.
	{"method_arg_f64_mixed", map[string]string{"main.fern": `struct Bag { tag: i32 }
function (b: Bag) pick(xs: f64[]): f64 { return xs[1]; }
function main(): i32 { var b: Bag = Bag { tag: 0 }; var v: f64 = b.pick([1, 2.5]);
    if (v > 2.4 && v < 2.6) { return 7; }
    return 1; }`}},
	// Across a module boundary: at parse time the callee's signature is not in
	// the module being settled, so the merge has to re-settle.
	{"cross_module", map[string]string{
		"lib64.fern": `pub function pick(xs: i64[]): i64 { return xs[1]; }
pub function pickf(xs: f64[]): f64 { return xs[1]; }`,
		"main.fern": `import "./lib64";
function main(): i32 {
    var v: i64 = lib64.pick([1, 2]);
    var f: f64 = lib64.pickf([1, 2]);
    return (v as i32) + (f as i32) * 10;
}`}},
	// The other element types the stamp now decides, in one program: u64 and
	// nested i64[][] take the 8-byte path, u8 / string / boolean the 4-byte one.
	{"elem_type_sweep", map[string]string{"main.fern": `function pu(xs: u64[]): u64 { return xs[1]; }
function pb(xs: u8[]): u8 { return xs[2]; }
function ps(xs: string[]): string { return xs[1]; }
function pl(xs: boolean[]): boolean { return xs[1]; }
function pn(m: i64[][]): i64 { return m[1][0]; }
function main(): i32 {
    var r: i32 = 0;
    if (pu([1, 2]) == (2 as u64)) { r = r + 1; }
    if (pb([1, 2, 3]) == (3 as u8)) { r = r + 2; }
    if (ps(["a", "b"]) == "b") { r = r + 4; }
    if (pl([true, false]) == false) { r = r + 8; }
    if (pn([[1, 2], [30, 4]]) == (30 as i64)) { r = r + 16; }
    return r;
}`}},
	// f32 rides the f64 slot in the self-host (8 bytes where native uses 4).
	// That is a footprint divergence, not a value one — this pins the values so
	// the day the distinct f32 slot lands it cannot silently truncate them.
	{"f32_values_roundtrip", map[string]string{"main.fern": `function main(): i32 {
    var a: f32 = 16777216.0 as f32;
    var b: f32 = 1.0 as f32;
    var xs: f32[] = [a + b, b, a];
    var r: i32 = 0;
    if (xs[0] == a) { r = r + 1; }
    if (xs[1] == b) { r = r + 2; }
    if (xs[2] == a) { r = r + 4; }
    if (xs.len() == 3) { r = r + 8; }
    return r;
}`}},
	// A TYPE-PARAMETER destination stamps "T", which names no slot width. The
	// stamp must not out-vote the expression probe there, or an f64 literal
	// passed to `first[T](xs: T[])` would be built for i32 slots while its
	// elements lower as doubles.
	{"generic_param_falls_back_to_probe", map[string]string{"main.fern": `function first[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var v: f64 = first([2.5, 1.5]);
    var w: i64 = first([(7 as i64), (8 as i64)]);
    return (v as i32) + (w as i32) * 10;
}`}},
	// Controls: destinations that were already type-driven and must stay so.
	{"annotated_var_i64", map[string]string{"main.fern": `function main(): i32 {
    var xs: i64[] = [1, 2];
    return (xs[1] as i32) + 10;
}`}},
	{"return_position_i64", map[string]string{"main.fern": `function mk(): i64[] { return [1, 2]; }
function main(): i32 { var xs: i64[] = mk(); return (xs[1] as i32) + 10; }`}},
}

// writeArrayElemCase writes a case's files into a fresh dir and returns the
// entry path.
func writeArrayElemCase(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return filepath.Join(dir, "main.fern")
}

// arrayElemOracle runs the case under the native interpreter — the oracle every
// leg is compared against.
func arrayElemOracle(t *testing.T, interpBin string, files map[string]string) int {
	t.Helper()
	cmd := exec.Command(interpBin, "-interp", writeArrayElemCase(t, files))
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("interpreter did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

// arrayElemFernDriver builds the self-hosted `fern` driver — the production
// compiler, which is what routes a multi-module program through the merge where
// the cross-module case is settled.
func arrayElemFernDriver(t *testing.T) (fernBin, stdlibRoot string, x86runner []string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return buildSelfHostBin(t, gcc, dir, "fern.fern", "fern"), root, runner
}

// TestSelfHostArrayElemTypeStrideWasm is the leg the stride half of the bug was
// on: wasm sizes an element slot from the literal's chosen width, so a literal
// built for i32 slots and read at 8 bytes returns garbage.
func TestSelfHostArrayElemTypeStrideWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping array-element stride wasm cases")
	}
	fernBin, stdlibRoot, _ := arrayElemFernDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range arrayElemTypeStrideCases {
		t.Run(tc.name, func(t *testing.T) {
			want := arrayElemOracle(t, interpBin, tc.files)
			mainPath := writeArrayElemCase(t, tc.files)
			outWat := filepath.Join(filepath.Dir(mainPath), "out.wat")
			if out, cerr := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (wasm) = %d, want %d (interp oracle) — element slot width must come from the element type", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostArrayElemTypeStrideX86_64 pins the register backend. Its slots are
// 8 bytes regardless, so it never had the stride half — but the unsettled-float
// cases (`b.pick([1, 2])` on an f64[] parameter) stored integer bit patterns
// into slots read as doubles and were wrong here too.
func TestSelfHostArrayElemTypeStrideX86_64(t *testing.T) {
	fernBin, stdlibRoot, runner := arrayElemFernDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range arrayElemTypeStrideCases {
		t.Run(tc.name, func(t *testing.T) {
			want := arrayElemOracle(t, interpBin, tc.files)
			mainPath := writeArrayElemCase(t, tc.files)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, cerr := exec.Command(fernBin, "-target", "x86-64-linux", mainPath, stdlibRoot, "-o", binPath).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			var rcmd *exec.Cmd
			if len(runner) == 0 {
				rcmd = exec.Command(binPath)
			} else {
				rcmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
			}
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("program did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (x86-64) = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostArrayElemTypeStrideArm64 is the leg where the self-host compiler
// produces the finished binary itself (emit + assemble + link in-process).
func TestSelfHostArrayElemTypeStrideArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	fernBin, stdlibRoot, _ := arrayElemFernDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range arrayElemTypeStrideCases {
		t.Run(tc.name, func(t *testing.T) {
			want := arrayElemOracle(t, interpBin, tc.files)
			mainPath := writeArrayElemCase(t, tc.files)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, cerr := exec.Command(fernBin, "-target", "arm64-linux", mainPath, stdlibRoot, "-o", binPath).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			var rcmd *exec.Cmd
			if qemu == "" {
				rcmd = exec.Command(binPath)
			} else {
				rcmd = exec.Command(qemu, binPath)
			}
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("program did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (arm64) = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

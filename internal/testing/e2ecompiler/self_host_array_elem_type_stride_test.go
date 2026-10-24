package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrayElemTypeStrideCases = []struct {
	name  string
	files map[string]string
}{
	{"free_fn_i64_param", map[string]string{"main.fern": `function pick(xs: i64[]): i64 { return xs[1]; }
function main(): i32 { let v: i64 = pick([1, 2]); return (v as i32) + 10; }`}},
	{"struct_field_i64", map[string]string{"main.fern": `struct Holder { xs: i64[] }
function main(): i32 { let h: Holder = Holder { xs: [1, 2] }; return (h.xs[1] as i32) + 10; }`}},
	{"method_arg_f64", map[string]string{"main.fern": `struct Bag { tag: i32 }
function (b: Bag) pick(xs: f64[]): f64 { return xs[1]; }
function main(): i32 { let b: Bag = Bag { tag: 0 }; let v: f64 = b.pick([1, 2]); return (v as i32) + 10; }`}},
	{"method_arg_i64", map[string]string{"main.fern": `struct Bag { tag: i32 }
function (b: Bag) pick(xs: i64[]): i64 { return xs[1]; }
function main(): i32 { let b: Bag = Bag { tag: 0 }; let v: i64 = b.pick([1, 2]); return (v as i32) + 10; }`}},
	{"method_arg_f64_mixed", map[string]string{"main.fern": `struct Bag { tag: i32 }
function (b: Bag) pick(xs: f64[]): f64 { return xs[1]; }
function main(): i32 { let b: Bag = Bag { tag: 0 }; let v: f64 = b.pick([1, 2.5]);
    if (v > 2.4 && v < 2.6) { return 7; }
    return 1; }`}},
	{"cross_module", map[string]string{
		"lib64.fern": `pub function pick(xs: i64[]): i64 { return xs[1]; }
pub function pickf(xs: f64[]): f64 { return xs[1]; }`,
		"main.fern": `import "./lib64";
function main(): i32 {
    let v: i64 = lib64.pick([1, 2]);
    let f: f64 = lib64.pickf([1, 2]);
    return (v as i32) + (f as i32) * 10;
}`}},
	{"elem_type_sweep", map[string]string{"main.fern": `function pu(xs: u64[]): u64 { return xs[1]; }
function pb(xs: u8[]): u8 { return xs[2]; }
function ps(xs: string[]): string { return xs[1]; }
function pl(xs: boolean[]): boolean { return xs[1]; }
function pn(m: i64[][]): i64 { return m[1][0]; }
function main(): i32 {
    let r: i32 = 0;
    if (pu([1, 2]) == (2 as u64)) { r = r + 1; }
    if (pb([1, 2, 3]) == (3 as u8)) { r = r + 2; }
    if (ps(["a", "b"]) == "b") { r = r + 4; }
    if (pl([true, false]) == false) { r = r + 8; }
    if (pn([[1, 2], [30, 4]]) == (30 as i64)) { r = r + 16; }
    return r;
}`}},
	{"f32_values_roundtrip", map[string]string{"main.fern": `function main(): i32 {
    let a: f32 = 16777216.0 as f32;
    let b: f32 = 1.0 as f32;
    let xs: f32[] = [a + b, b, a];
    let r: i32 = 0;
    if (xs[0] == a) { r = r + 1; }
    if (xs[1] == b) { r = r + 2; }
    if (xs[2] == a) { r = r + 4; }
    if (xs.len() == 3) { r = r + 8; }
    return r;
}`}},
	{"generic_param_falls_back_to_probe", map[string]string{"main.fern": `function first[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    let v: f64 = first([2.5, 1.5]);
    let w: i64 = first([(7 as i64), (8 as i64)]);
    return (v as i32) + (w as i32) * 10;
}`}},
	{"annotated_var_i64", map[string]string{"main.fern": `function main(): i32 {
    let xs: i64[] = [1, 2];
    return (xs[1] as i32) + 10;
}`}},
	{"return_position_i64", map[string]string{"main.fern": `function mk(): i64[] { return [1, 2]; }
function main(): i32 { let xs: i64[] = mk(); return (xs[1] as i32) + 10; }`}},
}

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

func arrayElemOracle(t *testing.T, interpBin string, files map[string]string) int {
	t.Helper()
	cmd := exec.Command(interpBin, "-interp", writeArrayElemCase(t, files))
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("interpreter did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func arrayElemFernDriver(t *testing.T) (fernBin, stdlibRoot string, x86runner []string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return buildSelfHostBin(t, gcc, dir, "fern.fern", "fern"), root, runner
}

func TestSelfHostArrayElemTypeStrideWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping array-element stride wasm cases")
	}
	fernBin, stdlibRoot, runner := arrayElemFernDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range arrayElemTypeStrideCases {
		t.Run(tc.name, func(t *testing.T) {
			want := arrayElemOracle(t, interpBin, tc.files)
			mainPath := writeArrayElemCase(t, tc.files)
			outWat := filepath.Join(filepath.Dir(mainPath), "out.wat")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
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

func TestSelfHostArrayElemTypeStrideX86_64(t *testing.T) {
	fernBin, stdlibRoot, runner := arrayElemFernDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range arrayElemTypeStrideCases {
		t.Run(tc.name, func(t *testing.T) {
			want := arrayElemOracle(t, interpBin, tc.files)
			mainPath := writeArrayElemCase(t, tc.files)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-o", binPath, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
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

func TestSelfHostArrayElemTypeStrideArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	fernBin, stdlibRoot, runner := arrayElemFernDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range arrayElemTypeStrideCases {
		t.Run(tc.name, func(t *testing.T) {
			want := arrayElemOracle(t, interpBin, tc.files)
			mainPath := writeArrayElemCase(t, tc.files)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "arm64-linux", "-o", binPath, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			rcmd := runArm64Bin(qemu, binPath)
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

package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Keep the interpreter result independently pinned: signedness, element
// width and pointer kind must survive both parameter and foreach bindings.
var nestedParamTypeCases = []struct {
	name string
	src  string
	want int
}{
	{"u64-index", `function pick(m: u64[][]): i32 { return (m[0][0] >> 58) as i32; }
function main(): i32 { return pick([[18000000000000000000, 1]]); }`, 62},
	{"u64-own-param", `function pick(own m: u64[][]): i32 {
    if (m[0][0] > (100 as u64)) { return 7; }
    return 99;
}
function main(): i32 { return pick([[18000000000000000000, 1]]); }`, 7},
	{"u64-method-param", `struct Picker { tag: i32 }
function (p: Picker) pick(m: u64[][]): i32 { return (m[0][0] >> 58) as i32; }
function main(): i32 {
    var p: Picker = Picker { tag: 0 };
    return p.pick([[18000000000000000000, 1]]);
}`, 62},
	{"u64-foreach", `function pick(m: u64[][]): i32 {
    for row in m { for x in row { return (x >> 58) as i32; } }
    return 1;
}
function main(): i32 { return pick([[18000000000000000000, 1]]); }`, 62},
	{"u64-local-foreach", `function main(): i32 {
    var m: u64[][] = [[18000000000000000000, 1]];
    for row in m { for x in row { return (x >> 58) as i32; } }
    return 1;
}`, 62},
	{"string-foreach", `function pick(m: string[][]): i32 {
    var n: i32 = 0;
    for row in m { for x in row { n = n + x.len(); } }
    return n;
}
function main(): i32 { return pick([["abc", "defgh"], ["ij"]]); }`, 10},
	{"string-local-foreach", `function main(): i32 {
    var m: string[][] = [["abc", "defgh"], ["ij"]];
    var n: i32 = 0;
    for row in m { for x in row { n = n + x.len(); } }
    return n;
}`, 10},
	{"i32-three-deep", `function pick(m: i32[][][]): i32 {
    var n: i32 = 0;
    for plane in m { for row in plane { for x in row { n = n + x; } } }
    return n;
}
function main(): i32 { return pick([[[1, 2], [3]], [[4]]]); }`, 10},
	{"i64-width-control", `function pick(m: i64[][]): i32 {
    for row in m { for x in row { return (x >> 32) as i32; } }
    return 99;
}
function main(): i32 { return pick([[4294967296, 2]]); }`, 1},
	{"f64-width-control", `function pick(m: f64[][]): i32 {
    var n: f64 = 0.0;
    for row in m { for x in row { n = n + x; } }
    return n as i32;
}
function main(): i32 { return pick([[1.5, 2.5], [3.5]]); }`, 7},
}

func TestSelfHostNestedParamType(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interp := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern", "wasm_ir_run.fern")
	asmDriver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "asm-driver")
	wasmDriver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm-driver")
	for _, target := range []string{"arm64-linux", "x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range nestedParamTypeCases {
				t.Run(tc.name, func(t *testing.T) {
					if got := interpExit(t, interp, tc.src); got != tc.want {
						t.Fatalf("interpreter = %d, want %d", got, tc.want)
					}
					var cmd *exec.Cmd
					switch target {
					case "arm64-linux":
						armgcc, qemu := arm64Tooling(t)
						asm := runCapture(t, gcc, runner, asmDriver, []byte(tc.src), "-target", target)
						bin := buildBinArm64(t, armgcc, t.TempDir(), "out", string(asm))
						cmd = runArm64Bin(qemu, bin)
					case "x86-64-linux":
						asm := runCapture(t, gcc, runner, asmDriver, []byte(tc.src), "-target", target)
						bin := buildBin(t, gcc, t.TempDir(), "out", string(asm))
						cmd = runX86_64Bin(runner, bin)
					case "wasm32-wasi":
						if _, err := exec.LookPath("wasmtime"); err != nil {
							t.Skip("wasmtime not on PATH")
						}
						wat := runCapture(t, gcc, runner, wasmDriver, []byte(tc.src), "-ir")
						path := filepath.Join(t.TempDir(), "out.wat")
						if err := os.WriteFile(path, wat, 0o644); err != nil {
							t.Fatal(err)
						}
						cmd = exec.Command("wasmtime", "run", path)
					}
					out, err := cmd.CombinedOutput()
					if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
						t.Fatalf("program did not exit normally: %v\n%s", err, out)
					}
					if got := cmd.ProcessState.ExitCode(); got != tc.want {
						t.Fatalf("self-host = %d, want %d\n%s", got, tc.want, out)
					}
				})
			}
		})
	}
}

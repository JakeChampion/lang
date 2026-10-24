package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type genStructOfGenEnumIRCase struct {
	name     string
	src      string
	expected int
}

var genStructOfGenEnumIRCases = []genStructOfGenEnumIRCase{
	{"box_of_opt_i32", `struct Box[T] { v: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    let b: Box[Opt[i32]] = Box { v: Sm(5) };
    match (b.v) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 5},
	{"box_of_opt_string", `struct Box[T] { v: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    let b: Box[Opt[string]] = Box { v: Sm("hey") };
    match (b.v) { Sm(s) => { return s.len(); }, Nn => { return 0; } }
}`, 3},
	{"pair_of_opt", `struct Pair[T] { a: T, b: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    let p: Pair[Opt[i32]] = Pair { a: Sm(4), b: Sm(6) };
    let x: i32 = match (p.a) { Sm(n) => n, Nn => 0 };
    let y: i32 = match (p.b) { Sm(n) => n, Nn => 0 };
    return x + y;
}`, 10},
	{"box_of_result", `struct Box[T] { v: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    let b: Box[Opt[i32]] = Box { v: Sm(40) };
    let n: i32 = match (b.v) { Sm(x) => x, Nn => 0 };
    return n + 2;
}`, 42},
}

func TestSelfHostGenStructOfGenEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range genStructOfGenEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "genstruct_of_genenum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genstruct-of-genenum %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostGenStructOfGenEnumWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range genStructOfGenEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}

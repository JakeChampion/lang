package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type structGenEnumFieldIRCase struct {
	name     string
	src      string
	expected int
}

var structGenEnumFieldIRCases = []structGenEnumFieldIRCase{
	{"field_match_i32", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function main(): i32 {
    let s = S { o: Sm(5) };
    match (s.o) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 5},
	{"field_match_string", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[string] }
function main(): i32 {
    let s = S { o: Sm("hi") };
    match (s.o) { Sm(n) => { return n.len(); }, Nn => { return 0; } }
}`, 2},
	{"field_unit", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function main(): i32 {
    let s = S { o: Nn };
    match (s.o) { Sm(n) => { return n; }, Nn => { return 9; } }
}`, 9},
	{"two_fields", `enum Opt[T] { Sm(T), Nn }
struct S { a: Opt[i32], b: Opt[i32] }
function main(): i32 {
    let s = S { a: Sm(3), b: Sm(4) };
    let x: i32 = 0;
    match (s.a) { Sm(n) => { x = n; }, Nn => { } }
    match (s.b) { Sm(n) => { x = x + n; }, Nn => { } }
    return x;
}`, 7},
	{"annotated_binding", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function main(): i32 {
    let s: S = S { o: Sm(8) };
    match (s.o) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 8},
	{"struct_param", `enum Opt[T] { Sm(T), Nn }
struct S { o: Opt[i32] }
function get(s: S): i32 {
    match (s.o) { Sm(n) => { return n; }, Nn => { return 0; } }
}
function main(): i32 { return get(S { o: Sm(6) }); }`, 6},
	{"mixed_instantiations", `enum Opt[T] { Sm(T), Nn }
struct S { a: Opt[i32], b: Opt[string] }
function main(): i32 {
    let s = S { a: Sm(4), b: Sm("xyz") };
    let x: i32 = 0;
    match (s.a) { Sm(n) => { x = n; }, Nn => { } }
    match (s.b) { Sm(t) => { x = x + t.len(); }, Nn => { } }
    return x;
}`, 7},
}

func TestSelfHostStructGenEnumFieldIRX86_64(t *testing.T) {
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

	for _, tc := range structGenEnumFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "struct_genum_field_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("struct-genum-field %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostStructGenEnumFieldWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range structGenEnumFieldIRCases {
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

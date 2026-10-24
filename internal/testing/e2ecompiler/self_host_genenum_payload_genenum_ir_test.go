package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type genEnumPayloadGenEnumIRCase struct {
	name     string
	src      string
	expected int
}

var genEnumPayloadGenEnumIRCases = []genEnumPayloadGenEnumIRCase{
	{"i32_payload", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    let e: E[i32] = A(Sm(5));
    match (e) { A(o) => { match (o) { Sm(n) => { return n; }, Nn => { return 0; } } }, B => { return 0; } }
}`, 5},
	{"string_payload", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    let e: E[string] = A(Sm("hi"));
    match (e) { A(o) => { match (o) { Sm(n) => { return n.len(); }, Nn => { return 0; } } }, B => { return 0; } }
}`, 2},
	{"outer_unit", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    let e: E[i32] = B;
    match (e) { A(o) => { return 1; }, B => { return 8; } }
}`, 8},
	{"inner_arith", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    let e: E[i32] = A(Sm(4));
    match (e) { A(o) => { match (o) { Sm(n) => { return n + 1; }, Nn => { return 0; } } }, B => { return 0; } }
}`, 5},
	{"two_instantiations", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    let x: E[i32] = A(Sm(3));
    let y: E[string] = A(Sm("zz"));
    let r: i32 = 0;
    match (x) { A(o) => { match (o) { Sm(n) => { r = n; }, Nn => { } } }, B => { } }
    match (y) { A(o) => { match (o) { Sm(s) => { r = r + s.len(); }, Nn => { } } }, B => { } }
    return r;
}`, 5},
}

func TestSelfHostGenEnumPayloadGenEnumIRX86_64(t *testing.T) {
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

	for _, tc := range genEnumPayloadGenEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "genenum_payload_genenum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genenum-payload-genenum %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostGenEnumPayloadGenEnumWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range genEnumPayloadGenEnumIRCases {
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

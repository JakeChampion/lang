package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type genEnumOfGenStructIRCase struct {
	name     string
	src      string
	expected int
}

var genEnumOfGenStructIRCases = []genEnumOfGenStructIRCase{
	{"user_enum_i32", `enum Opt[T] { Sm(T), Nn }
struct Box[U] { v: U }
function main(): i32 {
    let o: Opt[Box[i32]] = Sm(Box { v: 7 });
    match (o) { Sm(b) => { return b.v; }, Nn => { return 0; } }
}`, 7},
	{"user_enum_string", `enum Opt[T] { Sm(T), Nn }
struct Box[U] { v: U }
function main(): i32 {
    let o: Opt[Box[string]] = Sm(Box { v: "hi" });
    match (o) { Sm(b) => { return b.v.len(); }, Nn => { return 0; } }
}`, 2},
	{"user_enum_unit", `enum Opt[T] { Sm(T), Nn }
struct Box[U] { v: U }
function main(): i32 {
    let o: Opt[Box[i32]] = Nn;
    match (o) { Sm(b) => { return b.v; }, Nn => { return 9; } }
}`, 9},
	{"option_of_struct", `struct Box[U] { v: U }
function main(): i32 {
    let o: Option[Box[i32]] = Some(Box { v: 5 });
    match (o) { Some(b) => { return b.v; }, None => { return 0; } }
}`, 5},
	{"result_of_struct", `struct Box[U] { v: U }
function f(): Result[Box[i32], string] { return Ok(Box { v: 9 }); }
function main(): i32 {
    match (f()) { Ok(b) => { return b.v; }, Err(e) => { return 0; } }
}`, 9},
	{"struct_two_fields", `enum Opt[T] { Sm(T), Nn }
struct Pt[U] { x: U, y: U }
function main(): i32 {
    let o: Opt[Pt[i32]] = Sm(Pt { x: 3, y: 4 });
    match (o) { Sm(p) => { return p.x + p.y; }, Nn => { return 0; } }
}`, 7},
}

func TestSelfHostGenEnumOfGenStructIRX86_64(t *testing.T) {
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

	for _, tc := range genEnumOfGenStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				append(os.Environ(), "FERN_STRICT_IR=1"))
			if len(asm) == 0 {
				t.Fatalf("driver emitted no asm under FERN_STRICT_IR=1")
			}
			progBin := buildBin(t, gcc, dir, "genenum_of_genstruct_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genenum-of-genstruct %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostGenEnumOfGenStructWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range genEnumOfGenStructIRCases {
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

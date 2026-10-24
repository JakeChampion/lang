package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type genStructFieldGenStructIRCase struct {
	name     string
	src      string
	expected int
}

var genStructFieldGenStructIRCases = []genStructFieldGenStructIRCase{
	{"i32_field", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function main(): i32 {
    let h: Holder[i32] = Holder { b: Box { v: 9 } };
    return h.b.v;
}`, 9},
	{"string_field", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function main(): i32 {
    let h: Holder[string] = Holder { b: Box { v: "hey" } };
    return h.b.v.len();
}`, 3},
	{"mixed_fields", `struct Box[T] { v: T }
struct Holder[U] { tag: i32, b: Box[U] }
function main(): i32 {
    let h: Holder[i32] = Holder { tag: 3, b: Box { v: 4 } };
    return h.tag + h.b.v;
}`, 7},
	{"two_instantiations", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function main(): i32 {
    let a: Holder[i32] = Holder { b: Box { v: 4 } };
    let c: Holder[string] = Holder { b: Box { v: "xyz" } };
    return a.b.v + c.b.v.len();
}`, 7},
	{"struct_param", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function get(h: Holder[i32]): i32 { return h.b.v; }
function main(): i32 { return get(Holder { b: Box { v: 6 } }); }`, 6},
}

func TestSelfHostGenStructFieldGenStructIRX86_64(t *testing.T) {
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

	for _, tc := range genStructFieldGenStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "genstruct_field_genstruct_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genstruct-field-genstruct %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostGenStructFieldGenStructWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range genStructFieldGenStructIRCases {
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

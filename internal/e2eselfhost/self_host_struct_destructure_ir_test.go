package e2eselfhost

import (
	"os/exec"
	"testing"
)

// Struct destructure on the self-host IR path (#5354). The self-host parser
// gained the same `let/var Point { x, y } = E;` support as the native
// front-end: parse_struct_destructure encodes it as a StmtVar whose
// type_name is "@sd:<Struct>:<fields>" and whose name is the comma-joined
// bindings, and irlower's lower_struct_destructure expands it into per-field
// `var bind = tmp.field;` binds (reusing the field-read typing + RC dup-on-
// projection). These build the self-host x86-64 IR driver and assert the
// compiled binary agrees with the interpreter oracle.
var selfHostStructDestructureCases = []struct {
	name string
	src  string
}{
	{"shorthand", `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 3, y: 4 };
    let Point { x, y } = p;
    return x * 10 + y;
}`},
	{"var_keyword", `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 7, y: 2 };
    var Point { x, y } = p;
    return x - y;
}`},
	{"rename", `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 8, y: 1 };
    let Point { x: a, y: b } = p;
    return a * 10 + b;
}`},
	{"rest_partial", `struct Point { x: i32, y: i32, z: i32 }
function main(): i32 {
    var p: Point = Point { x: 5, y: 6, z: 7 };
    let Point { x, z, .. } = p;
    return x * 10 + z;
}`},
	{"string_field", `struct Named { id: i32, label: string }
function main(): i32 {
    var n: Named = Named { id: 40, label: "abc" };
    let Named { id, label } = n;
    return id + label.len();
}`},
	{"from_call", `struct Point { x: i32, y: i32 }
function mk(a: i32, b: i32): Point { return Point { x: a, y: b }; }
function main(): i32 {
    let Point { x, y } = mk(9, 6);
    return x * 10 + y;
}`},
}

// TestSelfHostStructDestructureX86_64 compiles each program through the
// self-host x86-64 IR path (asm_ir_run `-ir`) and checks the exit code
// against the native interpreter oracle.
func TestSelfHostStructDestructureX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostStructDestructureCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCapture(t, gcc, runner, driverBin, prog, "-ir")
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

// TestSelfHostStructDestructureArm64 — CI-gated arm64 counterpart. The
// destructure expansion is shared irlower analysis, so both register
// backends inherit it; the driver is built x86 and emits arm64 asm.
func TestSelfHostStructDestructureArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostStructDestructureCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCapture(t, x86gcc, x86runner, driverBin, prog, "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

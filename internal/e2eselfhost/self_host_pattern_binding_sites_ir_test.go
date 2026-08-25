package e2eselfhost

import (
	"os/exec"
	"testing"
)

// One pattern grammar at every irrefutable binding site (#5356). The `for`
// header and the `let` / `var` destructure now take the same pattern heads a
// destructured parameter does — a struct pattern, and an `@` binding naming
// the whole value beside either shape.
//
// On the self-host a pattern-shaped `for` header desugars to the plain foreach
// plus a destructure of the element (parse_for_stmt), which is the shape
// irlower already builds for `for (a, b) in xs`; the `@` binding rides on that
// destructure's marker channel and names the temp holding the whole value.
// These build the self-host x86-64 IR driver and assert the compiled binary
// agrees with the interpreter oracle — so a divergence between the self-host's
// desugar and native's ForEach/AtName lowering fails here rather than silently
// dropping a binding.
var selfHostPatternBindingSiteCases = []struct {
	name string
	src  string
}{
	{"for_struct_pattern", `struct Point { x: i32, y: i32 }
function main(): i32 {
    var ps: Point[] = [Point { x: 3, y: 4 }, Point { x: 5, y: 6 }];
    var acc: i32 = 0;
    for Point { x, y } in ps { acc = acc + x * 10 + y; }
    return acc;
}`},
	{"for_struct_rename_rest", `struct Point { x: i32, y: i32, z: i32 }
function main(): i32 {
    var ps: Point[] = [Point { x: 1, y: 2, z: 3 }, Point { x: 4, y: 5, z: 6 }];
    var acc: i32 = 0;
    for Point { x: a, z, .. } in ps { acc = acc + a * 10 + z; }
    return acc;
}`},
	{"for_at_struct", `struct Point { x: i32, y: i32 }
function main(): i32 {
    var ps: Point[] = [Point { x: 2, y: 3 }];
    var acc: i32 = 0;
    for w @ Point { x, y } in ps { acc = acc + w.x + w.y + x + y; }
    return acc;
}`},
	{"for_at_tuple", `function main(): i32 {
    var ts: (i32, i32)[] = [(3, 4), (5, 6)];
    var acc: i32 = 0;
    for w @ (a, b) in ts { acc = acc + w.0 + b; }
    return acc;
}`},
	{"var_at_struct", `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 6, y: 1 };
    var w @ Point { x, y } = p;
    return w.x * 10 + w.y + x + y;
}`},
	{"let_at_struct_rename", `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 8, y: 3 };
    let w @ Point { x: a, y: b } = p;
    return w.x * 10 + a + b;
}`},
	{"var_at_tuple", `function main(): i32 {
    var w @ (a, b) = (9, 2);
    return w.0 * 10 + w.1 + a + b;
}`},
	{"at_struct_string_field", `struct Named { id: i32, label: string }
function main(): i32 {
    var n: Named = Named { id: 30, label: "abcd" };
    let w @ Named { id, label } = n;
    return id + label.len() + w.label.len();
}`},
}

// TestSelfHostPatternBindingSitesX86_64 compiles each program through the
// self-host x86-64 IR path (asm_ir_run `-ir`) and checks the exit code against
// the native interpreter oracle.
func TestSelfHostPatternBindingSitesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostPatternBindingSiteCases {
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

// TestSelfHostPatternBindingSitesArm64 — CI-gated arm64 counterpart. The
// desugar and the `@` holder naming are shared irlower analysis, so both
// register backends inherit them; the driver is built x86 and emits arm64 asm.
func TestSelfHostPatternBindingSitesArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostPatternBindingSiteCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCapture(t, x86gcc, x86runner, driverBin, prog, "-target", "arm64-linux", "-ir")
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

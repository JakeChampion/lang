package e2eselfhost

import (
	"os/exec"
	"testing"
)

// Struct-pattern match arms on the self-host IR path (#5354). An `Ident {` at an
// arm is also how a record-form ENUM variant is destructured, so the parser
// takes the struct path only when the head names a struct declared in the same
// file (is_struct_pattern_arm, #6676): it desugars `match (p) { Point { x,
// y } => … }` at parse time (build_struct_match) into the `done`-flag chain
// with per-field `var bind = tmp.field;` binds — the field reads + ifs lower
// through the ordinary IR paths, no checker/irlower changes. These build the
// self-host x86-64 + arm64 IR drivers and check each program against the
// native interpreter oracle.
var selfHostStructMatchCases = []struct {
	name string
	src  string
}{
	{"guarded_fallthrough", `struct Point { x: i32, y: i32 }
function classify(p: Point): i32 {
  match (p) {
    Point { x, y } when x > y => { return 1; },
    Point { x, y } when x < y => { return 2; },
    Point { x, y } => { return 3; },
  }
  return 0;
}
function main(): i32 {
  return classify(Point { x: 5, y: 2 }) * 100
       + classify(Point { x: 1, y: 9 }) * 10
       + classify(Point { x: 4, y: 4 });
}`},
	{"single_arm", `struct P { x: i32, y: i32 }
function f(p: P): i32 { match (p) { P { x, y } => { return x + y; } } return 0; }
function main(): i32 { return f(P { x: 10, y: 20 }); }`},
	{"wildcard_default", `struct P { x: i32, y: i32 }
function f(p: P): i32 {
  match (p) {
    P { x, y } when x == 0 => { return 99; },
    _ => { return 7; },
  }
  return 0;
}
function main(): i32 { return f(P { x: 0, y: 1 }) + f(P { x: 5, y: 1 }); }`},
	{"string_field", `struct Named { id: i32, label: string }
function f(n: Named): i32 {
  match (n) {
    Named { id, label } when id > 5 => { return label.len() + 10; },
    Named { id, label } => { return label.len(); },
  }
  return 0;
}
function main(): i32 { return f(Named { id: 9, label: "abcd" }) * 10 + f(Named { id: 1, label: "ab" }); }`},
	{"partial_bind", `struct P { x: i32, y: i32, z: i32 }
function f(p: P): i32 { match (p) { P { y } => { return y; } } return 0; }
function main(): i32 { return f(P { x: 1, y: 42, z: 3 }); }`},
	{"expr_form", `struct Point { x: i32, y: i32 }
function area(p: Point): i32 {
  return match (p) {
    Point { x, y } when x == y => x * 10,
    Point { x, y } => x + y,
  };
}
function main(): i32 { return area(Point { x: 4, y: 4 }) + area(Point { x: 3, y: 5 }); }`},
	{"rename_stmt", `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { match (p) { Point { x: a, y: b } => { return a * 10 + b; } } return 0; }
function main(): i32 { return f(Point { x: 3, y: 7 }); }`},
	{"rename_expr", `struct Point { x: i32, y: i32 }
function area(p: Point): i32 {
  return match (p) {
    Point { x: a, y: b } when a == b => a * 10,
    Point { x: a, y: b } => a + b,
  };
}
function main(): i32 { return area(Point { x: 4, y: 4 }) + area(Point { x: 3, y: 5 }); }`},
	{"rename_partial", `struct P { x: i32, y: i32, z: i32 }
function f(p: P): i32 { match (p) { P { y: m } => { return m; } } return 0; }
function main(): i32 { return f(P { x: 1, y: 42, z: 3 }); }`},
	{"rename_mixed", `struct Named { id: i32, label: string }
function f(n: Named): i32 {
  match (n) {
    Named { id: k, label } when k > 5 => { return label.len() + 10; },
    Named { id, label: s } => { return s.len(); },
  }
  return 0;
}
function main(): i32 { return f(Named { id: 9, label: "abcd" }) * 10 + f(Named { id: 1, label: "ab" }); }`},
}

func TestSelfHostStructMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostStructMatchCases {
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

func TestSelfHostStructMatchArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostStructMatchCases {
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

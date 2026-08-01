package e2eselfhost

import (
	"os/exec"
	"testing"
)

// `@` bindings on the self-host IR path (#5356): `match (b) { n @ Full(v) => … }`
// and `match (p) { w @ Point { x, y } => … }` bind the whole matched value
// alongside the payload / field binds.
//
// Variant `@`: the parser records at_binding on PatVariant; lower_stmt_match
// prepends `var n = <cached scrutinee>;` to the arm body and rewrites any
// guard reference to `n` to the cached scrutinee (astwalk.subst_ident_expr)
// so the guard sees the value before the body-prepended bind. The expression
// form delegates via lower_iife_match, inheriting both.
//
// Struct `@`: struct matches desugar at parse time (build_struct_match), so
// the at-name is emitted as `var w = <cached scrutinee>;` in the arm's bind
// list — ahead of the guard — no lowering change needed.
//
// Tuple `@`: tuple matches also desugar at parse time (build_tuple_match).
// When any arm binds `@`, the whole tuple is cached in a `_w` temp (single
// scrutinee eval) that the element destructure reads from, and the at-name
// aliases it in the arm's bind list.
var selfHostAtBindingCases = []struct {
	name string
	src  string
}{
	{"stmt_whole_and_payload", `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 {
  match (b) {
    n @ Full(v) => { return total(n) * 10 + v; },
    Empty => { return 0; },
  }
  return 0;
}
function main(): i32 { return f(Full(3)); }`},
	{"expr_no_guard", `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 { return match (b) { n @ Full(v) => total(n) * 10 + v, Empty => 0 }; }
function main(): i32 { return f(Full(3)); }`},
	{"expr_guard_uses_at", `enum Box { Full(i32), Empty }
function is_full(b: Box): boolean { match (b) { Full(v) => { return true; }, Empty => { return false; } } return false; }
function f(b: Box): i32 {
  return match (b) {
    n @ Full(v) when is_full(n) => v + 2,
    Full(v) => v,
    Empty => 0,
  };
}
function main(): i32 { return f(Full(3)); }`},
	{"struct_whole_and_fields", `struct Point { x: i32, y: i32 }
function f(p: Point): i32 {
  match (p) {
    w @ Point { x, y } => { return w.x + w.y + x + y; },
  }
  return 0;
}
function main(): i32 { return f(Point { x: 3, y: 4 }); }`},
	{"struct_expr_guard_uses_at", `struct P { a: i32, b: i32 }
function f(p: P): i32 {
  return match (p) {
    w @ P { a, b } when w.a > 0 => a + b,
    P { a, b } => 0,
  };
}
function main(): i32 { return f(P { a: 2, b: 8 }); }`},
	{"struct_at_with_rename", `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { match (p) { w @ Point { x: nx, y: ny } => { return w.x * 100 + nx * 10 + ny; } } return 0; }
function main(): i32 { return f(Point { x: 1, y: 2 }); }`},
	{"tuple_whole_and_elems", `function f(t: (i32, i32)): i32 {
  match (t) {
    w @ (1, x) => { return w.0 * 10 + x; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f((1, 5)); }`},
	{"tuple_expr_guard_uses_at", `function f(t: (i32, i32)): i32 {
  return match (t) {
    w @ (a, b) when w.1 > 0 => a + b,
    _ => 0,
  };
}
function main(): i32 { return f((4, 6)); }`},
}

func TestSelfHostAtBindingX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "flatten.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostAtBindingCases {
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

func TestSelfHostAtBindingArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "flatten.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostAtBindingCases {
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

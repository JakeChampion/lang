package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditNumGenCases isolate sized-int / float numeric features and
// generics / traits / closures, run through the SELF-HOSTED compiler.
// Self-host arm of the §A audit (docs/FEATURE-AUDIT.md); the native arm
// is the `audit_numeric_types` + `audit_generics_traits_closures`
// fixtures (all four native backends).
var auditNumGenCases = []struct {
	name string
	src  string
	exit int
}{
	// sized ints + floats
	{"i64-add", `function main(): i32 { var big: i64 = 5000000000; var big1: i64 = big + 1; return (big1 - big) as i32; }`, 1},
	{"i64-mul", `function main(): i32 { var prod: i64 = 1000000 * 3; return (prod / 1000000) as i32; }`, 3},
	{"u8-wrap", `function main(): i32 { var v: i32 = 250 + 10; var w: u8 = v as u8; return w as i32; }`, 4},
	{"cast-narrow", `function main(): i32 { var v: i32 = 300; var w: u8 = v as u8; return w as i32; }`, 44},
	{"f64-mul", `function main(): i32 { var fx: f64 = 3.5; return (fx * 2.0) as i32; }`, 7},
	{"f64-cmp", `function main(): i32 { var a: f64 = 1.5; var b: f64 = 2.5; if (!(a < b) || !(b > a) || (a >= b)) { return 1; } return 9; }`, 9},
	{"f32-add", `function main(): i32 { var g: f32 = 2.5; var h: f32 = g + 1.5; return h as i32; }`, 4},
	// generics / traits / closures
	{"generic-fn", `function id[T](x: T): T { return x; } function main(): i32 { return id(42); }`, 42},
	{"generic-struct", `struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box { v: 33 }; return b.v; }`, 33},
	{"generic-method", `struct Box[T] { v: T } function (b: Box[i32]) get(): i32 { return b.v; } function main(): i32 { var b: Box[i32] = Box { v: 33 }; return b.get(); }`, 33},
	{"trait-dispatch", `trait Doubler { function dbl(self: Self): i32; } impl Doubler for i32 { function dbl(self: Self): i32 { return self * 2; } } function main(): i32 { return (21).dbl(); }`, 42},
	{"lambda", `function main(): i32 { var f: (i32) => i32 = function(x: i32): i32 { return x + 1; }; return f(41); }`, 42},
	{"closure-capture", `function main(): i32 { var n: i32 = 10; var f: (i32) => i32 = function(x: i32): i32 { return x + n; }; return f(5); }`, 15},
	{"fn-value", `function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var f: (i32) => i32 = dbl; return f(21); }`, 42},
	{"higher-order", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var inc: (i32) => i32 = function(x: i32): i32 { return x + 1; }; return apply(inc, 41); }`, 42},
	{"tail-call", `function sum_to(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return sum_to(n - 1, acc + n); } function main(): i32 { return sum_to(100, 0); }`, 186}, // 5050 mod 256
}

// TestSelfHostAuditNumGenX86_64 runs each numeric/generics case through
// the self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditNumGenX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range auditNumGenCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostAuditNumGenArm64 — CI-gated arm64 counterpart.
func TestSelfHostAuditNumGenArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range auditNumGenCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

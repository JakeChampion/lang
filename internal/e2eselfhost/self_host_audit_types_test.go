package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditTypesCases isolate composite-type + pattern-matching features and
// run them through the SELF-HOSTED compiler, asserting the exit code.
// Self-host arm of the §C / §A audit (docs/FEATURE-AUDIT.md); the native
// arm is the `audit_types_match` fixture (all four native backends).
//
// NOTE: struct fields are immutable after construction — the sanctioned
// update is functional (`T { ...old, f: v }`). The native checker rejects
// `p.x = v` (E048); the self-host checker currently does NOT (issue
// #2825), so these cases deliberately use only functional update.
var auditTypesCases = []struct {
	name string
	src  string
	exit int
}{
	{"struct-literal-field", `struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 7, y: 3 }; return p.x + p.y; }`, 10},
	{"struct-method", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function main(): i32 { var p: P = P { x: 21 }; return p.dbl(); }`, 42},
	{"struct-functional-update", `struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 7, y: 3 }; var q: P = P { ...p, x: 40 }; return q.x + q.y + p.x; }`, 50},
	{"enum-payload-match", `enum Sh { Circle(i32), Square(i32), Unit } function area(s: Sh): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(a) => { return a * a; }, Unit => { return 1; } } } function main(): i32 { return area(Square(6)); }`, 36},
	{"enum-unit-variant", `enum Sh { Circle(i32), Unit } function area(s: Sh): i32 { match (s) { Circle(r) => { return r; }, Unit => { return 99; } } } function main(): i32 { return area(Unit); }`, 99},
	{"match-expression", `enum Sh { Circle(i32), Square(i32) } function main(): i32 { var s: Sh = Square(5); var m: i32 = match (s) { Circle(r) => r + 1, Square(a) => a + 2 }; return m; }`, 7},
	{"tuple-index", `function main(): i32 { var t: (i32, i32) = (7, 3); return t.0 + t.1; }`, 10},
	{"tuple-destructure", `function swap(): (i32, i32) { return (3, 7); } function main(): i32 { var (a, b) = swap(); return a * 10 + b; }`, 37},
	{"option-some", `function main(): i32 { var o: Option[i32] = Some(42); match (o) { Some(v) => { return v; }, None => { return 0; } } }`, 42},
	{"option-none", `function main(): i32 { var o: Option[i32] = None; match (o) { Some(v) => { return v; }, None => { return 17; } } }`, 17},
	{"result-ok", `function main(): i32 { var r: Result[i32, i32] = Ok(42); match (r) { Ok(v) => { return v; }, Err(e) => { return 0 - e; } } }`, 42},
	{"result-err", `function main(): i32 { var r: Result[i32, i32] = Err(9); match (r) { Ok(v) => { return v; }, Err(e) => { return e; } } }`, 9},
}

// TestSelfHostAuditTypesX86_64 runs each composite-type case through the
// self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditTypesX86_64(t *testing.T) {
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

	for _, tc := range auditTypesCases {
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

// TestSelfHostAuditTypesArm64 — CI-gated arm64 counterpart.
func TestSelfHostAuditTypesArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range auditTypesCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
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

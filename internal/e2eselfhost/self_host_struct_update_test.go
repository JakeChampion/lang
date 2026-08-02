package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// structUpdateCases exercise the struct-update expression
// `Name { ...base, field: value }` in the self-host compiler: a
// leading `...base` spread copies the un-overridden declared fields
// from `base`, the listed fields override (in any order, as a subset).
// The self-host parser flags this with ExprStructLit.has_base; the
// checker relaxes completeness + validates the base type; the emitter
// allocates a fresh box, copies the non-overridden declared fields
// from base, then stores the overrides. Exit codes cross-checked vs the
// Go interp + Go native x86-64 backend.
var structUpdateCases = []struct {
	name string
	src  string
	exit int
}{
	// Single override; the other two fields copy from base. 1+20+3=24.
	{"single-override", "struct P { x: i32, y: i32, z: i32 } function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p, y: 20 }; return q.x + q.y + q.z; }", 24},
	// Pure copy (no overrides) — every field comes from base. 100+20+3=123.
	{"pure-copy", "struct P { x: i32, y: i32, z: i32 } function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p }; return q.x*100 + q.y*10 + q.z; }", 123},
	// Overrides listed out of declaration order (z then x); the box must
	// still be laid out in decl order. 9*100+2*10+7=927 -> 927&255=159.
	{"out-of-order-overrides", "struct P { x: i32, y: i32, z: i32 } function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p, z: 7, x: 9 }; return q.x*100 + q.y*10 + q.z; }", 159},
	// Struct-update inside a function return; the base local is borrowed
	// (its own field stays unchanged). 6*1000+5*100+106=6606 -> &255=206.
	{"update-in-return", "struct P { a: i32, b: i32 } function bump(p: P): P { return P { ...p, b: p.b + 100 }; } function main(): i32 { var p: P = P { a: 5, b: 6 }; var q: P = bump(p); return p.b*1000 + q.a*100 + q.b; }", 206},
	// String field copied verbatim from base, i32 field overridden.
	{"string-field-copy", "struct S { name: string, n: i32 } function main(): i32 { var s: S = S { name: \"hi\", n: 3 }; var t: S = S { ...s, n: 9 }; if (t.name == \"hi\") { return t.n; } return 0; }", 9},
}

// TestSelfHostStructUpdateX86_64 compiles struct-update programs with
// the self-hosted x86-64 compiler and checks exit codes.
func TestSelfHostStructUpdateX86_64(t *testing.T) {
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

	for _, tc := range structUpdateCases {
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

// TestSelfHostStructUpdateArm64 — CI-gated arm64 counterpart.
func TestSelfHostStructUpdateArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structUpdateCases {
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

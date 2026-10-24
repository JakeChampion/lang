package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var shortCircuitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"and-method-both-true",
		`struct P { x: i32 } function (p: P) ok(): boolean { return p.x > 0; } function main(): i32 { let a: P = P { x: 1 }; let b: P = P { x: 2 }; if (a.ok() && b.ok()) { return 1; } return 0; }`, 1},
	{"and-shortcircuit-skips-trap-rhs",
		`function rhs(): boolean { let z: i32 = 0; return (10 / z) == 0; } function main(): i32 { let f: boolean = false; if (f && rhs()) { return 1; } return 7; }`, 7},
	{"or-shortcircuit-skips-trap-rhs",
		`function rhs(): boolean { let z: i32 = 0; return (10 / z) == 0; } function main(): i32 { let t: boolean = true; if (t || rhs()) { return 7; } return 1; }`, 7},
	{"and-rhs-false",
		`function rhs(): boolean { return false; } function main(): i32 { let t: boolean = true; if (t && rhs()) { return 1; } return 9; }`, 9},
	{"or-rhs-true",
		`function rhs(): boolean { return true; } function main(): i32 { let f: boolean = false; if (f || rhs()) { return 5; } return 1; }`, 5},
	{"and-chain-eq-methods",
		`trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } struct P { x: i32, y: i32 } function (p: P) same(o: P): boolean { return p.x.eq(o.x) && p.y.eq(o.y); } function main(): i32 { let a: P = P { x: 1, y: 2 }; let b: P = P { x: 1, y: 2 }; let c: P = P { x: 1, y: 9 }; let r: i32 = 0; if (a.same(b)) { r = r + 1; } if (!a.same(c)) { r = r + 2; } return r; }`, 3},
}

func TestSelfHostShortCircuitIRX86_64(t *testing.T) {
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

	for _, tc := range shortCircuitIRCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostShortCircuitIRWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range shortCircuitIRCases {
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

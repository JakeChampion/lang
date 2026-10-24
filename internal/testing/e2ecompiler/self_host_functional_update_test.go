package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var functionalUpdateCases = []struct {
	name string
	src  string
	exit int
}{
	{"bump-through-fn", "struct Box { v: i32 } function bump(b: Box): Box { return Box { ...b, v: b.v + 1 }; } function main(): i32 { let b: Box = Box { v: 10 }; b = bump(b); b = bump(b); return b.v; }", 12},
	{"two-fields", "struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 1, y: 2 }; p = P { ...p, x: 10 }; p = P { ...p, y: 30 }; return p.x + p.y; }", 40},
	{"update-in-loop", "struct C { n: i32 } function inc(c: C): C { return C { ...c, n: c.n + 1 }; } function main(): i32 { let c: C = C { n: 0 }; let i: i32 = 0; while (i < 5) { c = inc(c); i = i + 1; } return c.n; }", 5},
	{"compound-via-update", "struct A { v: i32 } function main(): i32 { let a: A = A { v: 7 }; a = A { ...a, v: a.v + 35 }; return a.v; }", 42},
}

func TestSelfHostFunctionalUpdateX86_64(t *testing.T) {
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

	for _, tc := range functionalUpdateCases {
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

func TestSelfHostFunctionalUpdateArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range functionalUpdateCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}

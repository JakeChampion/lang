package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var structUpdateCases = []struct {
	name string
	src  string
	exit int
}{
	{"single-override", "struct P { x: i32, y: i32, z: i32 } function main(): i32 { let p: P = P { x: 1, y: 2, z: 3 }; let q: P = P { ...p, y: 20 }; return q.x + q.y + q.z; }", 24},
	{"pure-copy", "struct P { x: i32, y: i32, z: i32 } function main(): i32 { let p: P = P { x: 1, y: 2, z: 3 }; let q: P = P { ...p }; return q.x*100 + q.y*10 + q.z; }", 123},
	{"out-of-order-overrides", "struct P { x: i32, y: i32, z: i32 } function main(): i32 { let p: P = P { x: 1, y: 2, z: 3 }; let q: P = P { ...p, z: 7, x: 9 }; return q.x*100 + q.y*10 + q.z; }", 159},
	{"update-in-return", "struct P { a: i32, b: i32 } function bump(p: P): P { return P { ...p, b: p.b + 100 }; } function main(): i32 { let p: P = P { a: 5, b: 6 }; let q: P = bump(p); return p.b*1000 + q.a*100 + q.b; }", 206},
	{"string-field-copy", "struct S { name: string, n: i32 } function main(): i32 { let s: S = S { name: \"hi\", n: 3 }; let t: S = S { ...s, n: 9 }; if (t.name == \"hi\") { return t.n; } return 0; }", 9},
}

func TestSelfHostStructUpdateX86_64(t *testing.T) {
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

func TestSelfHostStructUpdateArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range structUpdateCases {
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

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var structArrayIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"index-field-direct",
		`struct P { v: i32 } function main(): i32 { let ps: P[] = [P { v: 3 }, P { v: 4 }]; return ps[0].v + ps[1].v; }`, 7},
	{"for-elem-field",
		`struct P { v: i32 } function main(): i32 { let ps: P[] = [P { v: 3 }, P { v: 4 }]; let t: i32 = 0; for x in ps { t = t + x.v; } return t; }`, 7},
	{"for-elem-method",
		`struct P { v: i32 } function (p: P) get(): i32 { return p.v; } function main(): i32 { let ps: P[] = [P { v: 3 }, P { v: 4 }]; let t: i32 = 0; for x in ps { t = t + x.get(); } return t; }`, 7},
	{"param-index-field",
		`struct P { v: i32 } function sum(a: P[]): i32 { let t: i32 = 0; let i: i32 = 0; while (i < a.len()) { t = t + a[i].v; i = i + 1; } return t; } function main(): i32 { let ps: P[] = [P { v: 3 }, P { v: 4 }]; return sum(ps); }`, 7},
	{"index-multifield",
		`struct P { a: i32, b: i32 } function main(): i32 { let ps: P[] = [P { a: 10, b: 20 }, P { a: 30, b: 40 }]; return ps[0].a + ps[1].b; }`, 50},
	{"var-index-loop",
		`struct P { v: i32 } function main(): i32 { let ps: P[] = [P { v: 1 }, P { v: 2 }, P { v: 3 }]; let t: i32 = 0; let i: i32 = 0; while (i < ps.len()) { t = t + ps[i].v; i = i + 1; } return t; }`, 6},
	{"inferred-index-field",
		`struct P { v: i32 } function main(): i32 { let ps = [P { v: 3 }, P { v: 4 }]; return ps[0].v + ps[1].v; }`, 7},
	{"inferred-for-elem-field",
		`struct P { v: i32 } function main(): i32 { let ps = [P { v: 3 }, P { v: 4 }]; let t: i32 = 0; for x in ps { t = t + x.v; } return t; }`, 7},
	{"inferred-for-elem-method",
		`struct P { v: i32 } function (p: P) get(): i32 { return p.v; } function main(): i32 { let ps = [P { v: 3 }, P { v: 4 }]; let t: i32 = 0; for x in ps { t = t + x.get(); } return t; }`, 7},
	{"inferred-index-multifield",
		`struct P { a: i32, b: i32 } function main(): i32 { let ps = [P { a: 10, b: 20 }, P { a: 30, b: 40 }]; return ps[0].a + ps[1].b; }`, 50},
	{"inferred-var-index-loop",
		`struct P { v: i32 } function main(): i32 { let ps = [P { v: 1 }, P { v: 2 }, P { v: 3 }]; let t: i32 = 0; let i: i32 = 0; while (i < ps.len()) { t = t + ps[i].v; i = i + 1; } return t; }`, 6},
}

func TestSelfHostStructArrayIRX86_64(t *testing.T) {
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

	for _, tc := range structArrayIRCases {
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

func TestSelfHostStructArrayIRWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range structArrayIRCases {
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

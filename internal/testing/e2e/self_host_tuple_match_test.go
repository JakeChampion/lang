package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var selfHostTupleMatchCases = []struct {
	name string
	src  string
	exit int
}{
	{"literal-dispatch", "function classify(p: (i32, i32)): i32 { match (p) { (0, 0) => { return 1; }, (0, y) => { return y; }, (x, 0) => { return x * 10; }, (x, y) when x > y => { return x - y; }, (x, y) => { return x + y; } } return -1; } function main(): i32 { return classify((0, 0)) + classify((0, 7)) + classify((3, 0)) + classify((9, 4)) + classify((2, 5)) - 8; }", 42},
	{"wildcard-arm", "function pick(p: (i32, i32)): i32 { match (p) { (1, y) => { return y; }, _ => { return 40; } } return -1; } function main(): i32 { return pick((1, 2)) + pick((9, 9)); }", 42},
	{"string-element", "function tag(p: (string, i32)): i32 { match (p) { (\"a\", n) => { return n; }, (_, n) => { return n * 10; } } return -1; } function main(): i32 { return tag((\"a\", 2)) + tag((\"z\", 4)); }", 42},
	{"expr-form", "function main(): i32 { let v = match ((1, 2)) { (1, b) => b * 20, (a, _) => a }; return v + 2; }", 42},
	{"expr-form-capturing", "function main(): i32 { let p = (1, 2); let v = match (p) { (x, _) => x }; return v + 41; }", 42},
	{"expr-form-two", "function main(): i32 { let p = (1, 2); let q = (7, 7); let a = match (p) { (1, b) => b * 10, (x, _) => x }; let b = match (q) { (0, y) => y, (x, y) when x == y => x + y, (x, y) => x - y }; return a + b + 8; }", 42},
	{"expr-form-block-body", "function main(): i32 { let p = (3, 4); let v = match (p) { (x, y) => { let s = x + y; s * 6 } }; return v; }", 42},
	{"expr-form-struct-pattern", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 40, y: 2 }; let v = match (p) { P { x, y } => x + y }; return v; }", 42},
}

func TestSelfHostTupleMatchX86_64(t *testing.T) {
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

	for _, tc := range selfHostTupleMatchCases {
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

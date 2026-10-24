package e2ecompiler

import (
	"os/exec"
	"testing"
)

var strFreshRetIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"freshret-concat",
		`import "std/i32";
function fmt(n: i32): string { return "n=" + n.to_string(); } function main(): i32 { let r: string = fmt(42); return r.len(); }`,
		4},
	{"freshret-unannotated",
		`import "std/string";
function up(s: string): string { return s.to_ascii_upper(); } function main(): i32 { let r = up("abc"); return r.len(); }`,
		3},
	{"freshret-churn-safe",
		`import "std/i32";
function build(n: i32): string { return "x=" + n.to_string(); } function main(): i32 { let t: i32 = 0; let k: i32 = 0; while (k < 3000000) { let r: string = build(k); if (r.len() < 3) { t = 1; } k = k + 1; } return t; }`,
		0},
	{"freshret-return-param-not-reclaimed",
		`@noinline function id(s: string): string { return s; } function main(): i32 { let r: string = id("xy"); return r.len(); }`,
		2},
}

func TestSelfHostStrFreshRetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range strFreshRetIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
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

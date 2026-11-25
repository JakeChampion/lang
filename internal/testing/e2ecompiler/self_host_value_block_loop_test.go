package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A local that a loop body declares is new on each iteration, also when a
// value block of the body consumes it (#17178).
func valueBlockLoopCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = `struct E { buf: u8[] }
function (own e: E) done(): E { return e; }
function take(own e: E): i32 { return e.buf.len(); }
`
	const fresh = `let e = E { buf: [] }; `
	cases := []struct{ name, body, want string }{
		{"match-scrutinee", `let t = 0; for _ in 0..3 { ` + fresh + `let n = match (e.done().buf.len()) { 0 => 1, _ => 2 }; t += n; } return t - 3;`, ""},
		{"block", `let t = 0; for _ in 0..3 { ` + fresh + `let n = { e.done().buf.len() }; t += n; } return t;`, ""},
		{"if-condition", `let t = 0; for _ in 0..3 { ` + fresh + `let n = if (e.done().buf.len() > 0) { 1 } else { 2 }; t += n; } return t - 6;`, ""},
		{"while", `let t = 0; while (t < 3) { ` + fresh + `let n = { take(e) }; t += n + 1; } return t - 3;`, ""},
		{"continue-after-move", `let t = 0; for i in 0..3 { ` + fresh + `let n = { e.done().buf.len() }; if (i == 1) { continue; } t += n + 1; } return t - 2;`, ""},
		{"outer-local", fresh + `let t = 0; for _ in 0..3 { let n = { e.done().buf.len() }; t += n; } return t;`, "E051"},
		{"use-after-move", `let t = 0; for _ in 0..3 { ` + fresh + `let n = { e.done().buf.len() }; t += n + e.buf.len(); } return t;`, "E051"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "function main(): i32 { " + tc.body + " }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.want == "" {
					if len(ds) != 0 {
						t.Fatalf("%s rejected a valid loop: %v", label, ds)
					}
					return
				}
				for _, d := range ds {
					if d.code == tc.want {
						return
					}
				}
				t.Fatalf("%s: want %s, got %v", label, tc.want, ds)
			}
			check("Go", goCheckerDiags(t, dir, src))
			cmd := exec.Command(bin)
			if len(runner) != 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			check("self-host", driverDiags(runCheckerDriver(t, cmd, tc.name)))
		})
	}
}

func TestSelfHostValueBlockLoopX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "value_block_loop_checker")
	valueBlockLoopCases(t, bin, runner, dir)
}

func TestSelfHostValueBlockLoopWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "value_block_loop_checker.wasm", e2eharness.TargetWasm32Wasi)
	valueBlockLoopCases(t, bin, []string{wasmtime, "run"}, dir)
}

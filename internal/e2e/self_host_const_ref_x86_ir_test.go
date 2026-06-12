package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostConstRefX86IR gates a bare `const` reference (a zero-arg i32 /
// boolean function, the desugared form of `const NAME = EXPR;`) lowering on the
// IR path: a value-position `NAME` becomes a direct call (arity 0) instead of
// bailing the whole module to the AST backend.
//
// Proving the IR path handled it needs care: for simple programs the IR and AST
// backends emit byte-identical asm (the IR path is behaviour-equivalent by
// design), and a const ref lowers to the same `call` in both — so the output
// alone can't reveal eligibility. The trick: each program ALSO allocates a
// leak-safe struct, whose IR lowering frees the box (`call __fn___fern_arr_dec`,
// the shared rc-release helper) while the AST path leaks it. If the const ref
// had bailed, the WHOLE module would fall back to AST and that struct-free would
// be absent. So the marker's presence proves the const ref did not bail the
// module — and the exit code proves the const's value is right.
func TestSelfHostConstRefX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
	}
	run := func(t *testing.T, asmText string) int {
		t.Helper()
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, []byte(asmText), 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, asmText)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally")
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// A const used to initialise a struct field. The struct free proves the
		// module went IR; 40 + 2 = 42 proves the const's value.
		{"const-in-struct-field", `const BASE = 40; struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: BASE, y: 2 }; return p.x + p.y; }`, 42},
		// A const used in arithmetic, alongside a struct that anchors the marker.
		{"const-in-arith", `const STEP = 7; struct Q { a: i32, b: i32 } function main(): i32 { var q: Q = Q { a: 1, b: 2 }; return STEP + STEP + q.a + q.b; }`, 17},
		// A boolean const driving a branch, alongside a struct.
		{"bool-const", `const ON = true; struct R { v: i32, w: i32 } function main(): i32 { var r: R = R { v: 5, w: 4 }; if (ON) { return r.v + r.w; } return 1; }`, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := emit(t, tc.src)
			// The struct free is emitted only on the IR path. Its presence proves
			// the const ref did not bail the module to the leak-only AST backend.
			if !strings.Contains(out, "call __fn___fern_arr_dec") {
				t.Errorf("%q: struct free absent — const ref bailed the module to AST?", tc.name)
			}
			if got := run(t, out); got != tc.expected {
				t.Errorf("const-ref x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

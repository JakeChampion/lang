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

// TestSelfHostStructRcX86IR gates shallow struct reference counting on the IR
// path: an owned struct local is freed (a shallow __fern_rc_dec of the box) at
// scope exit / loop-rebind, with alias-inc on `var s2 = s1` and move-on-return —
// mirroring the array RC. Leak-safe structs (scalar/string fields) only, so no
// rc-tracked field dangles. Asserts BOTH the correct value (a double-free would
// corrupt it) AND that the struct free is actually emitted (`__fern_rc_dec`), so
// it can't silently regress to leak-only.
func TestSelfHostStructRcX86IR(t *testing.T) {
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
		name       string
		src        string
		expected   int
		wantStruct bool // asserts the struct free (__fern_rc_dec) is emitted
	}{
		// Loop-rebind: each `var p` frees the prior one. sum_{i=0..9} 3i = 135.
		{"loop-rebind-free", `struct P { x: i32, y: i32 } function use_p(p: P): i32 { return p.x + p.y; } function main(): i32 { var total: i32 = 0; var i: i32 = 0; while (i < 10) { var p: P = P { x: i, y: i * 2 }; total = total + use_p(p); i = i + 1; } return total; }`, 135, true},
		// Alias + return: alias-inc keeps both refs balanced; returned struct is moved.
		{"alias-and-return", `struct Pt { x: i32, y: i32 } function mk(a: i32): Pt { var p: Pt = Pt { x: a, y: a + 1 }; var q: Pt = p; return q; } function main(): Pt { return mk(20); } `, 0, true},
		{"alias-and-read", `struct Pt { x: i32, y: i32 } function main(): i32 { var p: Pt = Pt { x: 7, y: 13 }; var q: Pt = p; return q.x + q.y + p.x; }`, 27, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := emit(t, tc.src)
			// The struct free lowers to a `call __fn___fern_arr_dec` (x86 maps the
			// generic rc-dec to the shared array-release helper). These programs use
			// NO arrays, so any such call is a struct free — its absence means the
			// path regressed to leak-only.
			if tc.wantStruct && !strings.Contains(out, "call __fn___fern_arr_dec") {
				t.Errorf("%q: struct free not emitted (regressed to leak-only?)", tc.name)
			}
			// "alias-and-return" returns a struct (pointer); exit code isn't a
			// meaningful scalar, so only the emitted-free assertion applies.
			if tc.name == "alias-and-return" {
				return
			}
			if got := run(t, out); got != tc.expected {
				t.Errorf("struct-RC x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

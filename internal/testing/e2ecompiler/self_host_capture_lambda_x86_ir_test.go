package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostCaptureLambdaX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(emitted)
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
		{"single-capture", `function main(): i32 { let base: i32 = 20; let add = (x: i32): i32 => { return x + base; }; return add(5) + add(10); }`, 55},
		{"capture-param", `function f(base: i32): i32 { let g = (x: i32): i32 => { return x * base; }; return g(3) + g(4); } function main(): i32 { return f(10); }`, 70},
		{"multi-capture", `function main(): i32 { let a: i32 = 7; let b: i32 = 3; let combine = (x: i32): i32 => { return x + a - b; }; return combine(10); }`, 14},
		{"capture-in-loop", `function main(): i32 { let step: i32 = 2; let bump = (x: i32): i32 => { return x + step; }; let total: i32 = 0; let i: i32 = 0; while (i < 3) { total = bump(total); i = i + 1; } return total; }`, 6},
		{"arr-literal-capture", `function main(): i32 { let a = [10, 20, 30]; let len = (): i32 => { return a.len(); }; return len(); }`, 3},
		{"arr-literal-index", `function main(): i32 { let a = [3, 5, 9]; let third = (): i32 => { return a[2]; }; return third(); }`, 9},
		{"strarr-literal-capture", `function main(): i32 { let a = ["x", "y"]; let len = (): i32 => { return a.len(); }; return len(); }`, 2},
		{"struct-literal-capture", `struct P { x: i32 } function main(): i32 { let p = P { x: 42 }; let get = (): i32 => { return p.x; }; return get(); }`, 42},
		{"i64-capture-inferred-ret", `function main(): i32 { let base: i64 = 7000000000; let f = () => base + 2000000000; return (f() / 1000000000) as i32; }`, 9},
		{"i64-param-inferred-ret", `function main(): i32 { let f = (x: i64) => x + 1; return f(5) as i32; }`, 6},
		{"u64-param-inferred-ret", `function main(): i32 { let f = (x: u64) => x + 3; return f(5) as i32; }`, 8},

		{"boxed-shadowed-by-for-binder", `function main(): i32 { let n: i32 = 0; let bump = (): i32 => { n = n + 1; return n; }; let s: i32 = bump() + bump(); let xs: i32[] = [0, 1, 2]; for n in xs { s = s + n; } return s; }`, 6},
		{"boxed-shadowed-by-lambda-param", `function main(): i32 { let n: i32 = 0; let bump = (): i32 => { n = n + 1; return n; }; let twice = (n: i32) => n + n; let a: i32 = bump(); return a + twice(10); }`, 21},
		{"boxed-shadowed-by-match-binder", `function pick(k: i32): Option[i32] { if (k > 0) { return Some(41); } return None; } function main(): i32 { let n: i32 = 0; let bump = (): i32 => { n = n + 1; return n; }; let s: i32 = bump() + bump(); match (pick(1)) { Some(n) => { s = s + n; }, None => { s = s + 100; } } return s; }`, 44},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := emit(t, tc.src)
			if !strings.Contains(out, "__lam_0") {
				t.Errorf("%q did not reach the IR path (no __lam_0 — lambda-lift bailed to AST)", tc.name)
			}
			if got := run(t, out); got != tc.expected {
				t.Errorf("capture-lambda x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// methodFnArgIRCases pass a function VALUE (a bare function name) to a METHOD's
// "fn"-typed parameter. The free-call arg site already lowered such a bare name
// to const_func (a function pointer) when the param is "fn"-typed; the method-
// dispatch arg loops did not, so the bare name fell through to the const path
// and was mis-lowered as a CALL — corrupting the call frame (segfault). The
// shared lower_call_arg now applies the same fn-value rule (keyed by the
// method's "<Type>.<method>" label) on every dispatch path, so a fn-value method
// argument lowers correctly and the whole module stays on the IR path.
var methodFnArgIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"void-fn-arg", `struct Foo { n: i32 } pub function (f: Foo) call_one(fn: () => void): Foo { fn(); return Foo { n: f.n + 99 }; } function noop() { } function main(): i32 { var f: Foo = Foo { n: 0 }; f = f.call_one(noop); return f.n; }`, 99},
	{"i32-fn-arg", `struct Calc { base: i32 } pub function (c: Calc) apply(fn: (i32) => i32, x: i32): i32 { return c.base + fn(x); } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var c = Calc { base: 10 }; return c.apply(dbl, 5); }`, 20},
	{"two-fn-args", `struct R { } pub function (r: R) combine(f: (i32) => i32, g: (i32) => i32, x: i32): i32 { return f(x) + g(x); } function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { var r = R { }; return r.combine(inc, dbl, 10); }`, 31},
}

// TestSelfHostMethodFnArgX86IR gates fn-value arguments at method-call sites on
// x86-64: each case asserts the program routes through the "ir" path (via
// asm_pathprobe_run) and that the IR path computes the oracle exit code.
func TestSelfHostMethodFnArgX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	probeSrc, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), probeSrc, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	runSrc, err := os.ReadFile("../../examples/self_host/asm_ir_run.fern")
	if err != nil {
		t.Fatalf("read asm_ir_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_ir_run.fern"), runSrc, 0o644); err != nil {
		t.Fatalf("write asm_ir_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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

	for _, tc := range methodFnArgIRCases {
		t.Run(tc.name, func(t *testing.T) {
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if route != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, route)
			}
			if got := run(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("method-fn-arg x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

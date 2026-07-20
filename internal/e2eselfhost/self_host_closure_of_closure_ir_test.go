package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostClosureOfClosureIRX86_64 pins a closure factory that returns a
// (capturing) closure, called through both levels via locals:
// `var outer = make(30); var inner = outer(); inner(12)`. `outer` is a closure
// local (make returns a closure box); calling it yields ANOTHER closure box,
// but `var inner = outer()` wasn't classified as a closure local — the callee
// `outer` is a closure LOCAL, not a module fn in closure_fns — so `inner(x)`
// called the box pointer as code and SIGSEGV'd. The closure-local var-init
// classification now also fires when the callee is a closure local whose
// return type is a fn (a closure returning a closure), so inner dispatches
// env-first and the chain computes the native value.
func TestSelfHostClosureOfClosureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		// Double-nested factory: inner closure captures `base` through two
		// levels; called via `outer()` then `inner(12)`.
		{"closure-of-closure-captured",
			`function make(base: i32): () => (i32) => i32 { return function (): (i32) => i32 { return function (x: i32): i32 { return x + base; }; }; } function main(): i32 { var outer: () => (i32) => i32 = make(30); var inner: (i32) => i32 = outer(); return inner(12); }`,
			42},
		// Triple-nested factory with captures at each level.
		{"closure-of-closure-triple-captured",
			`function make(base: i32): (i32) => (i32) => i32 { return function (a: i32): (i32) => i32 { return function (b: i32): i32 { return a + b + base; }; }; } function main(): i32 { var f: (i32) => (i32) => i32 = make(10); var g: (i32) => i32 = f(12); return g(20); }`,
			42},
		// The inner closure also captures the middle-level parameter, exercised
		// through the two-arg chain.
		{"closure-of-closure-middle-capture",
			`function make(base: i32): (i32) => (i32) => i32 { return function (a: i32): (i32) => i32 { return function (b: i32): i32 { return a * b + base; }; }; } function main(): i32 { var f: (i32) => (i32) => i32 = make(2); var g: (i32) => i32 = f(8); return g(5); }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

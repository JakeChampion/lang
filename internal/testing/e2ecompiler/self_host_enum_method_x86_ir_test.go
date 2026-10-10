package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// enumMethodIRCases call a method on an enum-typed RECEIVER: an enum-valued
// LOCAL (`let d = Heading.N; d.code()`, qualified or bare, unit or payload), a
// method's enum result, or a FRESH variant (`Heading.N.code()`). Each needs the
// receiver's enum type to form the `<Enum>.<method>` label and dispatch
// through the IR path.
var enumMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"qual-unit-local", `enum Heading { N, S } function (d: Heading) code(): i32 { match (d) { Heading.N => { return 7; }, Heading.S => { return 9; } } } function main(): i32 { let d = Heading.S; return d.code(); }`, 9},
	{"bare-unit-local", `enum Heading { N, S } function (d: Heading) code(): i32 { return 7; } function main(): i32 { let d = N; return d.code(); }`, 7},
	{"payload-local", `enum E { A(i32), B } function (e: E) get(): i32 { match (e) { E.A(n) => { return n; }, E.B => { return 0; } } } function main(): i32 { let e = E.A(42); return e.get(); }`, 42},
	{"method-returns-enum", `enum Heading { N, S } function (d: Heading) opp(): Heading { match (d) { Heading.N => { return Heading.S; }, Heading.S => { return Heading.N; } } } function main(): i32 { let d = Heading.N; match (d.opp()) { Heading.N => { return 0; }, Heading.S => { return 1; } } }`, 1},
	{"fresh-variant-method", `enum Heading { N, S } function (d: Heading) code(): i32 { return 7; } function main(): i32 { return Heading.N.code(); }`, 7},
}

// TestSelfHostEnumMethodX86IR gates enum-receiver method dispatch on x86-64:
// each case asserts the program routes through the "ir" path (via
// asm_pathprobe_run) and that the IR path computes the oracle exit code.
func TestSelfHostEnumMethodX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	probeSrc, err := os.ReadFile("../../../compiler/drivers/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_pathprobe_run.fern"), probeSrc, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
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

	for _, tc := range enumMethodIRCases {
		t.Run(tc.name, func(t *testing.T) {
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if route != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, route)
			}
			if got := run(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("enum-method x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

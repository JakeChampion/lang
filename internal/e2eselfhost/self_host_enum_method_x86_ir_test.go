package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// enumMethodIRCases call a method on an enum-typed RECEIVER. Enum methods on a
// PARAM already lowered (the param carries its declared enum type); a method on
// an enum-valued LOCAL (`var d = Dir.N; d.code()`) or a FRESH variant
// (`Dir.N.code()`) did not — the local's enum type was never recorded, so the
// dispatch couldn't form the `<Enum>.<method>` label and bailed to AST.
//
// Two fixes land together: expr_enum_type now resolves a QUALIFIED variant
// (`Dir.N` → `Dir`), and the unannotated-enum-binding recording (#2947) — which
// was DEAD CODE, shadowed by an identical `else if (struct_ty == "")` guard on
// the preceding struct-array-literal branch — is folded into that branch so it
// actually runs. Together a `var d = <variant>` local (qualified or bare,
// unit or payload) records its enum type, and `d.method()` / `Variant.method()`
// dispatch through the IR path.
var enumMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"qual-unit-local", `enum Dir { N, S } function (d: Dir) code(): i32 { match (d) { Dir.N => { return 7; }, Dir.S => { return 9; } } } function main(): i32 { var d = Dir.S; return d.code(); }`, 9},
	{"bare-unit-local", `enum Dir { N, S } function (d: Dir) code(): i32 { return 7; } function main(): i32 { var d = N; return d.code(); }`, 7},
	{"payload-local", `enum E { A(i32), B } function (e: E) get(): i32 { match (e) { E.A(n) => { return n; }, E.B => { return 0; } } } function main(): i32 { var e = E.A(42); return e.get(); }`, 42},
	{"method-returns-enum", `enum Dir { N, S } function (d: Dir) opp(): Dir { match (d) { Dir.N => { return Dir.S; }, Dir.S => { return Dir.N; } } } function main(): i32 { var d = Dir.N; match (d.opp()) { Dir.N => { return 0; }, Dir.S => { return 1; } } }`, 1},
	{"fresh-variant-method", `enum Dir { N, S } function (d: Dir) code(): i32 { return 7; } function main(): i32 { return Dir.N.code(); }`, 7},
}

// TestSelfHostEnumMethodX86IR gates enum-receiver method dispatch on x86-64:
// each case asserts the program routes through the "ir" path (via
// asm_pathprobe_run) and that the IR path computes the oracle exit code.
func TestSelfHostEnumMethodX86IR(t *testing.T) {
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

	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
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

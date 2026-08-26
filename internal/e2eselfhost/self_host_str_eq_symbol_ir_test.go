package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__fern_str_eq(a, b)` is the RUNTIME SYMBOL accepted as surface syntax, the
// same shape as the `__rc_dec` / `__fern_rc_dec` hooks next to it in irlower.
//
// It exists for helper sources written on the raw-memory floor (#2649). Those
// hold their strings as i32 box pointers — `keys[i]` read back through
// `__raw_load_ptr` — and `==` on two i32s is an integer compare, so without
// this spelling there is no way to reach the comparison at all. That is what
// blocks `__fern_map_delete`'s string-key arm from being written in Fern.
//
// Only the self-host accepts it; native registers no such builtin, so a source
// using it is a self-host dialect program, exactly as the RC hooks are.
const strEqSymbolSrc = `// The i32 spelling the runtime helpers use: strings held as raw box pointers.
// Compiled to prove it type-checks; not called, since fabricating a box
// pointer to pass it would prove nothing about the comparison.
function eqraw(p: i32, q: i32): boolean { return __fern_str_eq(p, q); }

function main(): i32 {
    var a: string = "hello";
    var b: string = "hel" + "lo";
    var c: string = "world";
    // Two DISTINCT boxes: literals are interned, so "hello" twice would be
    // pointer-identical and the comparison would pass without comparing.
    if (__raw_data(a) == __raw_data(b)) { return 90; }
    if (!__fern_str_eq(a, b)) { return 91; }
    if (__fern_str_eq(a, c)) { return 92; }
    if (__fern_str_eq("", "")) { } else { return 93; }
    if (__fern_str_eq("ab", "abc")) { return 94; }
    return 42;
}
`

// TestSelfHostStrEqSymbolIRX86_64 runs it end to end on x86-64.
func TestSelfHostStrEqSymbolIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := string(runCapture(t, gcc, runner, driverBin, []byte(strEqSymbolSrc), "-ir"))
	if len(asm) == 0 {
		t.Fatal("self-host emitted 0 bytes")
	}
	// The recognition must reach the shared comparison helper, not open-code
	// an integer compare — that is the whole point of the spelling.
	if !strings.Contains(asm, "__fern_str_eq") {
		t.Errorf("emitted asm never calls __fern_str_eq — the builtin lowered to something else:\n%s", asm)
	}
	bin := buildBin(t, gcc, dir, "streq", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 42 {
		t.Errorf("exited %d, want 42 (90=literals interned to one box so the "+
			"comparison proved nothing, 91=equal strings compared unequal, "+
			"92=unequal compared equal, 93=empty vs empty, 94=prefix matched)", got)
	}
}

// TestSelfHostStrEqSymbolIRArm64 is the same program on arm64 under qemu. The
// op is target-independent, but the emitters are not: str_eq is selected
// separately on each backend.
func TestSelfHostStrEqSymbolIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(strEqSymbolSrc), "-target", "arm64-linux"))
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "streq_arm64", asm)
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 42 {
		t.Errorf("arm64 exited %d, want 42", got)
	}
}

// TestSelfHostStrEqSymbolTypeChecks pins that the front end ACCEPTS the
// spelling, which the emit drivers cannot show: `asm_ir_run.fern` does not
// type-check at all, so a program driven through it compiles whatever it is
// handed. The control below is what makes this assertion mean something.
func TestSelfHostStrEqSymbolTypeChecks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	check := func(t *testing.T, name, src string) (string, int) {
		t.Helper()
		p := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(cli, p)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), cli, p)...)
		}
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	// Control: this front end really does reject a type error. Without it, a
	// pass below would only show that nothing was checked.
	if out, code := check(t, "control", "function main(): i32 {\n    var x: i32 = \"hello\";\n    return x;\n}\n"); code == 0 {
		t.Fatalf("the control program type-checked — this front end is not checking, so the assertion below proves nothing (out=%q)", out)
	} else if !strings.Contains(out, "E003") {
		t.Fatalf("control rejected but not with E003: %q", out)
	}

	if out, code := check(t, "streq", strEqSymbolSrc); code != 0 {
		if len(out) > 300 {
			out = out[:300]
		}
		t.Errorf("__fern_str_eq did not type-check: exit=%d out=%q", code, out)
	}
}

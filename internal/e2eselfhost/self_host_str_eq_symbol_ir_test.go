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
// hold a string as the usize address of its box — `keys[i]` read back through
// `__raw_load_ptr` — and `==` on two usizes is an integer compare, so without
// this spelling there is no way to reach the comparison at all.
//
// Only the self-host accepts it; native registers no such builtin, so a source
// using it is a self-host dialect program, exactly as the RC hooks are.
const strEqSymbolSrc = `// A string box is {data, len}. box() builds a fresh one over s's bytes, the
// way a helper holds a key: as the usize address of its box.
function box(s: string): usize {
    var p: usize = __raw_alloc(16);
    __raw_store_ptr(p, 0, __raw_data(s));
    __raw_store_ptr(p, 1, s.len() as usize);
    return p;
}

function main(): i32 {
    var a: string = "hello";
    var b: string = "hel" + "lo";
    var c: string = "world";
    // Two DISTINCT data blocks: literals are interned, so "hello" twice would
    // share its bytes and a data-pointer shortcut would pass without comparing.
    if (__raw_data(a) == __raw_data(b)) { return 90; }
    if (!__fern_str_eq(box(a), box(b))) { return 91; }
    if (__fern_str_eq(box(a), box(c))) { return 92; }
    if (__fern_str_eq(box(""), box(""))) { } else { return 93; }
    if (__fern_str_eq(box("ab"), box("abc"))) { return 94; }
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
// handed. The controls below are what make this assertion mean something. The
// typed path is pinned on and strict, so a program the checker accepts but the
// typed path refuses fails here too.
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
		cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1")
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

	// A string is not a box address: the checker types the helper's operands
	// as the table has them.
	if out, code := check(t, "streq_string", "function main(): i32 {\n    if (__fern_str_eq(\"a\", \"b\")) { return 1; }\n    return 0;\n}\n"); code == 0 {
		t.Errorf("__fern_str_eq type-checked with string operands; its operands are usize box addresses (out=%q)", out)
	} else if !strings.Contains(out, "E038") {
		t.Errorf("string operands to __fern_str_eq rejected but not with E038: %q", out)
	}

	if out, code := check(t, "streq", strEqSymbolSrc); code != 0 {
		if len(out) > 300 {
			out = out[:300]
		}
		t.Errorf("__fern_str_eq did not type-check: exit=%d out=%q", code, out)
	}
}

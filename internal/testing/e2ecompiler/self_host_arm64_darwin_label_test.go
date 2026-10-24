package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const darwinLabelSrc = `function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function main(): i32 { return fib(7); }`

const darwinLabelStrSrc = `function main(): i32 {
    print(".Lnot_a_label");
    return 0;
}`

func selfHostArm64Asm(t *testing.T, target string) string {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	out := runCaptureEnv(t, runner, bin, []byte(darwinLabelSrc),
		[]string{"PATH=/usr/bin:/bin"}, "-target", target)
	if len(out) == 0 {
		t.Fatalf("self-host emitted 0 bytes for %s", target)
	}
	return string(out)
}

func TestSelfHostArm64DarwinUsesMachOLocalPrefix(t *testing.T) {
	dar := selfHostArm64Asm(t, "arm64-darwin")
	if n := countOutsideStrings(dar, ".L"); n != 0 {
		t.Errorf("the self-host Darwin listing still has %d ELF-style .L labels outside string data", n)
	}
	if !strings.Contains(dar, "L") {
		t.Fatal("no labels at all in the Darwin listing; the fixture stopped exercising them")
	}
}

func TestSelfHostArm64LinuxKeepsDotL(t *testing.T) {
	lin := selfHostArm64Asm(t, "arm64-linux")
	if n := countOutsideStrings(lin, ".L"); n == 0 {
		t.Error("the self-host Linux listing lost its .L labels — the rewrite is not Darwin-only")
	}
}

func TestSelfHostArm64DarwinRegisterEntriesAreLocal(t *testing.T) {
	dar := selfHostArm64Asm(t, "arm64-darwin")
	if !strings.Contains(dar, "\nL__fn_fib.r:") || !strings.Contains(dar, " L__fn_fib.r\n") {
		t.Fatal("the Darwin listing has no local register entry for fib and no call to it")
	}
	if strings.Contains(strings.ReplaceAll(dar, "L__fn_", ""), "__fn_fib.r") {
		t.Error("the Darwin listing still names __fn_fib.r as a symbol")
	}
	if !strings.Contains(selfHostArm64Asm(t, "arm64-linux"), "\n__fn_fib.r:") {
		t.Error("the Linux listing lost its __fn_fib.r symbol — the rewrite is not Darwin-only")
	}
	mc, err := exec.LookPath("llvm-mc")
	if err != nil {
		t.Skip("llvm-mc not on PATH; the listing's shape is checked, its assembly is not")
	}
	src := filepath.Join(t.TempDir(), "fib.s")
	if err := os.WriteFile(src, []byte(dar), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(mc, "-triple", "arm64-apple-macos", "-filetype=obj", "-o", os.DevNull, src).CombinedOutput(); err != nil {
		t.Errorf("llvm-mc refuses the Darwin listing: %v\n%s", err, out)
	}
}

func TestSelfHostArm64DarwinStringLiteralKeepsDotL(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	out := runCaptureEnv(t, runner, bin, []byte(darwinLabelStrSrc),
		[]string{"PATH=/usr/bin:/bin"}, "-target", "arm64-darwin")
	if !strings.Contains(string(out), ".Lnot_a_label") {
		t.Error(`the string literal ".Lnot_a_label" was rewritten in the self-host Darwin listing — string data is not a symbol`)
	}
}

const darwinLo12StrSrc = `function main(): i32 {
    print("    add x9, x9, :lo12:.Lfern_relanchor");
    return 0;
}`

func TestSelfHostArm64DarwinStringLiteralKeepsLo12(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	out := string(runCaptureEnv(t, runner, bin, []byte(darwinLo12StrSrc),
		[]string{"PATH=/usr/bin:/bin"}, "-target", "arm64-darwin"))
	if !strings.Contains(out, `:lo12:.Lfern_relanchor"`) {
		t.Error(`the string literal's ":lo12:" was rewritten in the self-host Darwin listing - string data is not an operand`)
	}
	if strings.Contains(out, `"@PAGEOFF`) {
		t.Error(`a .ascii data line carries a @PAGEOFF suffix in the self-host Darwin listing`)
	}
	if n := countOutsideStrings(out, ":lo12:"); n != 0 {
		t.Errorf("the self-host Darwin listing still has %d :lo12: operands outside string data", n)
	}
	if !strings.Contains(out, "@PAGEOFF") {
		t.Error("the self-host Darwin listing has no @PAGEOFF operand at all")
	}
}

func countOutsideStrings(asm, sub string) int {
	n := 0
	for _, line := range strings.Split(asm, "\n") {
		inStr := false
		for i := 0; i < len(line); i++ {
			if inStr {
				if line[i] == '\\' {
					i++
					continue
				}
				if line[i] == '"' {
					inStr = false
				}
				continue
			}
			if line[i] == '"' {
				inStr = true
				continue
			}
			if strings.HasPrefix(line[i:], sub) {
				n++
			}
		}
	}
	return n
}

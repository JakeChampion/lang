package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArm64EnvpSaveIR pins the envp-save in the arm64 IR-path _start.
// env() (and subprocess) read __fern_envp, a .bss slot the _start prologue is
// supposed to seed from the SysV entry stack (envp = sp + 16 + argc*8). The AST
// _start (asm_arm64.emit_module) saved it, but BOTH IR _start emitters —
// asm_arm64_ir.emit_body (single-module) and asm_arm64.emit_module_ir_unit_arm64
// (multi-unit) — omitted it, so __fern_envp stayed null and __fern_env's envp
// walk (`ldr x1, [x19]` on a null base) SIGSEGV'd. Every env()-calling program
// that routed the IR path crashed — which was the arm64 std-test
// env_unreachable / lang_binary_e2e failures.
//
// A small env() program routes the IR path (no strings-heavy stdlib to bail it
// to AST), so this compiles one to aarch64 on the x86 host and asserts the
// _start prologue seeds __fern_envp. Pure emission check — no qemu, runs on
// every x86 CI lane.
func TestSelfHostArm64EnvpSaveIR(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_arm64")

	prog := "function main(): i32 {\n" +
		"    match (env(\"PATH\")) {\n" +
		"        Some(v) => { return v.len(); },\n" +
		"        None => { return 0; }\n" +
		"    }\n" +
		"    return 0;\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "env_ir.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)
	// The Fern-compiled symbol specifically (#2649): a bare "__fern_env:" match
	// is also a substring of "__fn___fern_env:", so it would keep passing
	// whichever of the two the emitter produced.
	if !strings.Contains(asm, "__fn___fern_env:") {
		t.Fatal("__fn___fern_env helper not emitted — env() did not lower as expected")
	}

	// The _start prologue must seed __fern_envp (else env()'s walk derefs null).
	start := arm64StartBody(t, asm)
	if !strings.Contains(start, "__fern_envp") {
		t.Errorf("_start does not save envp (the IR-path env() SIGSEGV); _start:\n%s", start)
	}
}

// arm64StartBody returns the _start prologue up to the `bl __fn_main` dispatch.
func arm64StartBody(t *testing.T, asm string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "_start:" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("_start not found in emitted asm")
	}
	var b strings.Builder
	for i := start + 1; i < len(lines); i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
		if strings.Contains(lines[i], "bl __fn_main") {
			break
		}
	}
	return b.String()
}

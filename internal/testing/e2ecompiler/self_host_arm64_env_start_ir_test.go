package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArm64EnvpSaveIR(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc_arm64")

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
	if !strings.Contains(asm, "__fn___fern_env:") {
		t.Fatal("__fn___fern_env helper not emitted — env() did not lower as expected")
	}

	start := arm64StartBody(t, asm)
	if !strings.Contains(start, "__fern_envp") {
		t.Errorf("_start does not save envp (the IR-path env() SIGSEGV); _start:\n%s", start)
	}
}

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

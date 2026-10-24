package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostLocalShadowsImportedDecl(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	prog := filepath.Join(t.TempDir(), "shadow_prog.fern")
	src := "" +
		"import \"std/math\";\n" +
		"function main(): i32 { return math.random_int(0, 1); }\n"
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("self-host compile failed: %v (asm %d bytes)", err, len(asm))
	}
	bin := buildBin(t, gcc, dir, "shadow_prog", string(asm))
	for i := 0; i < 20; i++ {
		c := exec.Command(bin)
		c.Run()
		if code := c.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("random_int(0,1) returned %d, want 0 (local `range` mis-resolved to range() function)", code)
		}
	}
}

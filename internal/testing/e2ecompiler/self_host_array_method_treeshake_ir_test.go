package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArrayMethodTreeshakeIR(t *testing.T) {
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

	const src = `import "std/array";
function joined(): string {
    let ss: string[] = ["1", "2", "3"];
    return ss.join("-");
}
function main(): i32 {
    if (joined() == "1-2-3") { return 0; }
    return 1;
}`
	prog := filepath.Join(dir, "join_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}

	decideOut, err := exec.Command(mmc, prog, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("-decide failed: %v", err)
	}
	if got := strings.TrimSpace(string(decideOut)); got != "ir" {
		t.Fatalf("array-method program routed %q, want \"ir\" (treeshake pruned join)", got)
	}

	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("self-host compile failed: %v (len=%d)", err, len(asm))
	}
	bin := buildBin(t, gcc, dir, "join_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Errorf("join IR program exited %d, want 0 (\"1-2-3\")", code)
	}
}

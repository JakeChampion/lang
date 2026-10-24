package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostLargeTierFreelistX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const prog = `function main(): i32 {
    let i: i32 = 0;
    let ok: i32 = 0;
    while (i < 4000) {
        let a: i64[] = [];
        let j: i32 = 0;
        while (j < 70000) { a = a.append((j + i) as i64); j = j + 1; }
        if (a.len() == 70000 && a[69999] == (69999 + i) as i64 && a[0] == i as i64) { ok = ok + 1; }
        i = i + 1;
    }
    if (ok == 4000) { return 42; }
    return 1;
}
`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(prog))
	emitted, err := cmd.Output()
	if err != nil || len(emitted) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	asm := string(emitted)
	for _, sym := range []string{"__fern_large_freelist", "__fern_large_push", ".Lalloc_large"} {
		if !strings.Contains(asm, sym) {
			t.Fatalf("emitted runtime missing large-tier symbol %q — the new path was not compiled in", sym)
		}
	}
	innerAsm := filepath.Join(dir, "largetier.s")
	innerBin := filepath.Join(dir, "largetier")
	if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
		t.Fatalf("write inner asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
		t.Fatalf("inner gcc: %v\n%s", err, out)
	}
	var rcmd *exec.Cmd
	if len(runner) == 0 {
		rcmd = exec.Command(innerBin)
	} else {
		rcmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
	}
	_ = rcmd.Run()
	if rcmd.ProcessState == nil {
		t.Fatal("large-tier churn binary did not run")
	}
	got := rcmd.ProcessState.ExitCode()
	if got == 137 {
		t.Fatalf("large-tier churn OOMed (exit 137) — the arena exhausted, so large blocks are still leaking (freelist not recycling)")
	}
	if got != 42 {
		t.Fatalf("large-tier churn = %d, want 42 (a non-42/non-137 code means a mis-binned reuse corrupted the array)", got)
	}
}

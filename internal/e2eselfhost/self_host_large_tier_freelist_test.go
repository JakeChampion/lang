package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostLargeTierFreelistX86_64 guards the large-tier freelist (#3425): the
// self-host x86 runtime's 65536-word __fern_freelist recycles blocks <512 KiB
// exactly, but blocks >=512 KiB used to LEAK (no size class), so a long-running
// emit that frees big per-function arrays exhausted the arena (exit 137) — the
// self-host runtime lagging native's two-tier design and the root cause of the
// per-module gen1 self-compile OOM.
//
// __fern_alloc.Lalloc_large now rounds a large request up to its 3-significant-bit
// capacity, bumps at THAT (so every block in a class has one size — native's reuse
// invariant), and pops __fern_large_freelist[class]; the free sites (__fern_arr_dec
// data buffers, __fern_str_free string buffers) push back via __fern_large_push
// instead of leaking. This churns 4000 iterations of a fresh 560 KiB i64 array
// (alloc -> read -> drop -> recycle): a leak OOMs (exit 137) once the churned bytes
// exceed the arena, and a mis-binned reuse (a smaller block handed to a larger
// same-class request) overflows and returns a wrong sum — so a correct+recycling
// large tier is the only way this returns 42. The emitted asm must also carry the
// large-tier symbols (else the program never hit the new path).
func TestSelfHostLargeTierFreelistX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// 4000 iterations of a fresh 70000-element i64 array (560 KiB > 512 KiB): the
	// >=512 KiB doubling-growth buffers are freed each iteration and must recycle,
	// else ~4000*~1.5 MiB churn exhausts the arena. Reads a[0]/a[last]/len so a
	// mis-sized reuse surfaces as a wrong count.
	const prog = `function main(): i32 {
    var i: i32 = 0;
    var ok: i32 = 0;
    while (i < 4000) {
        var a: i64[] = [];
        var j: i32 = 0;
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
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(prog))
	emitted, err := cmd.Output()
	if err != nil || len(emitted) == 0 {
		t.Fatalf("driver -ir failed: %v", err)
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

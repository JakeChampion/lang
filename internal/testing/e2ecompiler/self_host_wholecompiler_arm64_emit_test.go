package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelfHostWholeCompilerArm64SingleProcessEmit(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI takes host paths as argv, so this runs only natively on x86-64")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	out := filepath.Join(t.TempDir(), "fern_arm64.s")
	start := time.Now()
	cmd := exec.Command(fernBin, "-target", "arm64-linux", "-emit", "asm", "-o", out, filepath.Join(dir, "fern.fern"), stdlibRoot)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the self-host compiler could not emit the whole compiler for arm64 in one process: %v\n%s", err, msg)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read emitted asm: %v", err)
	}
	t.Logf("whole-compiler arm64 emit: %d bytes in %.1fs", len(asm), time.Since(start).Seconds())
	const floor = 6 << 20
	if len(asm) < floor {
		t.Errorf("emitted %d bytes; the whole compiler is far larger than %d", len(asm), floor)
	}
	if !strings.Contains(string(asm), "__fern_buf_reserve:") {
		t.Error("the emitted compiler has no __fern_buf_reserve; its own output accumulator would be a fixed buffer again")
	}
}

package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSelfHostWholeCompilerArm64SingleProcessEmit emits the WHOLE compiler for
// -target arm64-linux in ONE process of the self-host CLI: the path #8212
// segfaulted on with no diagnostic. The emitted text accumulates in the
// string builder, and at this scale (63 MB after the peephole, more before
// it) it passed the 64 MiB the builder reserved as a fixed .bss buffer and
// overwrote the words after it. Neither arm64 route CI already ran reached
// that shape: the per-module whole-compiler build emits one unit per process
// and the fixture corpus is small programs. Exit 0 and a compiler-sized
// output are the assertions here; what the bytes say is the fixpoint and
// fixture legs' job.
//
// Builds fern.fern, so it is in ISOLATED_DRIVER_TESTS (test-e2e-selfhost.yml)
// and runs in the cli job against the driver that job just built. The emit
// alone is ~26 s natively on an M-series Mac.
func TestSelfHostWholeCompilerArm64SingleProcessEmit(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI takes host paths as argv, so this runs only natively on x86-64")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	out := filepath.Join(t.TempDir(), "fern_arm64.s")
	start := time.Now()
	// The staged copy of fern.fern, so the input is the same bytes the driver
	// was built from.
	cmd := exec.Command(fernBin, "-target", "arm64-linux", "-emit", "asm", filepath.Join(dir, "fern.fern"), stdlibRoot, "-o", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the self-host compiler could not emit the whole compiler for arm64 in one process: %v\n%s", err, msg)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read emitted asm: %v", err)
	}
	t.Logf("whole-compiler arm64 emit: %d bytes in %.1fs", len(asm), time.Since(start).Seconds())
	// The compiler's arm64 text is ~63 MB; a tenth of that is still far more
	// than any program short of the compiler emits, and rules out an emit that
	// exited 0 with a truncated accumulator.
	const floor = 6 << 20
	if len(asm) < floor {
		t.Errorf("emitted %d bytes; the whole compiler is far larger than %d", len(asm), floor)
	}
	if !strings.Contains(string(asm), "__fern_strbuf_grow:") {
		t.Error("the emitted compiler has no __fern_strbuf_grow; its own output accumulator would be a fixed buffer again")
	}
}

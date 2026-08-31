package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The `-backend ssa` (arm64) legs of the two fused SIMD kernels
// (docs/ATLAS-PLATFORM-PLAN.md §3). They run the SAME corpora as the native
// legs in memchr_test.go / ascii_run_test.go rather than a corpus of their
// own, which is the whole point: seven backends answer these ops and the only
// property that matters is that they answer identically.
//
// This backend is the one §3.4 miscounted — it was the seventh of seven, was
// missed when __memchr was adopted, and reported it as `branch to undefined
// label "fn___fern_memchr"`. It then stayed scalar while the natives were
// vectorised, so the sweep over every 16-byte block boundary is new signal
// here even though the corpus is not.
//
// The two CLI-roundtrip cases in arm64_ssa_test.go stay: they name the
// dependency at a glance and are short enough to read. Neither reaches past
// one vector block, which is exactly what these add.

// arm64SSACorpusRunner builds the fern CLI once and returns the runner
// shape the shared corpora take: compile with `-backend ssa`, run the ELF
// (natively on arm64, under qemu elsewhere), hand back stdout.
func arm64SSACorpusRunner(t *testing.T) func(t *testing.T, src string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}

	return func(t *testing.T, src string) string {
		t.Helper()
		srcPath := filepath.Join(dir, "corpus.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out := filepath.Join(dir, "corpus.bin")
		emit := exec.Command(bin, "-target", "arm64-linux", "-backend", "ssa", "-o", out, srcPath)
		if b, err := emit.CombinedOutput(); err != nil {
			t.Fatalf("fern -target arm64-linux -backend ssa: %v\n%s", err, b)
		}
		run := runArm64Bin(qemu, out)
		stdout, _ := run.CombinedOutput()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatal("program did not exit normally")
		}
		if code := run.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("program exited %d, want 0\noutput:\n%s", code, stdout)
		}
		return strings.TrimRight(string(stdout), "\n")
	}
}

func TestArm64SSAMemchr(t *testing.T) {
	runMemchrCorpus(t, arm64SSACorpusRunner(t))
}

func TestArm64SSAAsciiRun(t *testing.T) {
	runAsciiRunCorpus(t, arm64SSACorpusRunner(t))
}

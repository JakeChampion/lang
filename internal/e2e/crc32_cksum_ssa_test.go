package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The `-backend ssa` legs of __crc32_cksum, running the SAME 487-case corpus
// the native and wasm legs run. Both bodies are scalar
// (docs/ATLAS-PLATFORM-PLAN.md §3.4 step 1) where the two native emitters
// fold, so this is the pair that says the fold and the definition agree on a
// third and fourth implementation rather than on a restatement of one.
//
// These backends are the ones §3.4 has been missed on before: `-backend ssa`
// was the seventh of seven when __memchr was adopted, and reported the gap as
// `branch to undefined label`. A corpus that only runs the flat backends
// cannot see that, because nothing routes through here.

// x86_64SSACorpusRunner mirrors arm64SSACorpusRunner: build the fern CLI once,
// then compile each program with `-backend ssa` and run the ELF. The in-process
// harness cannot stand in — it calls x86_64.Emit directly, which is the flat
// backend and not the one under test.
func x86_64SSACorpusRunner(t *testing.T) func(t *testing.T, src string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("x86-64-ssa not exercised on windows")
	}
	_, exec_ := e2eharness.X86_64Tooling(t)

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
		emit := exec.Command(bin, "-target", "x86-64-linux", "-backend", "ssa", "-o", out, srcPath)
		if b, err := emit.CombinedOutput(); err != nil {
			t.Fatalf("fern -target x86-64-linux -backend ssa: %v\n%s", err, b)
		}
		run := e2eharness.RunX86_64Bin(exec_, out)
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

func TestX86_64SSACrc32Cksum(t *testing.T) {
	runCrc32CksumCorpus(t, x86_64SSACorpusRunner(t))
}

func TestArm64SSACrc32Cksum(t *testing.T) {
	runCrc32CksumCorpus(t, arm64SSACorpusRunner(t))
}

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const loopTailLabelProgram = `@noinline
function walk(n: i32, seen: i32): i32 {
    if (n > 0) { return walk(n - 1, seen + 1); }
    return seen;
}
function main(): i32 {
    if (walk(300000, 0) == 300000) { return 7; }
    return 1;
}
`

func TestSelfHostSSALoopTailBlockEmittedOnce(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "tco.fern")
	if err := os.WriteFile(src, []byte(loopTailLabelProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("x86-64", func(t *testing.T) {
		asm := emitSSAAsm(t, fernBin, stdlibRoot, src, "x86-64-linux")
		assertNoDuplicateLocalLabels(t, "x86-64 tco asm", asm)
		assertNoDanglingLocalLabels(t, "x86-64 tco asm", asm)
		bin := buildBin(t, gcc, t.TempDir(), "tco", string(asm))
		if out, code := runBin(exec.Command(bin), ""); code != 7 || out != "" {
			t.Fatalf("x86-64 run = %q exit %d, want %q exit 7", out, code, "")
		}
	})

	t.Run("arm64", func(t *testing.T) {
		armGCC, qemu := arm64Tooling(t)
		asm := emitSSAAsm(t, fernBin, stdlibRoot, src, "arm64-linux")
		assertNoDuplicateLocalLabels(t, "arm64 tco asm", asm)
		assertNoDanglingLocalLabels(t, "arm64 tco asm", asm)
		bin := buildBinArm64(t, armGCC, t.TempDir(), "tco", string(asm))
		if out, code := runBin(runArm64Bin(qemu, bin), ""); code != 7 || out != "" {
			t.Fatalf("arm64 run = %q exit %d, want %q exit 7", out, code, "")
		}
	})
}

func emitSSAAsm(t *testing.T, fernBin, stdlibRoot, src, target string) []byte {
	t.Helper()
	cmd := exec.Command(fernBin, "-backend", "ssa", "-target", target, "-emit", "asm", src, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	asm, err := cmd.Output()
	if err != nil {
		t.Fatalf("emit %s asm: %v\n%s", target, err, stderr.String())
	}
	if len(asm) == 0 {
		t.Fatalf("emit %s asm: empty listing", target)
	}
	if !strings.Contains(string(asm), ".Lssa_walk_") {
		t.Fatalf("emit %s asm: no .Lssa_walk_ label in the listing", target)
	}
	return asm
}

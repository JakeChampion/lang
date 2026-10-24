package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	mapHelperBareSrc = `import "core/map";
function main(): i32 { return __map_pow2_ceil(5); }
`
	mapHelperQualifiedSrc = `import "core/map";
function main(): i32 { return map.__map_pow2_ceil(5); }
`
)

func TestSelfHostMapHelpersKeepBareNames(t *testing.T) {
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
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	nativeRun := func(t *testing.T, src string) (compiled bool, exit int, out string) {
		t.Helper()
		caseDir := t.TempDir()
		srcPath := filepath.Join(caseDir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		binPath := filepath.Join(caseDir, "prog")
		build := exec.Command(nativeBin, "-target", "x86-64-linux", "-o", binPath, srcPath, stdlibRoot)
		if b, err := build.CombinedOutput(); err != nil {
			return false, 0, string(b)
		}
		run := exec.Command(binPath)
		_ = run.Run()
		return true, run.ProcessState.ExitCode(), ""
	}

	t.Run("the bare spelling runs on both", func(t *testing.T) {
		natOK, natExit, natErr := nativeRun(t, mapHelperBareSrc)
		if !natOK {
			t.Fatalf("native refused the bare spelling, which it is supposed to accept:\n%s", natErr)
		}
		if natExit != 8 {
			t.Fatalf("native: exit %d, want 8 (__map_pow2_ceil(5))\n%s", natExit, natErr)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target, func(t *testing.T) {
				exit, stderr := selfHostCLIRun(t, selfHostBin, stdlibRoot, mapHelperBareSrc, target)
				if exit != natExit {
					t.Fatalf("self-host: exit %d, native %d\n%s", exit, natExit, stderr)
				}
			})
		}
	})

	t.Run("the qualified spelling is refused by both", func(t *testing.T) {
		if compiled, _, _ := nativeRun(t, mapHelperQualifiedSrc); compiled {
			t.Error("native compiled map.__map_pow2_ceil; the self-host note about matching it is now stale")
		}
		caseDir := t.TempDir()
		srcPath := filepath.Join(caseDir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(mapHelperQualifiedSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(selfHostBin, "-target", "x86-64-linux",
			"-o", filepath.Join(caseDir, "out"), srcPath, stdlibRoot)
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState.ExitCode() == 0 {
			t.Fatalf("the self-host compiled map.__map_pow2_ceil, which native refuses:\n%s", out)
		}
	})
}

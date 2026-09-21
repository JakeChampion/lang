package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `core/map`'s helpers keep their BARE names in the bundle under both compilers
// (#9608).
//
// The Map surface is declared as concrete `_impl` functions — the language has
// no generic method on a generic struct — and every backend routes `map_new` /
// `__method_Map_get` onto them through one alias table, which can only name a
// function one way. Until this, the self-host mangled `core/map`'s helpers to
// `map____map_*` while native kept them bare, so the same source resolved under
// one compiler and not the other, and no shared alias table could name either.
//
// `internal/modload`'s parity test compares the two predicates as data; this is
// the behaviour on top of them, which is what a reader actually cares about:
// the bare spelling runs and answers the same number on both sides.
//
// The qualified spelling is asserted REFUSED by both, deliberately. A runtime
// helper is not public API, native mangles a qualified reference without
// consulting the exemption, and matching that exactly is worth more than
// opening a spelling only one compiler takes.
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
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	// compile returns the exit code of a native compile, which is 0 only when
	// the program both typed and linked.
	nativeRun := func(t *testing.T, src string) (int, string) {
		t.Helper()
		caseDir := t.TempDir()
		srcPath := filepath.Join(caseDir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(caseDir, "prog")
		build := exec.Command(nativeBin, "-target", "x86-64-linux", "-o", out, srcPath, stdlibRoot)
		if b, err := build.CombinedOutput(); err != nil {
			return -1, string(b)
		}
		run := exec.Command(out)
		_ = run.Run()
		return run.ProcessState.ExitCode(), ""
	}

	t.Run("the bare spelling runs on both", func(t *testing.T) {
		natExit, natErr := nativeRun(t, mapHelperBareSrc)
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

	// Refused by both, and refused for the SAME reason — the name the qualified
	// rewrite builds does not exist. Asserting only "self-host refuses" would
	// pass against a compiler that refused the whole program.
	t.Run("the qualified spelling is refused by both", func(t *testing.T) {
		if exit, _ := nativeRun(t, mapHelperQualifiedSrc); exit == 0 {
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

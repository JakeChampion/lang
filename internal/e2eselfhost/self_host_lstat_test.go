package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// lstat is the answer to "is this entry a symlink" that Fern had no way to ask
// (#7982). `stat` follows links and FileStat carries no link bit, so
// `internal/embed`'s `!d.Type().IsRegular()` skip — what keeps an asset tree
// from reaching outside its root or wedging a walk on a cycle — had no spelling
// in the language, and so none in the self-host compiler's own `-embed` either.
//
// Every backend implements it as its stat helper with one constant changed:
// fstatat's flags word gains AT_SYMLINK_NOFOLLOW, preview-1's
// path_filestat_get loses its symlink_follow lookupflag. Sharing the body is
// what makes these tests compare lstat AGAINST stat rather than checking lstat
// alone: a copy that quietly still followed links would agree with stat
// everywhere, and a link to a regular file is a regular file.
func lstatDifferentialSource(file, linkToFile, linkToDir, dir string) string {
	return fmt.Sprintf(`function kind(p: string): i32 {
    match (lstat(p)) {
        Ok(s) => { if (s.is_dir) { return 1; } if (s.is_file) { return 2; } return 0; },
        Err(e) => { return 9; },
    }
    return 9;
}
function skind(p: string): i32 {
    match (stat(p)) {
        Ok(s) => { if (s.is_dir) { return 1; } if (s.is_file) { return 2; } return 0; },
        Err(e) => { return 9; },
    }
    return 9;
}
function main(): i32 {
    if (kind(%[1]q) != 2) { return 1; }
    if (skind(%[1]q) != 2) { return 2; }
    if (kind(%[2]q) != 0) { return 3; }
    if (skind(%[2]q) != 2) { return 4; }
    if (kind(%[3]q) != 0) { return 5; }
    if (skind(%[3]q) != 1) { return 6; }
    if (kind(%[4]q) != 1) { return 7; }
    if (skind(%[4]q) != 1) { return 8; }
    if (kind(%[1]q + ".nope") != 9) { return 9; }
    if (skind(%[1]q + ".nope") != 9) { return 10; }
    return 0;
}
`, file, linkToFile, linkToDir, dir)
}

// lstatTree writes the four paths and returns them. Absolute, because the
// self-host driver and the native CLI are run from different directories.
func lstatTree(t *testing.T, root string) (file, linkToFile, linkToDir, dir string) {
	t.Helper()
	dir = filepath.Join(root, "realdir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file = filepath.Join(root, "real.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	linkToFile = filepath.Join(root, "link_to_file")
	if err := os.Symlink(file, linkToFile); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkToDir = filepath.Join(root, "link_to_dir")
	if err := os.Symlink(dir, linkToDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return file, linkToFile, linkToDir, dir
}

// TestSelfHostLstatMatchesNative runs the same program through both compilers,
// for each of the self-host's three targets.
//
// Compared on the compiled program's exit code, which names the pair that
// disagreed rather than reporting a bare mismatch — the source above returns a
// distinct code per assertion.
func TestSelfHostLstatMatchesNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("lstat differential runs only natively (stats host paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	file, linkToFile, linkToDir, realdir := lstatTree(t, t.TempDir())
	src := filepath.Join(dir, "lstat_differential.fern")
	if err := os.WriteFile(src, []byte(lstatDifferentialSource(file, linkToFile, linkToDir, realdir)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out := t.TempDir()
			nativeArgs := []string{"-target", target, "-o", filepath.Join(out, "n")}
			shArgs := []string{"-target", target, "-o", filepath.Join(out, "s")}
			if target == "wasm32-wasi" {
				nativeArgs = append(nativeArgs, "-emit", "core-module")
				shArgs = append(shArgs, "-emit", "core-module")
			}
			if o, err := exec.Command(nativeBin, append(nativeArgs, src)...).CombinedOutput(); err != nil {
				t.Fatalf("native compile for %s failed: %v\n%s", target, err, o)
			}
			if o, err := exec.Command(driverBin, append(shArgs, src, stdlib)...).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile for %s failed: %v\n%s", target, err, o)
			}
			// Only the host target is executed here. arm64 needs qemu and wasm
			// needs wasmtime with a preopen covering absolute host paths; both
			// have their own lanes, and what this row adds over them is that
			// the two compilers ACCEPT the same program for the same target —
			// which is where a missing lowering shows up, since the emitters
			// are the half that differs.
			if target != "x86-64-linux" {
				return
			}
			for _, r := range []struct {
				label string
				bin   string
			}{{"native", filepath.Join(out, "n")}, {"self-host", filepath.Join(out, "s")}} {
				cmd := exec.Command(r.bin)
				o, _ := cmd.CombinedOutput()
				if code := cmd.ProcessState.ExitCode(); code != 0 {
					t.Errorf("%s-compiled program exited %d, want 0 — that code names the disagreeing pair in lstatDifferentialSource\n%s", r.label, code, o)
				}
			}
		})
	}
}

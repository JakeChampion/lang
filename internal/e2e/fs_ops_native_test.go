// Native filesystem-op family coverage (issue #5372, part 2).
//
// #5380 lowered `remove_dir_all` on x86-64 so std/test TAP files
// link, but the SIBLING fs ops had the identical gap one call
// away: `stat` / `temp_dir` / `read_dir` / `remove_file` had no
// native lowering on x86-64 (any TAP file touching them failed
// with `undefined label "stat"` etc.), and the arm64 backend had
// NONE of the family — even `remove_dir_all`, so on the default
// target every examples/tests/*.fern failed at link through
// TestRunner.finish()'s cleanup loop. The fix emits the whole
// family as runtime helpers on both native backends
// (emitRemoveFileRuntime / emitTempDirRuntime /
// emitReadDirRuntime / emitStatRuntime on each, plus arm64's
// emitRemoveDirAllRuntime), sharing the existing
// __fern_io_error / alloc-box conventions with read_file /
// write_file.
//
// The interpreter is the oracle for the same shapes
// (TestRunnerFilesystemOpsExample drives the TAP file through
// -interp); here we pin that the NATIVE binaries link and
// produce the right filesystem effects on both backends.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fsOpsRoundTripSrc exercises the whole family end-to-end
// against absolute /tmp paths: temp_dir → write_file → stat
// (file kind + size, dir kind) → read_dir → remove_file (both
// the unlink and the missing-target-is-Some contract) →
// remove_dir_all → stat sees ENOENT. Every failure arm returns a
// distinct exit code so a regression names its op.
const fsOpsRoundTripSrc = `function main(): i32 {
    match (temp_dir("fsops-native")) {
        Ok(dir) => {
            match (write_file(dir + "/probe.txt", "hello")) { Err(e) => { return 10; }, Ok(_) => { } }
            match (stat(dir + "/probe.txt")) {
                Ok(fs) => {
                    if (fs.is_file) {
                        if (fs.size == 5) { } else { return 12; }
                    } else { return 11; }
                },
                Err(e) => { return 13; }
            }
            match (stat(dir)) {
                Ok(fs) => {
                    if (fs.is_dir) { } else { return 21; }
                },
                Err(e) => { return 22; }
            }
            match (read_dir(dir)) {
                Ok(names) => { if (names.len() == 1) { } else { return 14; } },
                Err(e) => { return 15; }
            }
            match (remove_file(dir + "/probe.txt")) { Err(e) => { return 16; }, Ok(_) => { } }
            match (remove_file(dir + "/probe.txt")) { Err(e) => { }, Ok(_) => { return 23; } }
            match (remove_dir_all(dir)) { Err(e) => { return 17; }, Ok(_) => { } }
            match (stat(dir)) {
                Ok(fs) => { return 18; },
                Err(e) => { return 0; }
            }
        },
        Err(e) => { return 19; }
    }
    return 20;
}`

func TestX86_64FsOpsRoundTrip(t *testing.T) {
	code, _ := compileRunX86_64WithSetup(t, fsOpsRoundTripSrc, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (see fsOpsRoundTripSrc arm codes)", code)
	}
}

// read_dir on a host-seeded directory returns exactly the seeded
// entries (files AND subdirectories, "." / ".." skipped) — the
// entry count comes back as the exit code.
func TestX86_64ReadDirListsSeededEntries(t *testing.T) {
	src := `function main(): i32 {
    match (read_dir("d")) {
        Ok(names) => { return names.len(); },
        Err(e) => { return 99; }
    }
    return 98;
}`
	code, _ := compileRunX86_64WithSetup(t, src, func(dir string) {
		if err := os.MkdirAll(filepath.Join(dir, "d", "sub"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
			if err := os.WriteFile(filepath.Join(dir, "d", f), []byte("x"), 0o644); err != nil {
				t.Fatalf("seed %s: %v", f, err)
			}
		}
	})
	if code != 4 {
		t.Errorf("exit = %d, want 4 (3 files + 1 subdir)", code)
	}
}

// stat on a missing path is Err (the NotFound IoError arm), not
// a crash or a zeroed Ok.
func TestX86_64StatMissingIsErr(t *testing.T) {
	src := `function main(): i32 {
    match (stat("no_such_path")) {
        Ok(fs) => { return 1; },
        Err(e) => { return 0; }
    }
    return 2;
}`
	code, _ := compileRunX86_64WithSetup(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (Err on a missing path)", code)
	}
}

// The class-level TAP pin: filesystem_ops_test.fern — the
// examples/tests suite that exercises temp_dir / read_dir /
// remove_file / remove_dir_all — must compile through the full
// CLI pipeline (modload + the in-process assembler) with
// -target x86-64-linux and pass. Before the fix this failed at link
// with `undefined label "temp_dir"`. The -interp leg lives in
// TestRunnerFilesystemOpsExample and stays authoritative for
// the TAP text contract.
func TestX86_64FilesystemOpsTapLinksNatively(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildFernCLI(t)
	out := filepath.Join(t.TempDir(), "fsops_tap")
	if o, err := exec.Command(fern, "-target", "x86-64-linux", "-o", out,
		"../../examples/tests/filesystem_ops_test.fern").CombinedOutput(); err != nil {
		t.Fatalf("native compile of filesystem_ops_test.fern failed: %v\n%s", err, o)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(out)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], out)...)
	}
	tap, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("TAP binary exit = %d, want 0\n%s", code, tap)
	}
	if s := string(tap); !strings.Contains(s, "# fail 0") {
		t.Errorf("TAP output missing \"# fail 0\":\n%s", s)
	}
}

// ---- arm64 ----

func TestArm64FsOpsRoundTrip(t *testing.T) {
	out, code := compileAndRunArm64(t, fsOpsRoundTripSrc)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (see fsOpsRoundTripSrc arm codes)\n%s", code, out)
	}
}

// arm64 remove_dir_all on a host-seeded nested tree: files at
// every level drive the ENOTDIR-unlink recursion, emptied
// directories rmdir on the way back up (mirrors
// TestX86_64RemoveDirAllNestedTree).
func TestArm64RemoveDirAllNestedTree(t *testing.T) {
	src := `function main(): i32 {
    match (remove_dir_all("tree")) {
        Err(e) => { return 40; },
        Ok(_) => { return 0; }
    }
    return 41;
}`
	binPath, qemu := compileArm64Bin(t, src)
	dir := t.TempDir()
	mkTree(t, dir)
	cmd := runArm64Bin(qemu, binPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (None — tree removed)\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tree")); !os.IsNotExist(err) {
		t.Errorf("tree still exists after remove_dir_all (stat err = %v)", err)
	}
}

// The arm64 headline pin for #5372: an unmodified examples/tests
// TAP file must compile on the DEFAULT target through the full
// CLI pipeline (modload + the in-process arm64 assembler) and
// pass. std/test's TestRunner.finish() references
// remove_dir_all unconditionally via its temp-dir cleanup loop,
// so before the fix EVERY TAP file failed here with
// `branch to undefined label "remove_dir_all"`.
func TestArm64ArithmeticTapLinksNatively(t *testing.T) {
	fern := buildFernCLI(t)
	qemu := arm64QemuOrEmpty(t)
	out := filepath.Join(t.TempDir(), "arith_tap_arm64")
	if o, err := exec.Command(fern, "-target", "arm64-linux", "-o", out,
		"../../examples/tests/arithmetic_test.fern").CombinedOutput(); err != nil {
		t.Fatalf("native arm64 compile of arithmetic_test.fern failed: %v\n%s", err, o)
	}
	cmd := runArm64Bin(qemu, out)
	tap, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("TAP binary exit = %d, want 0\n%s", code, tap)
	}
	if s := string(tap); !strings.Contains(s, "# fail 0") {
		t.Errorf("TAP output missing \"# fail 0\":\n%s", s)
	}
}

// ---- create_dir_all (#6749) ----

// createDirAllSrc pins the whole `mkdir -p` contract in one
// program, relative to the process working directory: a missing
// chain is created top to bottom, the result is a DIRECTORY, a
// second call over the same path is Ok (EEXIST folded into
// success), doubled separators and a trailing slash name the same
// directory rather than an empty component, and a component that
// is a regular file is a genuine Err. Each failure arm returns a
// distinct exit code.
const createDirAllSrc = `function main(): i32 {
    match (create_dir_all("a/b/c")) { Err(e) => { return 10; }, Ok(_) => { } }
    match (stat("a/b/c")) {
        Ok(fs) => { if (fs.is_dir) { } else { return 11; } },
        Err(e) => { return 12; }
    }
    match (stat("a")) {
        Ok(fs) => { if (fs.is_dir) { } else { return 13; } },
        Err(e) => { return 14; }
    }
    match (create_dir_all("a/b/c")) { Err(e) => { return 15; }, Ok(_) => { } }
    match (create_dir_all("x//y/")) { Err(e) => { return 16; }, Ok(_) => { } }
    match (stat("x/y")) {
        Ok(fs) => { if (fs.is_dir) { } else { return 17; } },
        Err(e) => { return 18; }
    }
    match (write_file("f.txt", "hi")) { Err(e) => { return 19; }, Ok(_) => { } }
    match (create_dir_all("f.txt/inner")) { Ok(_) => { return 20; }, Err(e) => { } }
    return 0;
}`

func TestX86_64CreateDirAll(t *testing.T) {
	code, _ := compileRunX86_64WithSetup(t, createDirAllSrc, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (see createDirAllSrc arm codes)", code)
	}
}

func TestArm64CreateDirAll(t *testing.T) {
	out, code := compileAndRunArm64(t, createDirAllSrc)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (see createDirAllSrc arm codes)\n%s", code, out)
	}
}

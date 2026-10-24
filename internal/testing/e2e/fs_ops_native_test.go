package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestX86_64FilesystemOpsTapLinksNatively(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildFernCLI(t)
	out := filepath.Join(t.TempDir(), "fsops_tap")
	if o, err := exec.Command(fern, "-target", "x86-64-linux", "-o", out,
		"../../../tests/stdlib/filesystem_ops_test.fern").CombinedOutput(); err != nil {
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

func TestArm64FsOpsRoundTrip(t *testing.T) {
	out, code := compileAndRunArm64(t, fsOpsRoundTripSrc)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (see fsOpsRoundTripSrc arm codes)\n%s", code, out)
	}
}

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

func TestArm64ArithmeticTapLinksNatively(t *testing.T) {
	fern := buildFernCLI(t)
	qemu := arm64QemuOrEmpty(t)
	out := filepath.Join(t.TempDir(), "arith_tap_arm64")
	if o, err := exec.Command(fern, "-target", "arm64-linux", "-o", out,
		"../../../tests/stdlib/arithmetic_test.fern").CombinedOutput(); err != nil {
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

func tempDirPrefixIsNameSrc(t *testing.T) string {
	t.Helper()
	parent, err := os.MkdirTemp("", "fern-esc")
	if err != nil {
		t.Fatalf("mkdir escape parent: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })
	return fmt.Sprintf(`function main(): i32 {
    let d: string = "";
    match (temp_dir("plain")) { Err(e) => { return 1; }, Ok(p) => { d = p; } }
    match (remove_dir_all(d)) { Err(e) => { return 2; }, Ok(_) => { } }
    match (temp_dir("%s/inner")) { Ok(p) => { return 3; }, Err(e) => { } }
    match (read_dir("%s")) {
        Err(e) => { return 4; },
        Ok(names) => { if (names.len() != 0) { return 5; } }
    }
    return 0;
}`, filepath.Base(parent), parent)
}

func TestX86_64TempDirPrefixIsName(t *testing.T) {
	code, _ := compileRunX86_64WithSetup(t, tempDirPrefixIsNameSrc(t), nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (1/2 setup, 3 escape accepted, 4/5 escape landed)", code)
	}
}

func TestArm64TempDirPrefixIsName(t *testing.T) {
	out, code := compileAndRunArm64(t, tempDirPrefixIsNameSrc(t))
	if code != 0 {
		t.Errorf("exit = %d, want 0 (1/2 setup, 3 escape accepted, 4/5 escape landed)\n%s", code, out)
	}
}

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

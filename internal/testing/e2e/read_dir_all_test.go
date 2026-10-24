package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func readDirAllSource(dots bool) string {
	const src = `function has(names: string[], want: string): boolean {
    let i: i32 = 0;
    while (i < names.len()) {
        if (names[i] == want) { return true; }
        i = i + 1;
    }
    return false;
}

function main(): i32 {
    match (create_dir_all("d")) { Err(_) => { return 1; }, Ok(_) => {} }
    match (write_file("d/a.txt", "x")) { Err(_) => { return 2; }, Ok(_) => {} }
    match (write_file("d/b.txt", "y")) { Err(_) => { return 3; }, Ok(_) => {} }
    let all: string[] = [];
    match (read_dir_all("d")) { Ok(ns) => { all = ns; }, Err(_) => { return 4; } }
    let plain: string[] = [];
    match (read_dir("d")) { Ok(ns) => { plain = ns; }, Err(_) => { return 5; } }
    if (plain.len() != 2) { return 6; }
    if (!has(plain, "a.txt")) { return 7; }
    if (!has(plain, "b.txt")) { return 8; }
    if (has(plain, ".")) { return 9; }
    if (has(plain, "..")) { return 10; }
    if (!has(all, "a.txt")) { return 11; }
    if (!has(all, "b.txt")) { return 12; }
__DOTS__
    // Drop the dot entries from read_dir_all's listing and what is left
    // must be read_dir's, IN ORDER: both drain the same directory, so the
    // surviving names line up one for one.
    let k: i32 = 0;
    let j: i32 = 0;
    while (k < all.len()) {
        if (all[k] != "." && all[k] != "..") {
            if (j >= plain.len()) { return 19; }
            if (all[k] != plain[j]) { return 19; }
            j = j + 1;
        }
        k = k + 1;
    }
    if (j != plain.len()) { return 19; }
    match (read_dir_all("no_such_dir")) { Ok(_) => { return 20; }, Err(_) => {} }
    return 0;
}
`
	arm := `    if (all.len() != 2) { return 16; }
    if (has(all, ".")) { return 17; }
    if (has(all, "..")) { return 18; }`
	if dots {
		arm = `    if (all.len() != 4) { return 13; }
    if (!has(all, ".")) { return 14; }
    if (!has(all, "..")) { return 15; }`
	}
	return strings.Replace(src, "__DOTS__", arm, 1)
}

func inDir(t *testing.T, cmd *exec.Cmd) *exec.Cmd {
	t.Helper()
	cmd.Dir = t.TempDir()
	return cmd
}

func TestX86_64ReadDirAll(t *testing.T) {
	bin, runner := compileX86_64Bin(t, readDirAllSource(true))
	out, code := runWithPipes(t, inDir(t, runX86_64Bin(runner, bin)))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see readDirAllSource)\n%s", code, out)
	}
}

func TestArm64ReadDirAll(t *testing.T) {
	bin, qemu := compileArm64Bin(t, readDirAllSource(true))
	out, code := runWithPipes(t, inDir(t, runArm64Bin(qemu, bin)))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see readDirAllSource)\n%s", code, out)
	}
}

func TestInterpReadDirAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(readDirAllSource(true)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildLangBinForInterp(t), "-interp", p)
	cmd.Dir = dir
	out, code := runWithPipes(t, cmd)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see readDirAllSource)\n%s", code, out)
	}
}

func TestWASMPreview1ReadDirAll(t *testing.T) {
	mod := buildWasmCore(t, readDirAllSource(true))
	if code := runPreview1Module(t, mod, t.TempDir()); code != 0 {
		t.Errorf("preview-1 read_dir_all: main = %d, want 0 (see readDirAllSource)", code)
	}
}

func TestWASMReadDirAll(t *testing.T) {
	stdout, stderr, ec := runCLIComponent(t, readDirAllSource(false), runOpts{workDir: t.TempDir()})
	if ec != 0 {
		t.Errorf("wasmtime exit %d, want 0 — the preview-1 command module's main names the case (see readDirAllSource)\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
}

const readDirAllWithRemoveSource = `function main(): i32 {
    match (create_dir_all("d/sub")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (write_file("d/a.txt", "x")) { Ok(_) => {}, Err(_) => { return 2; } }
    let all: string[] = [];
    match (read_dir_all("d")) { Ok(es) => { all = es; }, Err(_) => { return 3; } }
    // a.txt, sub, "." and "..".
    if (all.len() != 4) { return 4; }
    match (remove_dir_all("d")) { Ok(_) => {}, Err(_) => { return 5; } }
    match (read_dir_all("d")) { Ok(_) => { return 6; }, Err(_) => {} }
    return 0;
}
`

func TestX86_64ReadDirAllBesideRemoveDirAll(t *testing.T) {
	bin, runner := compileX86_64Bin(t, readDirAllWithRemoveSource)
	out, code := runWithPipes(t, inDir(t, runX86_64Bin(runner, bin)))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see readDirAllWithRemoveSource)\n%s", code, out)
	}
}

func TestArm64ReadDirAllBesideRemoveDirAll(t *testing.T) {
	bin, qemu := compileArm64Bin(t, readDirAllWithRemoveSource)
	out, code := runWithPipes(t, inDir(t, runArm64Bin(qemu, bin)))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see readDirAllWithRemoveSource)\n%s", code, out)
	}
}

func TestInterpReadDirAllBesideRemoveDirAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(readDirAllWithRemoveSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildLangBinForInterp(t), "-interp", p)
	cmd.Dir = dir
	out, code := runWithPipes(t, cmd)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see readDirAllWithRemoveSource)\n%s", code, out)
	}
}

func TestWASMPreview1ReadDirAllBesideRemoveDirAll(t *testing.T) {
	mod := buildWasmCore(t, readDirAllWithRemoveSource)
	if code := runPreview1Module(t, mod, t.TempDir()); code != 0 {
		t.Errorf("preview-1 read_dir_all beside remove_dir_all: main = %d, want 0", code)
	}
}

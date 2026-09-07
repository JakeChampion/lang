// The single-step directory and link primitives (#8883) end to end on every
// backend that provides them: create_dir / remove_dir / create_link /
// create_symlink / read_link, plus `umask` on the natives.
//
// What these assert that no unit test can: each backend builds the syscall
// arguments by hand — the two arm64 emitters and x86-64 from hand-written
// assembly, wasmbin from two WASI previews, the self-host from generated Fern
// — and every one of them is a place to pass AT_REMOVEDIR where AT_FDCWD
// belongs, or to swap `target` and `path`. Both mistakes produce a plausible
// syscall that fails, or worse succeeds against the wrong name, so the probe
// checks the RESULTING TREE and not only the return value.
//
// `umask` is native-only: WASI has no file-mode creation mask, so E066 refuses
// it there (capability `fsmode`) and the wasm probe is the same program with
// that block removed.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// dirLinkSource is the probe, parameterised by the directory its relative
// paths resolve against — "" for the backends that run with their cwd already
// there (wasm under its preopen), an absolute prefix for the rest.
//
// Every failure returns its own exit code, so the number names the step.
func dirLinkSource(prefix string, withUmask bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	src := fmt.Sprintf(`function main(): i32 {
    // create_dir makes ONE directory and reports EEXIST rather than folding
    // it away, which is the whole difference from create_dir_all.
    match (create_dir(%[1]q, 493)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (create_dir(%[1]q, 493)) { Ok(_) => { return 2; }, Err(_) => {} }
    // A missing parent is ENOENT, not a chain of created directories.
    match (create_dir(%[2]q, 493)) { Ok(_) => { return 3; }, Err(_) => {} }

    match (write_file(%[3]q, "hello\n")) { Ok(_) => {}, Err(_) => { return 4; } }

    // A hard link: the new name reaches the same inode, so reading it back
    // sees the content written through the old one.
    match (create_link(%[3]q, %[4]q)) { Ok(_) => {}, Err(_) => { return 5; } }
    match (read_file(%[4]q)) {
        Ok(s) => { if (s != "hello\n") { return 6; } },
        Err(_) => { return 7; }
    }
    // An existing name is EEXIST, and a missing target ENOENT.
    match (create_link(%[3]q, %[4]q)) { Ok(_) => { return 8; }, Err(_) => {} }
    match (create_link(%[5]q, %[6]q)) { Ok(_) => { return 9; }, Err(_) => {} }

    // A symlink stores its target verbatim and never resolves it, so a
    // dangling one is created without complaint and reads back as written.
    match (create_symlink("dangling-target", %[7]q)) { Ok(_) => {}, Err(_) => { return 10; } }
    match (read_link(%[7]q)) {
        Ok(target) => { if (target != "dangling-target") { return 11; } },
        Err(_) => { return 12; }
    }
    // read_link asks about the PATH, not what it points at: a regular file
    // is EINVAL rather than an empty answer.
    match (read_link(%[3]q)) { Ok(_) => { return 13; }, Err(_) => {} }
    match (read_link(%[5]q)) { Ok(_) => { return 14; }, Err(_) => {} }

    // remove_dir is rmdir(2): a directory with something in it is
    // ENOTEMPTY, and only the emptied one goes.
    match (write_file(%[8]q, "x")) { Ok(_) => {}, Err(_) => { return 15; } }
    match (remove_dir(%[1]q)) { Ok(_) => { return 16; }, Err(_) => {} }
    match (remove_file(%[8]q)) { Ok(_) => {}, Err(_) => { return 17; } }
    match (remove_dir(%[1]q)) { Ok(_) => {}, Err(_) => { return 18; } }
    match (remove_dir(%[1]q)) { Ok(_) => { return 19; }, Err(_) => {} }
    // A regular file is not a directory.
    match (remove_dir(%[3]q)) { Ok(_) => { return 20; }, Err(_) => {} }
`, p("d"), p("d/nope/deep"), p("f.txt"), p("hard.txt"), p("missing.txt"), p("from-missing.txt"), p("link"), p("d/inner.txt"))
	if withUmask {
		// umask(2) sets and reads in one step, so reading it is
		// umask(umask(0)) — and the mask it reports is the one that was
		// installed a moment earlier, not whatever the shell had.
		src += `    var prev: i32 = umask(18);
    if (umask(prev) != 18) { return 21; }
`
	}
	src += `    return 0;
}
`
	return src
}

// dirLinkCheckTree asserts the filesystem the probe left behind, which is the
// half a return value cannot prove: the hard link is gone with its original,
// the dangling symlink is still a symlink pointing where it was told, and the
// directory the probe emptied is not there.
func dirLinkCheckTree(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, "d")); !os.IsNotExist(err) {
		t.Errorf("d/ still exists after remove_dir (lstat err = %v)", err)
	}
	fi, err := os.Lstat(filepath.Join(dir, "link"))
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link is %v, want a symlink", fi.Mode())
	}
	target, err := os.Readlink(filepath.Join(dir, "link"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "dangling-target" {
		t.Errorf("link -> %q, want %q", target, "dangling-target")
	}
	// The hard link and its original share an inode, so the second name must
	// still read the content the first was written with.
	got, err := os.ReadFile(filepath.Join(dir, "hard.txt"))
	if err != nil {
		t.Fatalf("read hard.txt: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("hard.txt = %q, want %q", got, "hello\n")
	}
	if _, err := os.Lstat(filepath.Join(dir, "from-missing.txt")); !os.IsNotExist(err) {
		t.Errorf("from-missing.txt exists — a link to a missing target was created")
	}
}

// getcwdMarkerSource asserts that `getcwd()` names the directory the process
// is actually in, without the program being told which one that is: the
// answer has to be absolute, and reading MARKER through it has to find the
// file the harness seeded beside the binary.
//
// Comparing against a literal path would work only where the test can choose
// the child's directory, which is not every runner here. This asserts the
// same thing from inside: a backend that answered `/`, the empty string, or
// a path with the terminating NUL still on the end — the shape the two
// platforms' differing RETURN conventions invite, since Linux counts that
// NUL and XNU does not — fails to open the marker.
const getcwdMarkerSource = `function main(): i32 {
    match (getcwd()) {
        Ok(d) => {
            if (d.len() == 0) { return 1; }
            if (d[0] as i32 != 47) { return 2; }
            if (d.len() > 1 && d[d.len() - 1] as i32 == 47) { return 3; }
            match (read_file(d + "/cwd-marker.txt")) {
                Ok(text) => { if (text != "here\n") { return 4; } },
                Err(_) => { return 5; }
            }
            return 0;
        },
        Err(_) => { return 6; }
    }
    return 7;
}
`

// seedCwdMarker writes the file getcwdMarkerSource reads back through the
// path getcwd answered.
func seedCwdMarker(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "cwd-marker.txt"), []byte("here\n"), 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
}

func TestX86_64Getcwd(t *testing.T) {
	code, _ := compileRunX86_64WithSetup(t, getcwdMarkerSource, func(dir string) { seedCwdMarker(t, dir) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — 1 empty, 2 not absolute, 3 a trailing slash, 5 the marker, 6 the Err arm", code)
	}
}

func TestArm64SSAGetcwd(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	seedCwdMarker(t, dir)
	bin := compileArm64SSA(t, fern, getcwdMarkerSource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — 1 empty, 2 not absolute, 3 a trailing slash, 5 the marker, 6 the Err arm\n%s", code, stderr)
	}
}

// The default arm64 emitter is the one whose helper has to BRANCH on the
// platform — Linux answers the byte count including the terminating NUL,
// Darwin's __getcwd answers 0 and leaves the buffer to be measured — so the
// marker read is the assertion that the length came out right.
func TestArm64Getcwd(t *testing.T) {
	runner, ok := arm64Runner()
	if !ok {
		t.Fatal("no way to run an arm64 binary: install qemu-aarch64")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	seedCwdMarker(t, dir)
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(getcwdMarkerSource), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := exec.Command(fern, "-target", "arm64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if runner == "" {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner, bin)
	}
	cmd.Dir = dir
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 — 1 empty, 2 not absolute, 3 a trailing slash, 5 the marker, 6 the Err arm", code)
	}
}

// The interpreter answers this from syscall.Getwd rather than os.Getwd: the
// second prefers $PWD when it names the same directory, which getcwd(2) does
// not, so a stale or symlinked PWD would make the two disagree. It runs in
// the test process's own directory, so the assertion is against that.
func TestInterpGetcwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	src := fmt.Sprintf(`function main(): i32 {
    match (getcwd()) {
        Ok(d) => {
            if (d != %[1]q) { return 1; }
            if (d.len() != %[2]d) { return 2; }
            return 0;
        },
        Err(_) => { return 3; }
    }
    return 4;
}
`, cwd, len(cwd))
	if code := runInterpExit(t, src); code != 0 {
		t.Fatalf("exit = %d, want 0 — 1 the path, 2 its length, 3 the Err arm", code)
	}
}

func TestX86_64DirLinkPrimitives(t *testing.T) {
	dir := t.TempDir()
	code, _ := compileRunX86_64WithSetup(t, dirLinkSource(dir, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirLinkSource)", code)
	}
	dirLinkCheckTree(t, dir)
}

func TestArm64DirLinkPrimitives(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, dirLinkSource(dir, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirLinkSource)\n%s", code, out)
	}
	dirLinkCheckTree(t, dir)
}

// The arm64 SSA-direct backend is a fourth hand-written implementation of the
// same five syscalls, with its own frame discipline and its own inline heap
// bump, so it gets the same probe rather than being taken on trust.
func TestArm64SSADirLinkPrimitives(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, dirLinkSource(dir, true), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirLinkSource)\n%s", code, stderr)
	}
	dirLinkCheckTree(t, dir)
}

// The interpreter answers these from Go's syscall package, so it is a fifth
// implementation and the one an in-language test suite runs under.
func TestInterpDirLinkPrimitives(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, dirLinkSource(dir, true)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dirLinkSource)", code)
	}
	dirLinkCheckTree(t, dir)
}

// The wasm leg runs under the component's preopen, so its paths are relative
// and `umask` is absent: WASI has no file-mode creation mask and E066 refuses
// the builtin on that target, which is the honest answer rather than a mask
// that describes nothing.
//
// main's return reaches us on STDOUT, not as the exit status: the harness
// builds with PrintMainResult.
func TestWASMDirLinkPrimitives(t *testing.T) {
	p := buildComponent(t, dirLinkSource("", false))
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dirLinkSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
	dirLinkCheckTree(t, dir)
}

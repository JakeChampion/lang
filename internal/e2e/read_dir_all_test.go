package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// read_dir_all is read_dir without the `.` / `..` filter (#9279): every
// name the directory holds, in the order its reader reports them. `ls -f`
// is what wanted it — an unsorted listing prints the dot entries where the
// directory keeps them, and a pair synthesized at the front is a different
// listing.
//
// The program seeds its own directory rather than taking a host-seeded one,
// so the same source runs on every leg including the two wasm ones, whose
// harnesses preopen a directory but do not create trees inside it. Every
// failure returns its own exit code.
//
// What is asserted here is the RELATIONSHIP between the two builtins on one
// backend: read_dir_all holds everything read_dir holds, in the same order,
// plus the dot entries where the reader put them. That the resulting order
// is the one GNU prints is pinned where it matters — the `ls -f` cases in
// internal/coreutils compare it against the GNU binary byte for byte.
//
// `dots` says whether the host reports `.` and `..` at all. Every kernel
// and preview 1 do; wasi-filesystem's read-directory omits them, so a
// preview-2 host has none to report and read_dir_all's answer is read_dir's.
// That case is asserted rather than skipped: the equivalence is the
// documented behaviour there, and a host that started reporting them would
// show up here.
func readDirAllSource(dots bool) string {
	const src = `function has(names: string[], want: string): boolean {
    var i: i32 = 0;
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
    var all: string[] = [];
    match (read_dir_all("d")) { Ok(ns) => { all = ns; }, Err(_) => { return 4; } }
    var plain: string[] = [];
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
    var k: i32 = 0;
    var j: i32 = 0;
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

// inDir gives a command its own empty directory to work in, so the
// program's relative `d` lands in a temp tree rather than the test's own.
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

// The interpreter reads the same getdents drain both builtins do, so it
// reports the same order — which is why its read_dir no longer wraps
// `os.ReadDir`, whose sort no backend reproduces.
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

// Preview 1's fd_readdir yields `.` and `..`, so the dot entries are there
// to be kept.
func TestWASMPreview1ReadDirAll(t *testing.T) {
	mod := buildPreview1Module(t, readDirAllSource(true))
	if code := runPreview1Module(t, mod, t.TempDir()); code != 0 {
		t.Errorf("preview-1 read_dir_all: main = %d, want 0 (see readDirAllSource)", code)
	}
}

func TestWASMReadDirAll(t *testing.T) {
	stdout, stderr, ec, _ := runWasmInDirOpts(t, readDirAllSource(false), nil, runOpts{stdin: ""})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Errorf("main = %d, want 0 (see readDirAllSource)\nstdout:\n%s\nstderr:\n%s", got, stdout, stderr)
	}
}

// readDirAllWithRemoveSource calls read_dir_all AND remove_dir_all in one
// program, which is the pair that catches a LABEL COLLISION between their
// two runtime helpers.
//
// Both bodies land in one object, and the in-process assemblers bound a
// duplicate `.L` name to whichever body was emitted second — silently, so
// every branch in the first one jumped into the second. read_dir_all
// shipped with `.Lrda` / `.Lrda2w` / `.Lssa_rda`, the prefixes
// remove_dir_all already owned, and this exact program segfaulted on all
// three native backends while every single-builtin test stayed green.
// Reported by pullfrog on #9290.
//
// The assemblers refuse a duplicate named .text label now
// (internal/native/*/), so the collision is a build error rather than a
// wrong image; this runs the pair anyway, because the refusal is a
// backstop and the answer being right is the property.
const readDirAllWithRemoveSource = `function main(): i32 {
    match (create_dir_all("d/sub")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (write_file("d/a.txt", "x")) { Ok(_) => {}, Err(_) => { return 2; } }
    var all: string[] = [];
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
	mod := buildPreview1Module(t, readDirAllWithRemoveSource)
	if code := runPreview1Module(t, mod, t.TempDir()); code != 0 {
		t.Errorf("preview-1 read_dir_all beside remove_dir_all: main = %d, want 0", code)
	}
}

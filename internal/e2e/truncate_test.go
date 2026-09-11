// `truncate` end to end on every backend that provides it — all of them.
//
// What no unit test can assert: five separate hand-written implementations
// build this call. x86-64, arm64-linux and arm64-ssa each issue truncate(2)
// from hand-written assembly; the interpreter goes through Go's syscall
// package; and wasmbin has two bodies over WASI, neither of which has a
// path-based set-size to call — both open a descriptor without CREATE or
// TRUNCATE, set the size on it, and drop it. Each of those is a place to
// truncate the LENGTH to 32 bits, to pass the operands in the wrong order, or
// to let the open create or empty the file before the size is set. Every one
// of those produces a call that succeeds against the wrong thing, so the
// probe reads the size and the bytes BACK rather than trusting the return
// value, and the Go side checks the tree the program left behind through
// os.Stat.
//
// The 4 GiB + 1 step is the one that justifies reading the size back: a length
// truncated to 32 bits is 1 there, and a helper that did that would report
// success either way.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// truncateBigLen is 2^32 + 1: past the 32-bit boundary, and with a low half of
// 1 so a truncated length is a plausible size rather than an obvious zero.
const truncateBigLen = 4294967297

// truncateSource is the probe, parameterised by the directory its relative
// paths resolve against — "" for the backends that run with their cwd already
// there (wasm under its preopen), an absolute prefix for the rest.
//
// Every failure returns its own exit code, so the number names the step.
func truncateSource(prefix string) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	return fmt.Sprintf(`function main(): i32 {
    // SHRINK: the bytes past the new length are gone and the ones before it
    // are untouched.
    match (write_file(%[1]q, "hello world\n")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (truncate(%[1]q, 5)) { Ok(_) => {}, Err(_) => { return 2; } }
    match (stat(%[1]q)) { Ok(st) => { if (st.size != 5) { return 3; } }, Err(_) => { return 4; } }
    match (read_file(%[1]q)) { Ok(s) => { if (s != "hello") { return 5; } }, Err(_) => { return 6; } }

    // GROW: the file extends with a hole that reads as zeros, and the bytes
    // already there keep their values. This is the direction an fd-based
    // builtin could not express, because every Fern open of an existing file
    // for writing empties it first.
    match (truncate(%[1]q, 20)) { Ok(_) => {}, Err(_) => { return 7; } }
    match (stat(%[1]q)) { Ok(st) => { if (st.size != 20) { return 8; } }, Err(_) => { return 9; } }
    match (read_file_bytes(%[1]q)) {
        Ok(b) => {
            if (b.len() != 20) { return 10; }
            if (b[0] != 104) { return 11; }
            if (b[4] != 111) { return 12; }
            var i: i32 = 5;
            while (i < 20) {
                if (b[i] != 0) { return 13; }
                i = i + 1;
            }
        },
        Err(_) => { return 14; }
    }

    // Shrink again, so the tree the Go side reads back is the short form.
    match (truncate(%[1]q, 5)) { Ok(_) => {}, Err(_) => { return 15; } }

    // It does NOT create. A missing path is an error and stays missing,
    // which is what "truncate -c" needs verbatim.
    match (truncate(%[2]q, 3)) { Ok(_) => { return 16; }, Err(_) => {} }
    match (stat(%[2]q)) { Ok(_) => { return 17; }, Err(_) => {} }

    // A negative length is the kernel's error, not a clamp to zero: a clamp
    // would resize the file to something the caller never named.
    match (truncate(%[1]q, 0 - 1)) { Ok(_) => { return 18; }, Err(_) => {} }
    match (stat(%[1]q)) { Ok(st) => { if (st.size != 5) { return 19; } }, Err(_) => { return 20; } }

    // The length is 64 bits wide. Truncated to 32 it would be 1 here, and the
    // call would report success on a file a thousandth the size asked for.
    match (write_file(%[3]q, "x")) { Ok(_) => {}, Err(_) => { return 21; } }
    match (truncate(%[3]q, %[4]d)) { Ok(_) => {}, Err(_) => { return 22; } }
    match (stat(%[3]q)) { Ok(st) => { if (st.size != %[4]d) { return 23; } }, Err(_) => { return 24; } }
    return 0;
}
`, p("a.txt"), p("missing.txt"), p("big.txt"), truncateBigLen)
}

// truncateCheckTree asserts the filesystem the probe left behind through Go's
// own stat rather than Fern's, so a broken reader cannot make the probe
// self-consistent: a `stat` that answered whatever `truncate` was last handed
// would pass every check inside the program and fail here.
func truncateCheckTree(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("stat a.txt: %v", err)
	}
	if fi.Size() != 5 {
		t.Errorf("a.txt size = %d, want 5", fi.Size())
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("a.txt = %q, want %q — the shrink kept the wrong bytes", got, "hello")
	}
	big, err := os.Stat(filepath.Join(dir, "big.txt"))
	if err != nil {
		t.Fatalf("stat big.txt: %v", err)
	}
	if big.Size() != truncateBigLen {
		t.Errorf("big.txt size = %d, want %d — the length was truncated on its way to the kernel",
			big.Size(), truncateBigLen)
	}
	if _, err := os.Lstat(filepath.Join(dir, "missing.txt")); !os.IsNotExist(err) {
		t.Errorf("missing.txt exists after a truncate of it (lstat err = %v) — truncate created a file", err)
	}
}

func TestX86_64Truncate(t *testing.T) {
	dir := t.TempDir()
	code, out := compileRunX86_64WithSetup(t, truncateSource(dir), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see truncateSource)\n%s", code, out)
	}
	truncateCheckTree(t, dir)
}

func TestArm64Truncate(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, truncateSource(dir))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see truncateSource)\n%s", code, out)
	}
	truncateCheckTree(t, dir)
}

// The arm64 SSA-direct backend is a third hand-written implementation of the
// same syscall, with its own frame discipline and its own second-scalar
// register, so it gets the probe rather than being taken on trust.
func TestArm64SSATruncate(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, truncateSource(dir), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see truncateSource)\n%s", code, stderr)
	}
	truncateCheckTree(t, dir)
}

func TestInterpTruncate(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, truncateSource(dir)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see truncateSource)", code)
	}
	truncateCheckTree(t, dir)
}

// Preview 1 answers this with path_open + fd_filestat_set_size + fd_close over
// an errno return, where the component leg below goes through
// get-directories, open-at, descriptor.set-size and a resource drop. Two
// separate hand-written bodies, so two runs.
func TestWASMPreview1Truncate(t *testing.T) {
	mod := buildPreview1Module(t, truncateSource(""))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see truncateSource)", got)
	}
	truncateCheckTree(t, dir)
}

// main's return reaches us on STDOUT, not as the exit status: the harness
// builds with PrintMainResult.
func TestWASMTruncate(t *testing.T) {
	p := buildComponent(t, truncateSource(""))
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see truncateSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
	truncateCheckTree(t, dir)
}

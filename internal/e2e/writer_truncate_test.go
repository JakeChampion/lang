package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// `Writer.truncate(len)` is ftruncate(2) on a handle, and it is six
// hand-written implementations: x86-64 and arm64 assembly, the arm64 SSA
// backend's own, wasmbin from two WASI previews, the interpreter's
// `os.File.Truncate`, and the self-host's generated Fern. Each gets the same
// probe, because each packs the call itself.
//
// Three properties, and the third is the reason the builtin exists:
//
//   - shrinking discards the tail and growing extends with a hole that reads
//     as zeros, so one file walked down and back up is `hello` + four NULs;
//   - a negative length is the kernel's EINVAL rather than a clamp here,
//     which a resize that silently did nothing would hide;
//   - a file the process created under a mode that would REFUSE a fresh open
//     still resizes, because the permission check happened at the open. That
//     is what a path-based truncate(2) cannot do and what GNU `truncate`
//     relies on: `umask 222` leaves a new file 0444.
//
// The third is asserted through `open_exclusive`, whose 0600 creation mode
// this test then chmods to 0444 — rather than through a umask, which is
// process-global state the suite's other tests would inherit. WASI has no
// file mode at all and E066 refuses `chmod` there, so the wasm leg runs the
// first two properties and says so rather than carrying a mode that would
// describe nothing.

// writerTruncateSource probes the properties above under `dir`, or under the
// working directory when `dir` is "" (the wasm leg, which runs inside its
// preopen). `perms` drops the mode property for a target that has no modes.
// The exit code names the step that failed.
// `closed` drops the closed-handle property on wasm: `close` there drops the
// preview-2 descriptor resource, and a dropped handle traps rather than
// answering EBADF.
func writerTruncateSource(dir string, perms bool, closed bool) string {
	at := func(name string) string {
		if dir == "" {
			return `"` + name + `"`
		}
		return `"` + filepath.Join(dir, name) + `"`
	}
	locked := ""
	if perms {
		// A descriptor already open outlives the mode that would refuse a
		// new one: this is the whole reason the method exists beside the
		// path form. 292 is 0444.
		locked = `    match (open_exclusive(` + at("locked.txt") + `)) {
        Ok(w) => {
            match (w.write("0123456789")) { Some(e) => { return 9; }, None => {} }
            match (chmod(` + at("locked.txt") + `, 292)) { Ok(_) => { }, Err(e) => { return 10; } }
            match (w.truncate(3 as i64)) { Some(e) => { return 11; }, None => {} }
            w.close();
        },
        Err(e) => { return 12; }
    }
`
	}
	shut := ""
	if closed {
		// ftruncate(2) of a descriptor that is no longer open is EBADF on
		// every kernel, so the compiled backends get this from the kernel
		// for free. The interpreter has no kernel to ask — it resolves the
		// fd through its own handle table — so this arm is what holds that
		// table to the same answer instead of aborting the program, the way
		// stat / seek / the sync family already are.
		shut = `    match (open_writer(` + at("closed.txt") + `)) {
        Ok(w) => {
            match (w.write("abc")) { Some(e) => { return 13; }, None => {} }
            w.close();
            match (w.truncate(0 as i64)) { Some(e) => { }, None => { return 14; } }
        },
        Err(e) => { return 15; }
    }
`
	}
	return `function main(): i32 {
    match (open_writer(` + at("grow.txt") + `)) {
        Ok(w) => {
            match (w.write("hello world")) { Some(e) => { return 2; }, None => {} }
            match (w.truncate(5 as i64)) { Some(e) => { return 3; }, None => {} }
            match (w.truncate(9 as i64)) { Some(e) => { return 4; }, None => {} }
            match (w.close()) { Some(e) => { return 5; }, None => {} }
        },
        Err(e) => { return 6; }
    }
    match (open_writer(` + at("neg.txt") + `)) {
        Ok(w) => {
            match (w.truncate((0 as i64) - (1 as i64))) {
                Some(e) => { },
                None => { return 7; }
            }
            w.close();
        },
        Err(e) => { return 8; }
    }
` + locked + shut + `    return 0;
}
`
}

func writerTruncateCheckTree(t *testing.T, dir string, perms bool, closed bool) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "grow.txt"))
	if err != nil {
		t.Fatalf("read grow.txt: %v", err)
	}
	if want := "hello\x00\x00\x00\x00"; string(got) != want {
		t.Errorf("grow.txt = %q, want %q — the shrink kept the tail or the grow did not hole", got, want)
	}
	fi, err := os.Stat(filepath.Join(dir, "neg.txt"))
	if err != nil {
		t.Fatalf("stat neg.txt: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("neg.txt is %d bytes — a refused resize changed the file", fi.Size())
	}
	if closed {
		shut, err := os.ReadFile(filepath.Join(dir, "closed.txt"))
		if err != nil {
			t.Fatalf("read closed.txt: %v", err)
		}
		if string(shut) != "abc" {
			t.Errorf("closed.txt = %q, want %q — the resize on a closed handle went through", shut, "abc")
		}
	}
	if !perms {
		return
	}
	locked, err := os.ReadFile(filepath.Join(dir, "locked.txt"))
	if err != nil {
		t.Fatalf("read locked.txt: %v", err)
	}
	if string(locked) != "012" {
		t.Errorf("locked.txt = %q, want %q — the resize did not go through the descriptor", locked, "012")
	}
}

func TestX86_64WriterTruncate(t *testing.T) {
	dir := t.TempDir()
	code, _ := compileRunX86_64WithSetup(t, writerTruncateSource(dir, true, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see writerTruncateSource)", code)
	}
	writerTruncateCheckTree(t, dir, true, true)
}

func TestArm64WriterTruncate(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, writerTruncateSource(dir, true, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see writerTruncateSource)\n%s", code, out)
	}
	writerTruncateCheckTree(t, dir, true, true)
}

func TestArm64SSAWriterTruncate(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, writerTruncateSource(dir, true, true), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see writerTruncateSource)\n%s", code, stderr)
	}
	writerTruncateCheckTree(t, dir, true, true)
}

func TestInterpWriterTruncate(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, writerTruncateSource(dir, true, true)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see writerTruncateSource)", code)
	}
	writerTruncateCheckTree(t, dir, true, true)
}

// The wasm leg runs under the component's preopen, so its paths are relative
// and main's return reaches us on stdout rather than as the exit status.
func TestWASMWriterTruncate(t *testing.T) {
	p := buildComponent(t, writerTruncateSource("", false, false))
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see writerTruncateSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
	writerTruncateCheckTree(t, dir, false, false)
}

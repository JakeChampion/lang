// The write-back family end to end on every backend: `sync()`, and
// `fsync` / `fdatasync` / `syncfs` on both handle types.
//
// What no unit test can assert: five hand-written implementations issue these
// calls. x86-64, arm64-linux and arm64-ssa each emit the syscall from
// hand-written assembly with its own frame discipline and its own literal
// syscall NUMBER; the interpreter goes through Go's syscall package behind a
// per-OS split; and wasmbin has two bodies per method over WASI, where syncfs
// exists in neither preview and must refuse by name.
//
// A wrong syscall number is the failure this is built to catch, and it is
// invisible to any test that only checks a call compiled: a number that names
// some OTHER syscall can return 0 and look like success. So the probe never
// trusts a bare `ok` — it pins each call to a DISTINGUISHING answer:
//
//   - fsync and fdatasync on a character device are EINVAL, where syncfs on
//     the same descriptor succeeds. That separates the per-FILE pair from the
//     per-FILESYSTEM call: a syncfs that was really an fsync fails it one
//     way, an fsync that was really a syncfs fails it the other.
//   - every method on a CLOSED handle is EBADF, which a call that ignored its
//     argument (or never reached the kernel) cannot produce.
//
// What it does NOT separate is fsync from fdatasync, and no behavioural test
// can: their error contracts are identical — every descriptor that answers
// EINVAL for one answers EINVAL for the other, both answer EBADF on a closed
// fd, and fdatasync's only difference is metadata it may skip WRITING, which
// nothing here can observe. (A directory fd does not separate them either;
// measured, both succeed.) So a transposition of the two — and they are
// adjacent on both Linux targets, though not on Darwin, where they are 95 and
// 187 — passes everything below. That pair is held instead by
// TestSyscallNumbersMatchTheKernelTable in each native backend, which compares
// the hand-written table against Go's own generated zsysnum transcription of
// the same kernel source.
//
// The Go side then reads the bytes back through os.ReadFile, so a write the
// program believed it had flushed is checked against the tree rather than
// against the return value it was handed.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// syncSource is the probe, parameterised by the directory its relative paths
// resolve against — "" for the backends whose cwd is already there (wasm under
// its preopen), an absolute prefix for the rest.
//
// `wasm` selects the arm that expects syncfs to be REFUSED: neither WASI
// preview has a per-filesystem flush, so the wasm bodies answer Unsupported
// where a kernel flushes. Asserting the refusal is the point — a body that
// quietly answered None would claim a flush the caller never got.
//
// Every failure returns its own exit code, so the number names the step.
func syncSource(prefix string, wasm bool, chardev bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	// On wasm the FIFO cases cannot run at all (no mknod there), and syncfs
	// is the refusal rather than the success.
	syncfsOnFile := `match (w.syncfs()) { None => {}, Some(_) => { return 12; } }`
	// A CHARACTER DEVICE separates the three calls from one another.
	// MEASURED on Linux: fsync and fdatasync of /dev/zero are both EINVAL,
	// where syncfs of the same descriptor succeeds — the fd names a
	// filesystem even when it names nothing fsync can write. GNU `sync`
	// reports exactly that pair ("error syncing '/dev/zero': Invalid
	// argument" against a silent `sync -f /dev/zero`), so a helper that
	// issued the wrong syscall number fails exactly one of these.
	//
	// A FIFO gives the same three answers, and is what GNU's own probe
	// uses, but opening one read-only BLOCKS until a writer arrives —
	// Fern's open_reader passes no O_NONBLOCK — so it would hang the run
	// rather than fail it.
	chardevBlock := `
    match (open_reader("/dev/zero")) {
        Ok(f) => {
            match (f.fsync())     { None => { return 31; }, Some(_) => {} }
            match (f.fdatasync()) { None => { return 32; }, Some(_) => {} }
            match (f.syncfs())    { None => {}, Some(_) => { return 33; } }
            f.close();
        },
        Err(_) => { return 34; }
    }`
	// EVERY method on a CLOSED handle is EBADF on a kernel, which is what
	// proves the descriptor reached it: a call that ignored its argument
	// answers None here. Preview 2 cannot be asked — `close` there drops the
	// descriptor RESOURCE, and the component model traps on a dropped handle
	// rather than returning an error — so the wasm legs stop at the close.
	closedWriter := `match (w.fsync())     { None => { return 14; }, Some(_) => {} }
            match (w.fdatasync()) { None => { return 15; }, Some(_) => {} }`
	closedReader := `match (r.syncfs())    { None => { return 22; }, Some(_) => {} }`
	if !chardev {
		chardevBlock = ""
	}
	if wasm {
		syncfsOnFile = `match (w.syncfs()) { None => { return 12; }, Some(_) => {} }`
		chardevBlock = ""
		closedWriter = "// a dropped preview-2 descriptor traps rather than answering EBADF"
		closedReader = closedWriter
	}

	return fmt.Sprintf(`function main(): i32 {
    // A Writer flushed through every method, then read back by the Go side.
    match (open_writer(%[1]q)) {
        Ok(w) => {
            match (w.write("durable\n")) { None => {}, Some(_) => { return 10; } }
            match (w.fsync())     { None => {}, Some(_) => { return 11; } }
            %[3]s
            match (w.fdatasync()) { None => {}, Some(_) => { return 13; } }
            w.close();
            %[6]s
        },
        Err(_) => { return 16; }
    }

    // The same three on a Reader: one op each, two method names, so a
    // backend that wired only the Writer half is caught here.
    match (open_reader(%[1]q)) {
        Ok(r) => {
            match (r.fsync())     { None => {}, Some(_) => { return 20; } }
            match (r.fdatasync()) { None => {}, Some(_) => { return 21; } }
            r.close();
            %[7]s
        },
        Err(_) => { return 23; }
    }
%[4]s

    // The whole-machine flush. It returns nothing and cannot fail, so the
    // only thing to assert is that the program is still running afterwards —
    // a bad syscall number here is a fault, not a bad answer.
    %[2]s
    match (write_file(%[5]q, "after\n")) { Ok(_) => {}, Err(_) => { return 40; } }
    return 0;
}
`, p("durable.txt"), syncCall(wasm), syncfsOnFile, chardevBlock, p("after.txt"),
		closedWriter, closedReader)
}

// syncCall is `sync()` on the native targets and nothing on wasm: no wasm
// profile holds the `fssync` capability, so a wasm module that named it would
// be refused at E066 rather than compiled. The refusal itself is asserted by
// TestWASMSyncRefused below.
func syncCall(wasm bool) string {
	if wasm {
		return "// sync() is E066-refused on this target; see TestWASMSyncRefused."
	}
	return "sync();"
}

// syncCheckTree reads the bytes back through Go's own stat and read rather
// than Fern's, so a broken reader cannot make the probe self-consistent.
func syncCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "durable.txt"))
	if err != nil {
		t.Fatalf("read durable.txt: %v", err)
	}
	if string(got) != "durable\n" {
		t.Errorf("durable.txt = %q, want %q", got, "durable\n")
	}
	after, err := os.ReadFile(filepath.Join(dir, "after.txt"))
	if err != nil {
		t.Fatalf("read after.txt: %v — the program did not survive sync()", err)
	}
	if string(after) != "after\n" {
		t.Errorf("after.txt = %q, want %q", after, "after\n")
	}
}

func TestX86_64Sync(t *testing.T) {
	dir := t.TempDir()
	code, out := compileRunX86_64WithSetup(t, syncSource(dir, false, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see syncSource)\n%s", code, out)
	}
	syncCheckTree(t, dir)
}

func TestArm64Sync(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, syncSource(dir, false, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see syncSource)\n%s", code, out)
	}
	syncCheckTree(t, dir)
}

// The arm64 SSA-direct backend is a third hand-written implementation of the
// same four syscalls, with its own frame discipline and its own handle layout
// (the fd at [handle+8] rather than [handle]), so it gets the probe rather
// than being taken on trust.
func TestArm64SSASync(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, syncSource(dir, false, true), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see syncSource)\n%s", code, stderr)
	}
	syncCheckTree(t, dir)
}

func TestInterpSync(t *testing.T) {
	dir := t.TempDir()
	// The interpreter runs on THIS host. The character-device answers were
	// measured on Linux; macOS flushes /dev/zero without complaint, so that
	// half of the probe only runs where it was measured.
	if code := runInterpExit(t, syncSource(dir, false, runtime.GOOS == "linux")); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see syncSource)", code)
	}
	syncCheckTree(t, dir)
}

// Preview 1 answers fsync / fdatasync with fd_sync / fd_datasync over an errno
// return, where the component leg goes through descriptor.sync / sync-data and
// a result<_, error-code>. Two separate hand-written bodies, so two runs.
func TestWASMPreview1Sync(t *testing.T) {
	mod := buildPreview1Module(t, syncSource("", true, false))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see syncSource)", got)
	}
	syncCheckTree(t, dir)
}

// main's return reaches us on STDOUT, not as the exit status: the harness
// builds with PrintMainResult.
func TestWASMSync(t *testing.T) {
	p := buildComponent(t, syncSource("", true, false))
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see syncSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
	syncCheckTree(t, dir)
}

// Both wasm worlds refuse the whole-machine flush. Preview 1's fd_sync is one
// descriptor's and a preopen is a capability handle rather than a mount, so
// there is no set of filesystems for a component to name; a no-op would be a
// flush the caller asked for and never got, so the honest answer is the
// compile-time refusal.
func TestWASMSyncRefused(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    sync();
    return 0;
}
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted sync(); it has no machine to flush", target)
			continue
		}
		if vs[0].Builtin != "sync" || vs[0].Capability != "fssync" {
			t.Errorf("%s refused %q on %q, want sync on fssync", target, vs[0].Builtin, vs[0].Capability)
		}
	}
	// The native targets all have it, and a capability that refused
	// everywhere would pass the loop above while making the builtin
	// unreachable.
	for _, target := range []string{"arm64-linux", "arm64-darwin", "x86-64-linux"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s refused sync(): %+v", target, vs)
		}
	}
}

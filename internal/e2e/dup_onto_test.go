// `r.dup_onto(fd)` / `w.dup_onto(fd)` end to end on every backend: dup3(2) of
// the handle's descriptor onto a number the caller chose.
//
// What no unit test can assert: four hand-written implementations issue the
// call. x86-64, arm64-linux and arm64-ssa each emit it from hand-written
// assembly with its own frame discipline, its own literal syscall NUMBER and
// its own handle layout (the fd at [handle] on two of them, [handle+8] on the
// third); the interpreter goes through Go's syscall package behind a per-OS
// split, because Linux has dup3 and XNU only dup2; and both wasm previews
// refuse it.
//
// The probe never trusts a bare `ok`. Each answer is pinned to one an
// operand mistake cannot produce:
//
//   - a live handle onto an UNUSED number succeeds, so the call reaches the
//     kernel with a plausible pair rather than failing on arrival.
//   - a live handle onto a NEGATIVE number is EBADF, so the destination is
//     being read rather than ignored.
//   - a CLOSED handle onto a valid number is EBADF, so the source is the
//     handle's own descriptor rather than something ambient.
//   - and the redirection itself is observable: after `w.dup_onto(1)` the
//     program's own `print` lands in the FILE. The Go side reads those bytes
//     back off disk, so the claim is checked against the tree rather than
//     against the value the program was handed.
//
// That last one is what catches a TRANSPOSITION — operands passed as
// dup3(newfd, own_fd) — and the reason it takes a tree check rather than an
// errno: where the destination happens to be open already the transposed
// call SUCCEEDS, answers None to every arm above, and leaves fd 1 alone. Fd 7
// is closed in a compiled binary and open in the interpreter's Go process, so
// the two legs would disagree about which arm notices. Measured by
// transposing the implementations in turn; a wrong syscall NUMBER fails the
// first arm instead.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// dupOntoSource is the probe, parameterised by the directory its relative
// paths resolve against — "" for the backends whose cwd is already there
// (wasm under its preopen), an absolute prefix for the rest.
//
// `wasm` selects the arm that expects every call to be REFUSED. Asserting the
// refusal is the point: a body that quietly answered None would claim a
// descriptor the caller never got, and the next thing the caller does is
// write to that number.
func dupOntoSource(dir string, wasm bool) string {
	p := func(name string) string {
		if dir == "" {
			return name
		}
		return filepath.Join(dir, name)
	}

	// 7 is above the three standard descriptors and above the pty the `tty`
	// harness hands out, so nothing in any of these runners has it open.
	readerArm := `match (r.dup_onto(7))    { None => {}, Some(_) => { return 11; } }
            match (r.dup_onto(0 - 1)) { None => { return 12; }, Some(_) => {} }`
	closedArm := `match (r.dup_onto(7))    { None => { return 13; }, Some(_) => {} }`
	writerArm := `match (w.dup_onto(1))    { None => {}, Some(_) => { return 21; } }
            print("redirected");`
	if wasm {
		// Refused on both previews, so every arm flips: the success cases
		// become failures and the closed-handle case cannot be reached at
		// all (a dropped preview-2 descriptor traps rather than answering).
		readerArm = `match (r.dup_onto(7))    { None => { return 11; }, Some(_) => {} }
            match (r.dup_onto(0 - 1)) { None => { return 12; }, Some(_) => {} }`
		closedArm = "// a dropped preview-2 descriptor traps rather than answering EBADF"
		writerArm = `match (w.dup_onto(1))    { None => { return 21; }, Some(_) => {} }`
	}

	return fmt.Sprintf(`function main(): i32 {
    match (write_file(%[1]q, "hello\n")) { Ok(_) => {}, Err(_) => { return 10; } }

    // The Reader half: the same op reached through the other method name, so
    // a backend that wired only the Writer is caught here.
    match (open_reader(%[1]q)) {
        Ok(r) => {
            %[2]s
            r.close();
            %[3]s
        },
        Err(_) => { return 14; }
    }

    // The Writer half, and the one assertion about the descriptor TABLE
    // rather than about an errno.
    match (open_writer(%[4]q)) {
        Ok(w) => {
            %[5]s
            w.close();
        },
        Err(_) => { return 22; }
    }
    return 0;
}
`, p("data.txt"), readerArm, closedArm, p("out.txt"), writerArm)
}

// dupOntoCheckTree reads the redirected bytes back through Go's own read
// rather than Fern's, so a broken reader cannot make the probe
// self-consistent. `w.close()` ran after the print, and closing the handle
// leaves fd 1 alone — that is dup2 semantics and what `nohup` relies on — so
// the file holds the line either way.
func dupOntoCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read out.txt: %v", err)
	}
	if string(got) != "redirected\n" {
		t.Errorf("out.txt = %q, want %q — fd 1 was not replaced", got, "redirected\n")
	}
}

func TestX86_64DupOnto(t *testing.T) {
	dir := t.TempDir()
	code, out := compileRunX86_64WithSetup(t, dupOntoSource(dir, false), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dupOntoSource)\n%s", code, out)
	}
	dupOntoCheckTree(t, dir)
}

func TestArm64DupOnto(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, dupOntoSource(dir, false))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dupOntoSource)\n%s", code, out)
	}
	dupOntoCheckTree(t, dir)
}

// The arm64 SSA-direct backend is a third hand-written implementation, with
// its own frame discipline and its own handle layout (the fd at [handle+8]
// rather than [handle]), so it gets the probe rather than being taken on
// trust.
func TestArm64SSADupOnto(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, dupOntoSource(dir, false), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dupOntoSource)\n%s", code, stderr)
	}
	dupOntoCheckTree(t, dir)
}

// The interpreter's descriptors are the program's, so the redirect really
// does take fd 1 away from `print` — which is why this leg runs the program
// as a subprocess rather than in the test's own process.
func TestInterpDupOnto(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, dupOntoSource(dir, false)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dupOntoSource)", code)
	}
	dupOntoCheckTree(t, dir)
}

// Preview 1 and the component leg are two separate hand-written bodies, so
// two runs. Neither can install a descriptor at a chosen number: preview 1's
// fd_renumber CLOSES the source, which is a move rather than a duplicate and
// would leave the handle the caller still holds dangling, and preview 2 has
// no numbered table at all.
func TestWASMPreview1DupOntoUnsupported(t *testing.T) {
	mod := buildPreview1Module(t, dupOntoSource("", true))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dupOntoSource)", got)
	}
}

func TestWASMDupOntoUnsupported(t *testing.T) {
	p := buildComponent(t, dupOntoSource("", true))
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dupOntoSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
}

// It is a runtime refusal rather than an E066 one, which is the `syncfs`
// shape and not the `sync` shape: the method has an error channel, so
// "Unsupported" is an answer the caller can act on, where a whole-machine
// flush that returns nothing has nowhere to put the refusal. Nothing here
// should be capability-gated — a target with no descriptor table simply says
// so at the call.
func TestWASMDupOntoNotCapabilityGated(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    match (open_writer("x")) {
        Ok(w) => { match (w.dup_onto(1)) { None => {}, Some(_) => {} } },
        Err(_) => {}
    }
    return 0;
}
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
}

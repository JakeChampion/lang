// The filesystem-metadata primitives (#9059) end to end on every backend
// that provides them: rename / set_file_times everywhere, plus `chmod` on the
// natives.
//
// What these assert that no unit test can: each backend builds the syscall
// arguments by hand — the two arm64 emitters and x86-64 from hand-written
// assembly, wasmbin from two WASI previews — and each is a place to swap the
// two path operands of a rename, to write a timespec pair in the wrong order,
// or to drop an omit sentinel. Every one of those produces a plausible call
// that succeeds against the wrong thing, so the probe reads the metadata BACK
// with `stat` / `lstat` rather than trusting the return value.
//
// `chmod` is native-only: neither WASI preview has permission bits, so E066
// refuses it there (capability `fsmode`) and the wasm probes are the same
// program with that block removed.
//
// The wasm leg runs TWICE, once per preview, because the two are separate
// hand-written bodies over separate WASI calls — `path_rename` /
// `path_filestat_set_times` against `descriptor.rename-at` /
// `descriptor.set-times-at`, one an errno return and the other a return area,
// one an `fstflags` bit for an omitted timestamp and the other a variant arm.
// Everything else in internal/e2e reaches wasm through the component, so
// without the preview-1 leg here these bodies would be compiled and never
// run.
package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// fsMetaSource is the probe, parameterised by the directory its relative
// paths resolve against — "" for the backends that run with their cwd already
// there (wasm under its preopen), an absolute prefix for the rest.
//
// `withChmod` is off for wasm. `withNoFollow` is off only for the interpreter
// on a platform whose `syscall` package exposes no utimensat, where the
// nofollow flag is refused rather than downgraded to a follow — see
// internal/interp/utimensat_other.go.
//
// Every failure returns its own exit code, so the number names the step.
func fsMetaSource(prefix string, withChmod, withNoFollow bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	src := fmt.Sprintf(`function main(): i32 {
    // A rename moves the entry: the bytes arrive under the new name and the
    // old one stops resolving. Nothing is copied.
    match (write_file(%[1]q, "one\n")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (rename(%[1]q, %[2]q)) { Ok(_) => {}, Err(_) => { return 2; } }
    match (read_file(%[2]q)) { Ok(s) => { if (s != "one\n") { return 3; } }, Err(_) => { return 4; } }
    match (read_file(%[1]q)) { Ok(_) => { return 5; }, Err(_) => {} }
    // A missing source is an error rather than a created destination.
    match (rename(%[3]q, %[4]q)) { Ok(_) => { return 6; }, Err(_) => {} }
    // An existing destination is REPLACED, not refused: that is what makes
    // mv over an existing file one step rather than two.
    match (write_file(%[4]q, "two\n")) { Ok(_) => {}, Err(_) => { return 7; } }
    match (rename(%[4]q, %[2]q)) { Ok(_) => {}, Err(_) => { return 8; } }
    match (read_file(%[2]q)) { Ok(s) => { if (s != "two\n") { return 9; } }, Err(_) => { return 10; } }
    match (read_file(%[4]q)) { Ok(_) => { return 11; }, Err(_) => {} }

    // set_file_times writes both timestamps, seconds and nanoseconds, and
    // stat reads back exactly what went in.
    match (set_file_times(%[2]q, 1000000000, 123456789, 2000000000, 987654321, 0)) { Ok(_) => {}, Err(_) => { return 12; } }
    match (stat(%[2]q)) {
        Ok(st) => {
            if (st.atime != 1000000000) { return 13; }
            if (st.atime_nsec != 123456789) { return 14; }
            if (st.mtime != 2000000000) { return 15; }
            if (st.mtime_nsec != 987654321) { return 16; }
        },
        Err(_) => { return 17; }
    }
    // Omit the access time (bit 1): the modification time moves and the
    // access time keeps the value the call before it wrote, even though this
    // call passed a different one.
    match (set_file_times(%[2]q, 7, 7, 1500000000, 250000000, 2)) { Ok(_) => {}, Err(_) => { return 18; } }
    match (stat(%[2]q)) {
        Ok(st) => {
            if (st.atime != 1000000000) { return 19; }
            if (st.atime_nsec != 123456789) { return 20; }
            if (st.mtime != 1500000000) { return 21; }
            if (st.mtime_nsec != 250000000) { return 22; }
        },
        Err(_) => { return 23; }
    }
    // …and the mirror, omitting the modification time (bit 2).
    match (set_file_times(%[2]q, 1750000000, 500000000, 9, 9, 4)) { Ok(_) => {}, Err(_) => { return 24; } }
    match (stat(%[2]q)) {
        Ok(st) => {
            if (st.atime != 1750000000) { return 25; }
            if (st.atime_nsec != 500000000) { return 26; }
            if (st.mtime != 1500000000) { return 27; }
            if (st.mtime_nsec != 250000000) { return 28; }
        },
        Err(_) => { return 29; }
    }
    match (set_file_times(%[3]q, 0, 0, 0, 0, 0)) { Ok(_) => { return 30; }, Err(_) => {} }
`, p("a.txt"), p("b.txt"), p("missing.txt"), p("c.txt"))

	if withChmod {
		src += fmt.Sprintf(`    // chmod sets the bits verbatim — the umask filters a creation, and
    // this is not one — and it can only be read back through stat.
    match (chmod(%[1]q, 493)) { Ok(_) => {}, Err(_) => { return 31; } }
    match (stat(%[1]q)) { Ok(st) => { if ((st.mode & 4095) != 493) { return 32; } }, Err(_) => { return 33; } }
    match (chmod(%[1]q, 384)) { Ok(_) => {}, Err(_) => { return 34; } }
    match (stat(%[1]q)) { Ok(st) => { if ((st.mode & 4095) != 384) { return 35; } }, Err(_) => { return 36; } }
    match (chmod(%[2]q, 420)) { Ok(_) => { return 37; }, Err(_) => {} }
`, p("b.txt"), p("missing.txt"))
	}
	if withNoFollow {
		src += fmt.Sprintf(`    // Bit 0 writes the SYMLINK's own timestamps. Both halves matter: the
    // link moves and the file it names does not.
    match (create_symlink("b.txt", %[1]q)) { Ok(_) => {}, Err(_) => { return 38; } }
    // lstat has to describe the LINK, or the check below is vacuous: both
    // paths would name the same file and the flag would prove nothing.
    match (lstat(%[1]q)) { Ok(st) => { if (st.is_file) { return 44; } }, Err(_) => { return 45; } }
    match (set_file_times(%[1]q, 100000000, 0, 200000000, 0, 1)) { Ok(_) => {}, Err(_) => { return 39; } }
    match (lstat(%[1]q)) { Ok(st) => { if (st.mtime != 200000000) { return 40; } }, Err(_) => { return 41; } }
    match (stat(%[2]q)) { Ok(st) => { if (st.mtime != 1500000000) { return 42; } }, Err(_) => { return 43; } }
`, p("link"), p("b.txt"))
	}
	src += `    return 0;
}
`
	return src
}

// fsMetaCheckTree asserts the filesystem the probe left behind, which is the
// half a return value cannot prove: one file under the renamed-to name with
// the bytes of the last thing moved onto it, nothing under either name it was
// moved away from, and the mode the final chmod asked for.
func fsMetaCheckTree(t *testing.T, dir string, withChmod bool) {
	t.Helper()
	for _, gone := range []string{"a.txt", "c.txt", "missing.txt"} {
		if _, err := os.Lstat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s exists after the renames (lstat err = %v)", gone, err)
		}
	}
	fi, err := os.Lstat(filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatalf("lstat b.txt: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if string(got) != "two\n" {
		t.Errorf("b.txt = %q, want %q — a rename copied rather than moved, or moved the wrong operand", got, "two\n")
	}
	if withChmod {
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("b.txt mode = %o, want 600", perm)
		}
	}
}

func TestX86_64FsMetaPrimitives(t *testing.T) {
	dir := t.TempDir()
	code, _ := compileRunX86_64WithSetup(t, fsMetaSource(dir, true, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see fsMetaSource)", code)
	}
	fsMetaCheckTree(t, dir, true)
}

func TestArm64FsMetaPrimitives(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, fsMetaSource(dir, true, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see fsMetaSource)\n%s", code, out)
	}
	fsMetaCheckTree(t, dir, true)
}

// The arm64 SSA-direct backend is a third hand-written implementation of the
// same three syscalls, with its own frame discipline, so it gets the same
// probe rather than being taken on trust.
func TestArm64SSAFsMetaPrimitives(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, fsMetaSource(dir, true, true), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see fsMetaSource)\n%s", code, stderr)
	}
	fsMetaCheckTree(t, dir, true)
}

// The interpreter answers these from Go's syscall package, so it is a fourth
// implementation and the one an in-language test suite runs under. Its
// nofollow leg is Linux-only: `syscall.UtimesNano` is the only utimensat the
// other platforms expose and it hard-codes a zero flags word, so the flag is
// refused there rather than silently following the link.
func TestInterpFsMetaPrimitives(t *testing.T) {
	dir := t.TempDir()
	noFollow := runtime.GOOS == "linux"
	if code := runInterpExit(t, fsMetaSource(dir, true, noFollow)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see fsMetaSource)", code)
	}
	fsMetaCheckTree(t, dir, true)
}

// buildPreview1Module compiles src to a bare preview-1 core module — the
// wasm32-wasi artifact `-emit core-module` produces, importing
// `wasi_snapshot_preview1` directly rather than through a component wrapper —
// and returns its path.
func buildPreview1Module(t *testing.T, src string) string {
	t.Helper()
	skipIfPreview2Missing(t) // the gate is wasmtime itself, which runs both

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{ForceMemorySection: true})
	if err != nil {
		t.Fatalf("wasmbin (preview 1): %v", err)
	}
	out := filepath.Join(dir, "main.wasm")
	if err := os.WriteFile(out, bin, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	return out
}

// runPreview1Module runs a preview-1 core module with `workDir` as its only
// preopen and answers main's return value, which `--invoke` prints.
func runPreview1Module(t *testing.T, modPath, workDir string) int {
	t.Helper()
	cmd := exec.Command("wasmtime", "run", "--dir="+workDir+"::/", "--invoke", "main", modPath)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	_ = cmd.Run()
	if rejected, why := wasmRejected(se.String()); rejected {
		t.Fatalf("wasmtime REJECTED the preview-1 module (%s)\nstderr:\n%s", why, se.String())
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", code, so.String(), se.String())
	}
	for _, ln := range strings.Split(so.String(), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			return n
		}
	}
	t.Fatalf("could not parse main's return from wasmtime output:\n%s\nstderr:\n%s", so.String(), se.String())
	return 0
}

// Preview 1 answers these with path_rename and path_filestat_set_times, over
// an errno return rather than a return area, and it spells an omitted
// timestamp as a cleared `fstflags` bit rather than a variant arm — so it is
// a separate body from the component leg below and gets its own run.
func TestWASMPreview1FsMetaPrimitives(t *testing.T) {
	mod := buildPreview1Module(t, fsMetaSource("", false, true))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see fsMetaSource)", got)
	}
	fsMetaCheckTree(t, dir, false)
}

// The wasm leg runs under the component's preopen, so its paths are relative
// and `chmod` is absent: neither WASI preview has permission bits and E066
// refuses the builtin on that target, which is the honest answer rather than
// a mode word that describes nothing.
//
// main's return reaches us on STDOUT, not as the exit status: the harness
// builds with PrintMainResult.
func TestWASMFsMetaPrimitives(t *testing.T) {
	p := buildComponent(t, fsMetaSource("", false, true))
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see fsMetaSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
	fsMetaCheckTree(t, dir, false)
}

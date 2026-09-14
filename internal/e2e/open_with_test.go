// `open_reader_with` / `open_writer_with` end to end on every native
// backend: the create bit creates without truncating, its absence is
// NotFound, and the non-blocking bit is what makes a FIFO with no peer
// openable at all — the reader's open returns at once where it would
// wait for a writer, and the writer's is ENXIO where it would wait for a
// reader. A backend that dropped the bit would hang the reader's open
// here rather than fail it, which is why no step past it is reachable
// without it.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Every failure returns its own exit code, so the number names the step.
func openWithSource(dir string) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	return fmt.Sprintf(`function main(): i32 {
    // Bit 0 creates, and a second open under it neither truncates nor
    // appends: the second write lands on the first byte.
    match (open_writer_with(%[1]q, 1)) { Ok(w) => { w.write("abc"); w.close(); }, Err(_) => { return 1; } }
    match (open_writer_with(%[1]q, 1)) { Ok(w) => { w.write("Z"); w.close(); }, Err(_) => { return 2; } }
    match (read_file(%[1]q)) { Ok(s) => { if (s != "Zbc") { return 3; } }, Err(_) => { return 4; } }
    // Without it a missing name is NotFound, for the writer and the
    // reader alike.
    match (open_writer_with(%[2]q, 0)) { Ok(w) => { w.close(); return 5; }, Err(e) => { match (e) { NotFound(_) => {}, _ => { return 6; } } } }
    match (open_reader_with(%[2]q, 2)) { Ok(r) => { r.close(); return 7; }, Err(e) => { match (e) { NotFound(_) => {}, _ => { return 8; } } } }
    match (open_reader_with(%[1]q, 0)) {
        Ok(r) => {
            match (r.read_chunk(3)) { Ok(s) => { if (s != "Zbc") { return 9; } }, Err(_) => { return 10; } }
            r.close();
        },
        Err(_) => { return 11; }
    }
    // A FIFO with no peer. Bit 1 is what makes the reader's open return
    // at all, and what makes the writer's an ENXIO rather than a wait.
    match (mknod(%[3]q, %[4]d, 0, 0)) { Ok(_) => {}, Err(_) => { return 12; } }
    match (open_reader_with(%[3]q, 2)) { Ok(r) => { r.close(); }, Err(_) => { return 13; } }
    match (open_writer_with(%[3]q, 2)) { Ok(w) => { w.close(); return 14; }, Err(e) => { match (e) { NotFound(_) => { return 15; }, _ => {} } } }
    return 0;
}
`, p("w.txt"), p("missing.txt"), p("fifo"), mknodFifo0666)
}

// openWithCheckTree reads the tree back through Go: the bytes the two
// writes left, the FIFO the probe made, and no file where create was not
// asked for.
func openWithCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "w.txt"))
	if err != nil {
		t.Fatalf("read w.txt: %v", err)
	}
	if string(got) != "Zbc" {
		t.Errorf("w.txt = %q, want %q — the second open truncated or appended", got, "Zbc")
	}
	fi, err := os.Lstat(filepath.Join(dir, "fifo"))
	if err != nil {
		t.Fatalf("lstat fifo: %v", err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("fifo is not a named pipe: %v", fi.Mode())
	}
	if _, err := os.Lstat(filepath.Join(dir, "missing.txt")); !os.IsNotExist(err) {
		t.Errorf("missing.txt exists (lstat err = %v) — an open without the create bit created it", err)
	}
}

func TestX86_64OpenWith(t *testing.T) {
	dir := t.TempDir()
	code, out := compileRunX86_64WithSetup(t, openWithSource(dir), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see openWithSource)\n%s", code, out)
	}
	openWithCheckTree(t, dir)
}

func TestArm64OpenWith(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, openWithSource(dir))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see openWithSource)\n%s", code, out)
	}
	openWithCheckTree(t, dir)
}

func TestArm64SSAOpenWith(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, openWithSource(dir), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see openWithSource)\n%s", code, stderr)
	}
	openWithCheckTree(t, dir)
}

func TestInterpOpenWith(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, openWithSource(dir)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see openWithSource)", code)
	}
	openWithCheckTree(t, dir)
}

// Neither WASI preview has a FIFO, so the wasm probe is the file half
// only: create without truncation, NotFound without the bit, and the
// non-blocking bit accepted on a regular file — a preview-1 fdflag,
// nothing at all on preview 2.
const openWithWasmSrc = `function main(): i32 {
    match (open_writer_with("w.txt", 1)) { Ok(w) => { w.write("abc"); w.close(); }, Err(_) => { return 1; } }
    match (open_writer_with("w.txt", 1)) { Ok(w) => { w.write("Z"); w.close(); }, Err(_) => { return 2; } }
    match (read_file("w.txt")) { Ok(s) => { if (s != "Zbc") { return 3; } }, Err(_) => { return 4; } }
    match (open_writer_with("missing.txt", 0)) { Ok(w) => { w.close(); return 5; }, Err(e) => { match (e) { NotFound(_) => {}, _ => { return 6; } } } }
    match (open_reader_with("w.txt", 2)) {
        Ok(r) => {
            match (r.read_chunk(3)) { Ok(s) => { if (s != "Zbc") { return 7; } }, Err(_) => { return 8; } }
            r.close();
        },
        Err(_) => { return 9; }
    }
    return 0;
}`

func TestWASMPreview1OpenWith(t *testing.T) {
	mod := buildPreview1Module(t, openWithWasmSrc)
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see openWithWasmSrc)", got)
	}
	openWithWasmCheckTree(t, dir)
}

func TestWASMOpenWith(t *testing.T) {
	p := buildComponent(t, openWithWasmSrc)
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see openWithWasmSrc)\nstdout:\n%s\nstderr:\n%s", got, stdout, stderr)
	}
	openWithWasmCheckTree(t, dir)
}

func openWithWasmCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "w.txt"))
	if err != nil {
		t.Fatalf("read w.txt: %v", err)
	}
	if string(got) != "Zbc" {
		t.Errorf("w.txt = %q, want %q — the second open truncated or appended", got, "Zbc")
	}
	if _, err := os.Lstat(filepath.Join(dir, "missing.txt")); !os.IsNotExist(err) {
		t.Errorf("missing.txt exists (lstat err = %v) — an open without the create bit created it", err)
	}
}

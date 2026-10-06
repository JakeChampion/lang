// `open_reader_with` / `open_writer_with` end to end on every native
// backend: the create bit creates without truncating, its absence is
// NotFound, and the non-blocking bit is what makes a FIFO with no peer
// openable at all — the reader's open returns at once where it would
// wait for a writer, and the writer's is ENXIO where it would wait for a
// reader. A backend that dropped the bit would hang the reader's open
// here rather than fail it, which is why no step past it is reachable
// without it. The seven open-time bits after exclusive (#9242) are
// checked by the kernel's own verdict where it has one — directory on a
// regular file, nofollow on a symlink — and by being accepted where it
// does not; a bit the target has no word for must come back Unsupported,
// which is what the direct and noatime steps pin on XNU.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
    // Bit 2 with bit 0 is the exclusive create: a fresh name is created at
    // 0666 through the umask (checked from Go), a taken one is AlreadyExists.
    match (open_writer_with(%[5]q, 5)) { Ok(w) => { w.write("x"); w.close(); }, Err(_) => { return 16; } }
    match (open_writer_with(%[5]q, 5)) { Ok(w) => { w.close(); return 17; }, Err(e) => { match (e) { AlreadyExists(_) => {}, _ => { return 18; } } } }
    // Bit 4 asks for a directory: the kernel refuses a regular file
    // (ENOTDIR, which is not Unsupported) and opens the directory itself.
    match (open_reader_with(%[1]q, 16)) { Ok(r) => { r.close(); return 19; }, Err(e) => { match (e) { Unsupported => { return 20; }, _ => {} } } }
    match (open_reader_with(%[6]q, 16)) { Ok(r) => { r.close(); }, Err(_) => { return 21; } }
    // Bit 9 refuses a symlink at the last component (ELOOP); without it
    // the same name opens.
    match (create_symlink(%[1]q, %[7]q)) { Ok(_) => {}, Err(_) => { return 22; } }
    match (open_reader_with(%[7]q, 512)) { Ok(r) => { r.close(); return 23; }, Err(e) => { match (e) { Unsupported => { return 24; }, _ => {} } } }
    match (open_reader_with(%[7]q, 0)) { Ok(r) => { r.close(); }, Err(_) => { return 25; } }
    // Bits 5, 6 and 8 (dsync, sync, noctty) are accepted on a regular
    // file; the write still lands on the first byte.
    match (open_writer_with(%[1]q, 32 | 64 | 256)) { Ok(w) => { w.write("Z"); w.close(); }, Err(_) => { return 26; } }
    // Bits 3 and 7 (direct, noatime): Linux has both and answers with the
    // filesystem's own verdict, never Unsupported — O_DIRECT may be
    // refused by a tmpfs, O_NOATIME is the owner's to ask for. XNU has
    // neither and must say so.
    match (open_reader_with(%[1]q, 8)) {
        Ok(r) => { r.close(); if (target_os() == "darwin") { return 27; } },
        Err(e) => { match (e) { Unsupported => { if (target_os() != "darwin") { return 28; } }, _ => { if (target_os() == "darwin") { return 29; } } } }
    }
    match (open_reader_with(%[1]q, 128)) {
        Ok(r) => { r.close(); if (target_os() == "darwin") { return 30; } },
        Err(e) => { match (e) { Unsupported => { if (target_os() != "darwin") { return 31; } }, _ => { return 32; } } }
    }
    return 0;
}
`, p("w.txt"), p("missing.txt"), p("fifo"), mknodFifo0666, p("excl.txt"), dir, p("lnk"))
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
	checkExclusiveCreate(t, dir)
}

// checkExclusiveCreate reads back the file the exclusive open made: its
// byte, and its mode, which is the create bit's 0666 through the umask and
// not open_exclusive's 0600 — the distinction #9237 exists for.
func checkExclusiveCreate(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "excl.txt"))
	if err != nil {
		t.Fatalf("read excl.txt: %v", err)
	}
	if string(got) != "x" {
		t.Errorf("excl.txt = %q, want %q", got, "x")
	}
	fi, err := os.Stat(filepath.Join(dir, "excl.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mask := syscall.Umask(0)
	syscall.Umask(mask)
	if want := os.FileMode(0o666 &^ mask); fi.Mode().Perm() != want {
		t.Errorf("excl.txt mode = %o, want %o (0666 through the umask %o)", fi.Mode().Perm(), want, mask)
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
// nothing at all on preview 2. Of the seven open-time bits, directory
// and nofollow are the host's verdict; dsync and sync are spelled (the
// DSYNC and SYNC fdflags, the two integrity-sync descriptor-flags) and
// then refused by wasmtime itself, which answers ENOTSUP for either sync
// flag on an open — the host's refusal, carried as Other with ENOTSUP (95), so
// a wasmtime that starts honouring them flips step 19 and says so here;
// direct, noatime and noctty — which neither preview can spell — are
// Unsupported before the host is asked.
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
    match (open_writer_with("excl.txt", 5)) { Ok(w) => { w.write("x"); w.close(); }, Err(_) => { return 10; } }
    match (open_writer_with("excl.txt", 5)) { Ok(w) => { w.close(); return 11; }, Err(e) => { match (e) { AlreadyExists(_) => {}, _ => { return 12; } } } }
    match (open_reader_with("w.txt", 16)) { Ok(r) => { r.close(); return 13; }, Err(e) => { match (e) { Unsupported => { return 14; }, _ => {} } } }
    match (create_symlink("w.txt", "lnk")) { Ok(_) => {}, Err(_) => { return 15; } }
    match (open_reader_with("lnk", 512)) { Ok(r) => { r.close(); return 16; }, Err(e) => { match (e) { Unsupported => { return 17; }, _ => {} } } }
    match (open_reader_with("lnk", 0)) { Ok(r) => { r.close(); }, Err(_) => { return 18; } }
    match (open_writer_with("w.txt", 32 | 64)) { Ok(w) => { w.close(); return 19; }, Err(e) => { match (e) { Other(_, _, n) => { if (n != 95) { return 27; } }, _ => { return 26; } } } }
    match (open_reader_with("w.txt", 8)) { Ok(r) => { r.close(); return 20; }, Err(e) => { match (e) { Unsupported => {}, _ => { return 21; } } } }
    match (open_reader_with("w.txt", 128)) { Ok(r) => { r.close(); return 22; }, Err(e) => { match (e) { Unsupported => {}, _ => { return 23; } } } }
    match (open_reader_with("w.txt", 256)) { Ok(r) => { r.close(); return 24; }, Err(e) => { match (e) { Unsupported => {}, _ => { return 25; } } } }
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
	dir := t.TempDir()
	out := runResultStdout(t, openWithWasmSrc, runOpts{workDir: dir})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see openWithWasmSrc)\nstdout:\n%s", got, out)
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
	if got, err := os.ReadFile(filepath.Join(dir, "excl.txt")); err != nil || string(got) != "x" {
		t.Errorf("excl.txt = %q, %v — the exclusive create did not land", got, err)
	}
}

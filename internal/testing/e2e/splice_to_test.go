// `r.splice_to(w, max)` end to end on every backend: splice(2) between two
// handles on Linux, through a pipe the runtime caches when neither side is
// one, and `Unsupported` wherever the kernel cannot move the bytes itself.
//
// The contract a caller builds on is that `Unsupported` took NOTHING from the
// reader, so a read_chunk / write fallback carries on from the same offset.
// The probe copies with exactly that loop and checks three shapes:
//
//   - file to file, where neither side is a pipe, so the bytes go through
//     the cached pipe; the copy is read back through Go's own read.
//   - a writer opened for APPEND, which splice(2) refuses: the answer must be
//     Unsupported, and the next read_chunk must still see the first byte.
//   - file to stdout, a pipe in these runners, so the direct splice.
//
// `spliced` says whether the backend is expected to move bytes itself. On the
// Linux backends it must, or the kernel path silently never runs; on wasm and
// in the interpreter every call is the refusal and the fallback does the work.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spliceToData is what the probe builds and writes: 25 bytes doubled 13
// times, 204,800 bytes, so a copy takes several calls at 64 KiB apiece.
func spliceToData() string {
	s := "splice me through a pipe\n"
	for k := 0; k < 13; k++ {
		s += s
	}
	return s
}

func spliceToSource(dir string, spliced bool) string {
	p := func(name string) string {
		if dir == "" {
			return name
		}
		return filepath.Join(dir, name)
	}
	want := 0
	if spliced {
		want = 1
	}
	return fmt.Sprintf(`// copy moves r to w: 1 when some bytes went by splice, 0 when every byte
// was read and written, negative on a failure.
function copy(r: Reader, w: Writer): i32 {
    let spliced: i32 = 0;
    let more: boolean = true;
    while (more) {
        match (r.splice_to(w, 65536)) {
            Ok(n) => {
                if (n == 0i64) { return spliced; }
                spliced = 1;
            },
            Err(e) => {
                match (e) {
                    Unsupported => { more = false; },
                    _ => { return 0 - 1; }
                }
            }
        }
    }
    while (true) {
        match (r.read_chunk(65536)) {
            Ok(c) => {
                if (c.len() == 0) { return spliced; }
                match (w.write(c)) { None => {}, Some(_) => { return 0 - 2; } }
            },
            Err(_) => { return 0 - 3; }
        }
    }
}

function main(): i32 {
    let data: string = "splice me through a pipe\n";
    let k: i32 = 0;
    while (k < 13) {
        data = data + data;
        k = k + 1;
    }
    match (write_file(%[1]q, data)) { Ok(_) => {}, Err(_) => { return 10; } }

    let r: Reader = match (open_reader(%[1]q)) { Ok(h) => h, Err(_) => { return 11; } };
    let w: Writer = match (open_writer(%[2]q)) { Ok(h) => h, Err(_) => { return 12; } };
    let how: i32 = copy(r, w);
    r.close();
    w.close();
    if (how < 0) { return 13; }
    if (how != %[3]d) { return 14; }

    let r2: Reader = match (open_reader(%[1]q)) { Ok(h) => h, Err(_) => { return 20; } };
    let a: Writer = match (open_appender(%[2]q)) { Ok(h) => h, Err(_) => { return 21; } };
    match (r2.splice_to(a, 4096)) {
        Ok(_) => { return 22; },
        Err(e) => {
            match (e) {
                Unsupported => {},
                _ => { return 23; }
            }
        }
    }
    match (r2.read_chunk(6)) {
        Ok(c) => { if (c != "splice") { return 24; } },
        Err(_) => { return 25; }
    }
    r2.close();
    a.close();

    let r3: Reader = match (open_reader(%[1]q)) { Ok(h) => h, Err(_) => { return 30; } };
    let how3: i32 = copy(r3, stdout());
    r3.close();
    if (how3 < 0) { return 31; }
    if (how3 != %[3]d) { return 32; }
    return 0;
}
`, p("data.txt"), p("copy.txt"), want)
}

// spliceToCheckTree reads the file-to-file copy back through Go's read, so a
// broken Fern reader cannot make the probe agree with itself.
func spliceToCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "copy.txt"))
	if err != nil {
		t.Fatalf("read copy.txt: %v", err)
	}
	if string(got) != spliceToData() {
		t.Errorf("copy.txt is %d bytes, want the %d the program wrote", len(got), len(spliceToData()))
	}
}

// spliceToCheckStdout checks the third copy arrived on stdout whole.
func spliceToCheckStdout(t *testing.T, out string) {
	t.Helper()
	if !strings.HasPrefix(out, spliceToData()) {
		t.Errorf("stdout holds %d bytes that are not the %d-byte file", len(out), len(spliceToData()))
	}
}

func TestX86_64SpliceTo(t *testing.T) {
	dir := t.TempDir()
	code, _ := compileRunX86_64WithSetup(t, spliceToSource(dir, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see spliceToSource)", code)
	}
	spliceToCheckTree(t, dir)
}

func TestArm64SpliceTo(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, spliceToSource(dir, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see spliceToSource)", code)
	}
	spliceToCheckTree(t, dir)
	spliceToCheckStdout(t, out)
}

// The interpreter's stdio need not be a descriptor, so it refuses every call
// and the fallback carries the bytes.
func TestInterpSpliceToUnsupported(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, spliceToSource(dir, false)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see spliceToSource)", code)
	}
	spliceToCheckTree(t, dir)
}

func TestWASMPreview1SpliceToUnsupported(t *testing.T) {
	mod := buildPreview1Module(t, spliceToSource("", false))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see spliceToSource)", got)
	}
	spliceToCheckTree(t, dir)
}

func TestWASMSpliceToUnsupported(t *testing.T) {
	p := buildWasmCore(t, spliceToSource("", false))
	dir := t.TempDir()
	stdout, stderr, ec := runWasmArtifact(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstderr:\n%s", ec, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see spliceToSource)\nstderr:\n%s", got, stderr)
	}
	spliceToCheckTree(t, dir)
}

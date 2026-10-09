// `r.seek(offset, 3)` and `r.seek(offset, 4)`: SEEK_DATA and SEEK_HOLE on
// every native target, Darwin included although its own numbers for the pair
// are the other way round. The probe writes a 4 MiB file whose only data is
// one byte at 1 MiB and records four answers: the first data, the first hole,
// the hole after the data, and SEEK_DATA from the end (ENXIO). Whether the
// filesystem keeps the holes is its own call, so the expected line is Go's
// lseek of the same file with the host's constants, which still tells a
// swapped or dropped whence apart on a filesystem without holes. Neither WASI
// preview has the pair: every answer there is EINVAL.
package e2e

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func seekHoleSource(dir string) string {
	p := func(name string) string {
		if dir == "" {
			return name
		}
		return filepath.Join(dir, name)
	}
	return fmt.Sprintf(`import "std/errno";
import "std/i32";
import "std/i64";

function show(r: Result[i64, IoError]): string {
    match (r) {
        Ok(n) => { return n.to_string(); },
        Err(e) => {
            if (errno.of(e) == errno.ENXIO) { return "ENXIO"; }
            if (errno.of(e) == errno.EINVAL) { return "EINVAL"; }
            return "E" + errno.of(e).to_string();
        }
    }
}

function main(): i32 {
    let w: Writer = match (open_writer(%[1]q)) { Ok(h) => h, Err(_) => { return 10; } };
    match (w.seek(1048576i64, 0)) { Ok(_) => {}, Err(_) => { return 11; } }
    match (w.write("x")) { None => {}, Some(_) => { return 12; } }
    match (w.truncate(4194304i64)) { None => {}, Some(_) => { return 13; } }
    w.close();
    let r: Reader = match (open_reader(%[1]q)) { Ok(h) => h, Err(_) => { return 20; } };
    let from: i64 = 0i64;
    match (r.seek(0i64, 3)) { Ok(d) => { from = d; }, Err(_) => {} }
    let line: string = show(r.seek(0i64, 3)) + " " + show(r.seek(0i64, 4)) + " " + show(r.seek(from, 4)) + " " + show(r.seek(4194304i64, 3));
    r.close();
    match (write_file(%[2]q, line)) { Ok(_) => {}, Err(_) => { return 30; } }
    return 0;
}
`, p("sparse.bin"), p("answers.txt"))
}

// seekHoleWant is the host's own answer for dir/sparse.bin.
func seekHoleWant(t *testing.T, dir string) string {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "sparse.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, hole := 3, 4
	if runtime.GOOS == "darwin" {
		data, hole = 4, 3
	}
	show := func(off int64, whence int) (int64, string) {
		n, err := syscall.Seek(int(f.Fd()), off, whence)
		switch {
		case err == nil:
			return n, strconv.FormatInt(n, 10)
		case errors.Is(err, syscall.ENXIO):
			return 0, "ENXIO"
		}
		t.Fatalf("host lseek(%d, %d): %v", off, whence, err)
		return 0, ""
	}
	from, first := show(0, data)
	_, h0 := show(0, hole)
	_, h1 := show(from, hole)
	_, end := show(4194304, data)
	return strings.Join([]string{first, h0, h1, end}, " ")
}

// seekHoleCheck reads the probe's answers back and checks the file it wrote.
func seekHoleCheck(t *testing.T, dir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "answers.txt"))
	if err != nil {
		t.Fatalf("answers: %v", err)
	}
	if string(got) != want {
		t.Errorf("SEEK_DATA / SEEK_HOLE answered %q, want %q", got, want)
	}
	b, err := os.ReadFile(filepath.Join(dir, "sparse.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 4194304 || b[1048576] != 'x' {
		t.Errorf("sparse.bin: %d bytes, want 4194304 with an x at 1 MiB", len(b))
	}
}

func TestX86_64SeekHole(t *testing.T) {
	code, dir := compileRunX86_64WithSetup(t, seekHoleSource(""), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (the code names the step)", code)
	}
	seekHoleCheck(t, dir, seekHoleWant(t, dir))
}

func TestArm64SeekHole(t *testing.T) {
	dir := t.TempDir()
	if _, code := compileAndRunArm64(t, seekHoleSource(dir)); code != 0 {
		t.Fatalf("exit = %d, want 0 (the code names the step)", code)
	}
	seekHoleCheck(t, dir, seekHoleWant(t, dir))
}

// Built everywhere, run on Apple Silicon, where the runtime swaps the pair.
func TestArm64DarwinSeekHole(t *testing.T) {
	dir := t.TempDir()
	if !buildAndRunDarwin(t, dir, seekHoleSource(dir)) {
		return
	}
	seekHoleCheck(t, dir, seekHoleWant(t, dir))
}

func TestInterpSeekHole(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, seekHoleSource(dir)); code != 0 {
		t.Fatalf("exit = %d, want 0 (the code names the step)", code)
	}
	seekHoleCheck(t, dir, seekHoleWant(t, dir))
}

const seekHoleWasmWant = "EINVAL EINVAL EINVAL EINVAL"

func TestWASMPreview1SeekHoleIsEINVAL(t *testing.T) {
	mod := buildWasmCore(t, seekHoleSource(""))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 (the code names the step)", got)
	}
	seekHoleCheck(t, dir, seekHoleWasmWant)
}

func TestWASMSeekHoleIsEINVAL(t *testing.T) {
	dir := t.TempDir()
	out := runResultStdout(t, seekHoleSource(""), runOpts{workDir: dir})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 (the code names the step)", got)
	}
	seekHoleCheck(t, dir, seekHoleWasmWant)
}

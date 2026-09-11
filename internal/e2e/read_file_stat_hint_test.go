package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// --- read_file / read_file_bytes vs st_size (#9065) ----------------
//
// The whole-file builtins used to size their buffer from fstat and
// report st_size as the length. A kernel pseudo-file generates its
// contents on the read, so st_size is 0 (/proc) or a page (/sys) and
// neither half held: every /proc file read back EMPTY and every /sys
// file as a page of mostly NUL. The interpreter goes through Go's
// os.ReadFile, which grows, so this was also a native/interp
// divergence no differential suite caught — they only ever read files
// the program itself had just written, where st_size is exact.
//
// The programs here are self-checking: each compares read_file
// against a Reader drain (open_reader + read_chunk, an independent
// path) and returns non-zero with a FAIL line on any disagreement.

// readFilePseudoProgram reads kernel pseudo-files — st_size 0, content
// generated on the read — and requires read_file / read_file_bytes to
// agree with the Reader drain and to end in real content rather than
// NUL padding.
const readFilePseudoProgram = `
import "std/i32";
import "std/array";

function drain(path: string): string {
    match (open_reader(path)) {
        Ok(r) => {
            var chunks: string[] = [];
            while (true) {
                match (r.read_chunk(4096)) {
                    Ok(piece) => {
                        if (piece.len() == 0) {
                            r.close();
                            return chunks.join("");
                        }
                        chunks = chunks.append(piece);
                    },
                    Err(_) => {
                        r.close();
                        return chunks.join("");
                    }
                }
            }
            return "";
        },
        Err(_) => { return ""; }
    }
}

function sum_bytes(s: string): i32 {
    var bs: u8[] = s.bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < bs.len()) {
        acc = acc + (bs[i] as i32) * (i % 7 + 1);
        i = i + 1;
    }
    return acc;
}

function fail(step: string): i32 {
    write("FAIL " + step + "\n");
    return 1;
}

function whole(path: string): string {
    return match (read_file(path)) { Ok(t) => t, Err(_) => "READ-ERR-SENTINEL" };
}

function readable(path: string): boolean {
    return match (read_file(path)) { Ok(_) => true, Err(_) => false };
}

function agrees(path: string): boolean {
    var want: string = drain(path);
    var got: string = whole(path);
    if (got.len() != want.len()) { return false; }
    if (sum_bytes(got) != sum_bytes(want)) { return false; }
    var n: i32 = match (read_file_bytes(path)) { Ok(b) => b.len(), Err(_) => 0 - 1 };
    return n == want.len();
}

function main(): i32 {
    // WASI resolves a path against a preopen, so the absolute form is
    // not openable there; fall back to the relative spelling.
    var mounts: string = "/proc/self/mounts";
    if (!readable(mounts)) { mounts = "proc/self/mounts"; }
    var cmdline: string = "/proc/self/cmdline";
    if (!readable(cmdline)) { cmdline = "proc/self/cmdline"; }

    if (!agrees(mounts)) { return fail("mounts-disagrees"); }
    if (!agrees(cmdline)) { return fail("cmdline-disagrees"); }

    var m: string = whole(mounts);
    if (m.len() == 0) { return fail("mounts-empty"); }
    var mb: u8[] = m.bytes();
    if (mb[mb.len() - 1] != 10 as u8) { return fail("mounts-nul-padded"); }

    write("pseudo-read-ok\n");
    return 0;
}
`

// readFileSizesProgram round-trips ordinary files across the sizes the
// buffer arithmetic turns on: empty, sub-word, and either side of the
// 4 KiB page the grow path floors at.
const readFileSizesProgram = `
import "std/i32";
import "std/array";

function sum_bytes(s: string): i32 {
    var bs: u8[] = s.bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < bs.len()) {
        acc = acc + (bs[i] as i32) * (i % 7 + 1);
        i = i + 1;
    }
    return acc;
}

function body(n: i32): string {
    var out: string = "0123456789abcdef".repeat(n / 16);
    return out + "z".repeat(n % 16);
}

function fail(step: string, n: i32): i32 {
    write("FAIL " + step + " n=" + n.to_string() + "\n");
    return 1;
}

function round_trips(n: i32): boolean {
    var want: string = body(n);
    if (want.len() != n) { return false; }
    match (write_file("size.bin", want)) { Ok(_) => {}, Err(_) => { return false; } }
    var got: string = match (read_file("size.bin")) { Ok(t) => t, Err(_) => "READ-ERR-SENTINEL" };
    if (got.len() != n) { return false; }
    if (sum_bytes(got) != sum_bytes(want)) { return false; }
    var bn: i32 = match (read_file_bytes("size.bin")) { Ok(b) => b.len(), Err(_) => 0 - 1 };
    return bn == n;
}

function main(): i32 {
    var sizes: i32[] = [0, 1, 15, 16, 4095, 4096, 4097, 8192, 100000];
    var i: i32 = 0;
    while (i < sizes.len()) {
        if (!round_trips(sizes[i])) { return fail("round-trip", sizes[i]); }
        i = i + 1;
    }
    write("sizes-ok\n");
    return 0;
}
`

// fifoBytes is four doublings past the 4 KiB floor the grow path
// starts at, so the buffer is re-allocated repeatedly and every copy
// has to carry the bytes already read.
const fifoBytes = 30000

// readFileFifoProgram reads a FIFO, whose st_size is 0 however many
// bytes the writer pushes through it.
var readFileFifoProgram = fmt.Sprintf(`
import "std/i32";

function main(): i32 {
    match (read_file("pipe")) {
        Ok(t) => {
            if (t.len() != %d) {
                write("FAIL fifo len=" + t.len().to_string() + "\n");
                return 1;
            }
            write("fifo-ok\n");
            return 0;
        },
        Err(_) => { write("FAIL fifo-err\n"); return 1; }
    }
}
`, fifoBytes)

func checkReadFileMarker(t *testing.T, marker, out string, code int) {
	t.Helper()
	if code != 0 || !strings.Contains(out, marker) {
		t.Fatalf("exit %d, output %q; want exit 0 and %s", code, out, marker)
	}
}

func TestX86_64ReadFileReadsPseudoFiles(t *testing.T) {
	out, code := compileNativeModloadInDir(t, "x86-64", readFilePseudoProgram)
	checkReadFileMarker(t, "pseudo-read-ok", out, code)
}

func TestArm64ReadFileReadsPseudoFiles(t *testing.T) {
	out, code := compileNativeModloadInDir(t, "arm64", readFilePseudoProgram)
	checkReadFileMarker(t, "pseudo-read-ok", out, code)
}

// A component gets one preopen, and runWasmInDir's is a seeded temp
// dir, so this runs the component directly with the root preopened —
// the only way the guest can name a path under /proc at all.
func TestWASMReadFileReadsPseudoFiles(t *testing.T) {
	stdout, stderr, _ := runComponent(t, buildComponent(t, readFilePseudoProgram), runOpts{workDir: "/"})
	if !strings.Contains(stdout, "pseudo-read-ok") {
		t.Fatalf("stdout %q stderr %q; want pseudo-read-ok", stdout, stderr)
	}
}

func TestInterpReadFileReadsPseudoFiles(t *testing.T) {
	if code := runInterpByte(t, readFilePseudoProgram); code != 0 {
		t.Fatalf("interp exit %d; want 0", code)
	}
}

func TestX86_64ReadFileRoundTripsEverySize(t *testing.T) {
	out, code := compileNativeModloadInDir(t, "x86-64", readFileSizesProgram)
	checkReadFileMarker(t, "sizes-ok", out, code)
}

func TestArm64ReadFileRoundTripsEverySize(t *testing.T) {
	out, code := compileNativeModloadInDir(t, "arm64", readFileSizesProgram)
	checkReadFileMarker(t, "sizes-ok", out, code)
}

func TestWASMReadFileRoundTripsEverySize(t *testing.T) {
	stdout, stderr, _, _ := runWasmInDir(t, readFileSizesProgram, nil)
	if !strings.Contains(stdout, "sizes-ok") {
		t.Fatalf("stdout %q stderr %q; want sizes-ok", stdout, stderr)
	}
}

func TestInterpReadFileRoundTripsEverySize(t *testing.T) {
	t.Chdir(t.TempDir())
	if code := runInterpByte(t, readFileSizesProgram); code != 0 {
		t.Fatalf("interp exit %d; want 0", code)
	}
}

// fifoDir makes a directory holding a FIFO named "pipe" and starts a
// writer that pushes fifoBytes through it and closes. Opening the
// write end blocks until the program under test opens the read end,
// so the writer runs in its own goroutine.
func fifoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	payload := strings.Repeat("fern-fifo-payload ", fifoBytes/18+1)[:fifoBytes]
	go func() {
		w, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer w.Close()
		_, _ = w.Write([]byte(payload))
	}()
	return dir
}

func TestX86_64ReadFileGrowsPastStatHint(t *testing.T) {
	out, code := compileNativeModloadInDirAt(t, "x86-64", readFileFifoProgram, fifoDir(t))
	checkReadFileMarker(t, "fifo-ok", out, code)
}

func TestArm64ReadFileGrowsPastStatHint(t *testing.T) {
	out, code := compileNativeModloadInDirAt(t, "arm64", readFileFifoProgram, fifoDir(t))
	checkReadFileMarker(t, "fifo-ok", out, code)
}

func TestInterpReadFileGrowsPastStatHint(t *testing.T) {
	t.Chdir(fifoDir(t))
	if code := runInterpByte(t, readFileFifoProgram); code != 0 {
		t.Fatalf("interp exit %d; want 0", code)
	}
}

// runArm64SSAProgram compiles src through `-backend ssa` and runs it
// in dir, returning its combined output and exit code. The SSA
// emitter carries its own read_file / read_file_bytes helpers, so it
// is a fourth implementation of the same contract.
func runArm64SSAProgram(t *testing.T, src, dir string) (string, int) {
	t.Helper()
	_, qemu := arm64Tooling(t)
	bin := compileArm64SSA(t, buildFernForArm64SSA(t), src, os.Environ())
	cmd := runArm64Bin(qemu, bin)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func TestArm64SSAReadFileReadsPseudoFiles(t *testing.T) {
	out, code := runArm64SSAProgram(t, readFilePseudoProgram, t.TempDir())
	checkReadFileMarker(t, "pseudo-read-ok", out, code)
}

func TestArm64SSAReadFileRoundTripsEverySize(t *testing.T) {
	out, code := runArm64SSAProgram(t, readFileSizesProgram, t.TempDir())
	checkReadFileMarker(t, "sizes-ok", out, code)
}

func TestArm64SSAReadFileGrowsPastStatHint(t *testing.T) {
	out, code := runArm64SSAProgram(t, readFileFifoProgram, fifoDir(t))
	checkReadFileMarker(t, "fifo-ok", out, code)
}

// readFileCensusProgram reads argv[1] n times. Run under
// FERN_LEAKCHECK, the difference between two counts is what one read
// of that path costs in allocations.
func readFileCensusProgram(n int) string {
	return fmt.Sprintf(`
function main(): i32 {
    var path: string = args()[1];
    var i: i32 = 0;
    var total: i32 = 0;
    while (i < %d) {
        total = total + match (read_file(path)) { Ok(t) => t.len(), Err(_) => 0 - 1 };
        i = i + 1;
    }
    if (total < 0) { return 1; }
    return 0;
}
`, n)
}

// censusRunner compiles the census program for backend with the leak
// census on and returns a runner that executes it in dir against one
// path, yielding the census's allocation count.
func censusRunner(t *testing.T, backend string, reads int) func(dir, path string) int64 {
	t.Helper()
	asm := emitLeakCheck(t, backend, readFileCensusProgram(reads), true)
	bindir := t.TempDir()
	asmPath := filepath.Join(bindir, "prog.s")
	binPath := filepath.Join(bindir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	var runner []string
	if backend == "arm64-linux" {
		gcc, qemu := arm64Tooling(t)
		if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		if qemu != "" {
			runner = []string{qemu}
		}
	} else {
		gcc, exec_ := x86_64Tooling(t)
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		runner = exec_
	}
	return func(dir, path string) int64 {
		t.Helper()
		argv := append(append(append([]string{}, runner...), binPath), path)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		_, stderr, code := runSplit(t, cmd)
		if code != 0 {
			t.Fatalf("census run %s: exit %d, stderr %q", path, code, stderr)
		}
		allocs, _, _ := parseLeakCheckLine(t, stderr)
		return allocs
	}
}

// TestReadFileRegularFileKeepsOneAllocation measures what a read costs
// rather than asserting a magic number: an ordinary file's per-read
// allocation count must not depend on its size — one buffer, sized
// from st_size, is enough — and must be strictly cheaper than a
// pseudo-file's, which pays for the grow and the re-fit a useless hint
// forces.
func TestReadFileRegularFileKeepsOneAllocation(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64-linux"} {
		t.Run(backend, func(t *testing.T) {
			one := censusRunner(t, backend, 1)
			many := censusRunner(t, backend, 101)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "empty.bin"), nil, 0o644); err != nil {
				t.Fatalf("write empty: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "big.bin"), []byte(strings.Repeat("x", 100000)), 0o644); err != nil {
				t.Fatalf("write big: %v", err)
			}
			perRead := func(path string) int64 {
				a, b := one(dir, path), many(dir, path)
				if (b-a)%100 != 0 {
					t.Fatalf("%s: allocation count is not per-iteration constant (1 read: %d, 101 reads: %d)", path, a, b)
				}
				return (b - a) / 100
			}
			empty, big := perRead("empty.bin"), perRead("big.bin")
			if empty != big {
				t.Errorf("a regular file's read cost depends on its size: %d allocations for an empty file, %d for 100 KB — "+
					"the st_size hint should size the buffer in one allocation either way", empty, big)
			}
			pseudo := perRead("/proc/self/mounts")
			if pseudo <= big {
				t.Errorf("reading /proc costs %d allocations and a regular file %d; the pseudo-file has to grow past its "+
					"zero hint, so a regular file matching it means the single-allocation fast path is gone", pseudo, big)
			}
		})
	}
}

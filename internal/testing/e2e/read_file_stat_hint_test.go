package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const readFilePseudoProgram = `
import "std/i32";
import "std/array";

function drain(path: string): string {
    match (open_reader(path)) {
        Ok(r) => {
            let chunks: string[] = [];
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
    let bs: u8[] = s.bytes();
    let i: i32 = 0;
    let acc: i32 = 0;
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
    let want: string = drain(path);
    let got: string = whole(path);
    if (got.len() != want.len()) { return false; }
    if (sum_bytes(got) != sum_bytes(want)) { return false; }
    let n: i32 = match (read_file_bytes(path)) { Ok(b) => b.len(), Err(_) => 0 - 1 };
    return n == want.len();
}

function main(): i32 {
    // WASI resolves a path against a preopen, so the absolute form is
    // not openable there; fall back to the relative spelling.
    let mounts: string = "/proc/self/mounts";
    if (!readable(mounts)) { mounts = "proc/self/mounts"; }
    let cmdline: string = "/proc/self/cmdline";
    if (!readable(cmdline)) { cmdline = "proc/self/cmdline"; }

    if (!agrees(mounts)) { return fail("mounts-disagrees"); }
    if (!agrees(cmdline)) { return fail("cmdline-disagrees"); }

    let m: string = whole(mounts);
    if (m.len() == 0) { return fail("mounts-empty"); }
    let mb: u8[] = m.bytes();
    if (mb[mb.len() - 1] != 10 as u8) { return fail("mounts-nul-padded"); }

    write("pseudo-read-ok\n");
    return 0;
}
`

const readFileSizesProgram = `
import "std/i32";
import "std/array";

function sum_bytes(s: string): i32 {
    let bs: u8[] = s.bytes();
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < bs.len()) {
        acc = acc + (bs[i] as i32) * (i % 7 + 1);
        i = i + 1;
    }
    return acc;
}

function body(n: i32): string {
    let out: string = "0123456789abcdef".repeat(n / 16);
    return out + "z".repeat(n % 16);
}

function fail(step: string, n: i32): i32 {
    write("FAIL " + step + " n=" + n.to_string() + "\n");
    return 1;
}

function round_trips(n: i32): boolean {
    let want: string = body(n);
    if (want.len() != n) { return false; }
    match (write_file("size.bin", want)) { Ok(_) => {}, Err(_) => { return false; } }
    let got: string = match (read_file("size.bin")) { Ok(t) => t, Err(_) => "READ-ERR-SENTINEL" };
    if (got.len() != n) { return false; }
    if (sum_bytes(got) != sum_bytes(want)) { return false; }
    let bn: i32 = match (read_file_bytes("size.bin")) { Ok(b) => b.len(), Err(_) => 0 - 1 };
    return bn == n;
}

function main(): i32 {
    let sizes: i32[] = [0, 1, 15, 16, 4095, 4096, 4097, 8192, 100000];
    let i: i32 = 0;
    while (i < sizes.len()) {
        if (!round_trips(sizes[i])) { return fail("round-trip", sizes[i]); }
        i = i + 1;
    }
    write("sizes-ok\n");
    return 0;
}
`

const fifoBytes = 30000

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
	out, code := compileRunInDir(t, "x86-64", readFilePseudoProgram)
	checkReadFileMarker(t, "pseudo-read-ok", out, code)
}

func TestArm64ReadFileReadsPseudoFiles(t *testing.T) {
	out, code := compileRunInDir(t, "arm64", readFilePseudoProgram)
	checkReadFileMarker(t, "pseudo-read-ok", out, code)
}

func TestWASMPreview1ReadFileReadsPseudoFiles(t *testing.T) {
	stdout, stderr, _ := runWasmArtifact(t, buildWasmCore(t, readFilePseudoProgram), runOpts{workDir: "/"})
	if !strings.Contains(stdout, "pseudo-read-ok") {
		t.Fatalf("stdout %q stderr %q; want pseudo-read-ok", stdout, stderr)
	}
}

func TestWASMReadFileReadsPseudoFiles(t *testing.T) {
	stdout, stderr, ec := runCLIComponent(t, readFilePseudoProgram, runOpts{workDir: "/"})
	if ec != 0 || !strings.Contains(stdout, "pseudo-read-ok") {
		t.Fatalf("exit %d, stdout %q stderr %q; want exit 0 and pseudo-read-ok", ec, stdout, stderr)
	}
}

func TestInterpReadFileReadsPseudoFiles(t *testing.T) {
	if code := runInterpByte(t, readFilePseudoProgram); code != 0 {
		t.Fatalf("interp exit %d; want 0", code)
	}
}

func TestX86_64ReadFileRoundTripsEverySize(t *testing.T) {
	out, code := compileRunInDir(t, "x86-64", readFileSizesProgram)
	checkReadFileMarker(t, "sizes-ok", out, code)
}

func TestArm64ReadFileRoundTripsEverySize(t *testing.T) {
	out, code := compileRunInDir(t, "arm64", readFileSizesProgram)
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
	out, code := compileRunInDirAt(t, "x86-64", readFileFifoProgram, fifoDir(t))
	checkReadFileMarker(t, "fifo-ok", out, code)
}

func TestArm64ReadFileGrowsPastStatHint(t *testing.T) {
	out, code := compileRunInDirAt(t, "arm64", readFileFifoProgram, fifoDir(t))
	checkReadFileMarker(t, "fifo-ok", out, code)
}

func TestInterpReadFileGrowsPastStatHint(t *testing.T) {
	t.Chdir(fifoDir(t))
	if code := runInterpByte(t, readFileFifoProgram); code != 0 {
		t.Fatalf("interp exit %d; want 0", code)
	}
}

func readFileCensusProgram(n int) string {
	return fmt.Sprintf(`
function main(): i32 {
    let path: string = args()[1];
    let i: i32 = 0;
    let total: i32 = 0;
    while (i < %d) {
        total = total + match (read_file(path)) { Ok(t) => t.len(), Err(_) => 0 - 1 };
        i = i + 1;
    }
    if (total < 0) { return 1; }
    return 0;
}
`, n)
}

func censusRunner(t *testing.T, backend string, reads int) func(dir, path string) int64 {
	t.Helper()
	target := e2eharness.TargetX86_64Linux
	if backend == "arm64-linux" {
		target = e2eharness.TargetArm64Linux
	}
	binPath := e2eharness.CompileSelfHostSource(t, target, readFileCensusProgram(reads), []string{"FERN_LEAKCHECK=1"})
	var run func(path string) *exec.Cmd
	if target == e2eharness.TargetArm64Linux {
		qemu := e2eharness.Arm64Runner(t)
		run = func(path string) *exec.Cmd { return runArm64Bin(qemu, binPath, path) }
	} else {
		runner := e2eharness.X86_64Runner(t)
		run = func(path string) *exec.Cmd { return runX86_64Bin(runner, binPath, path) }
	}
	return func(dir, path string) int64 {
		t.Helper()
		cmd := run(path)
		cmd.Dir = dir
		_, stderr, code := runSplit(t, cmd)
		if code != 0 {
			t.Fatalf("census run %s: exit %d, stderr %q", path, code, stderr)
		}
		allocs, _, _ := parseLeakCheckLine(t, stderr)
		return allocs
	}
}

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

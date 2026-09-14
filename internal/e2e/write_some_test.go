package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// `w.write_some(s)` is one write(2) and the COUNT it returned, where
// `w.write(s)` is the same call in a loop and can only report whether the
// whole string landed (#9231).
//
// What the count is FOR is a diagnostic nobody can reach from a test: GNU
// shred names the byte a failing write stopped at, and reaching that needs
// a full filesystem or a device at its end. What this source pins is the
// count on the paths that succeed — a regular file, an empty string, and
// stdout — and the bytes arriving in order, so a second write lands after
// the first rather than over it. Every write here is a WHOLE write, so
// none of it separates a count read out of the kernel's answer from one
// echoed back from the length asked for; writeSomePartialSource below is
// the case that does.
func writeSomeSource(path string) string {
	return fmt.Sprintf(`import "std/i64";

function main(): i32 {
    match (open_writer(%[1]q)) {
        Err(_) => { return 1; },
        Ok(w) => {
            match (w.write_some("hello world")) {
                Err(_) => { w.close(); return 2; },
                Ok(n) => { if (n != 11 as i64) { w.close(); return 3; } }
            }
            // Nothing asked for is nothing written, and not an error.
            match (w.write_some("")) {
                Err(_) => { w.close(); return 4; },
                Ok(n) => { if (n != 0 as i64) { w.close(); return 5; } }
            }
            // The offset moved with the bytes, so the second write lands
            // after the first rather than over it.
            match (w.write_some("!")) {
                Err(_) => { w.close(); return 6; },
                Ok(n) => { if (n != 1 as i64) { w.close(); return 7; } }
            }
            w.close();
        }
    }
    match (read_file(%[1]q)) {
        Err(_) => { return 8; },
        Ok(s) => { if (s != "hello world!") { return 9; } }
    }
    // A stdio Writer answers the same way; the bytes go to the test's
    // stdout, which is a pipe.
    match (stdout().write_some("ok\n")) {
        Err(_) => { return 10; },
        Ok(n) => { if (n != 3 as i64) { return 11; } }
    }
    return 0;
}
`, path)
}

// writeSomeTarget is the name the source writes, in a directory of its own
// so the wasm runner can mount it.
func writeSomeTarget(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "out.txt")
}

// writeSomePartialSource asks for the one answer a helper echoing back the
// length it was handed cannot give: a count SMALLER than the string. A pipe
// whose reader never drains takes what fits in the kernel's buffer and
// reports that, and the non-blocking open is what makes the write answer
// rather than wait for room.
//
// Not a wasm case. Preview 2 has no spelling for the non-blocking bit at
// all (docs/FREESTANDING-CORE.md) and neither preview can make a FIFO, so
// the partial answer is a native and interpreter fact.
func writeSomePartialSource(fifo string) string {
	return fmt.Sprintf(`import "std/i64";

function main(): i32 {
    // A megabyte, which is far past Linux's 64 KiB default pipe buffer and
    // past any grown one a caller is likely to have set.
    var big: string = "x";
    var i: i32 = 0;
    while (i < 20) {
        big = big + big;
        i = i + 1;
    }
    // 2 is the non-blocking bit of the open_*_with flags word; the FIFO
    // exists already, so nothing asks for CREATE.
    match (open_writer_with(%[1]q, 2)) {
        Err(_) => { return 1; },
        Ok(w) => {
            match (w.write_some(big)) {
                Err(_) => { w.close(); return 2; },
                Ok(n) => {
                    // Zero would be the standstill a full pipe gives, and
                    // the whole length would be an echo of what was asked.
                    if (n <= 0 as i64) { w.close(); return 3; }
                    if (n >= big.len() as i64) { w.close(); return 4; }
                }
            }
            w.close();
        }
    }
    return 0;
}
`, fifo)
}

// writeSomeFifo makes a FIFO and holds a READER open for the test's
// lifetime. Without one the writer's non-blocking open is ENXIO; with one
// that never reads, the buffer fills and the write can only be partial.
func writeSomeFifo(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	return p
}

func TestX86_64WriteSomePartial(t *testing.T) {
	bin, runner := compileX86_64Bin(t, writeSomePartialSource(writeSomeFifo(t)))
	out, code := runWithPipes(t, runX86_64Bin(runner, bin))
	if code != 0 {
		t.Errorf("exit = %d, want 0 (1=open, 2=Err, 3=count 0, 4=count is the whole length)\n%s", code, out)
	}
}

func TestArm64WriteSomePartial(t *testing.T) {
	bin, qemu := compileArm64Bin(t, writeSomePartialSource(writeSomeFifo(t)))
	out, code := runWithPipes(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Errorf("exit = %d, want 0 (1=open, 2=Err, 3=count 0, 4=count is the whole length)\n%s", code, out)
	}
}

func TestInterpWriteSomePartial(t *testing.T) {
	src := writeSomePartialSource(writeSomeFifo(t))
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runWithPipes(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
	if code != 0 {
		t.Errorf("exit = %d, want 0 (1=open, 2=Err, 3=count 0, 4=count is the whole length)\n%s", code, out)
	}
}

func TestX86_64WriteSome(t *testing.T) {
	bin, runner := compileX86_64Bin(t, writeSomeSource(writeSomeTarget(t)))
	out, code := runWithPipes(t, runX86_64Bin(runner, bin))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see writeSomeSource)\n%s", code, out)
	}
}

func TestArm64WriteSome(t *testing.T) {
	bin, qemu := compileArm64Bin(t, writeSomeSource(writeSomeTarget(t)))
	out, code := runWithPipes(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see writeSomeSource)\n%s", code, out)
	}
}

func TestInterpWriteSome(t *testing.T) {
	src := writeSomeSource(writeSomeTarget(t))
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runWithPipes(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see writeSomeSource)\n%s", code, out)
	}
}

// The preview-2 half, where a Writer holds an output STREAM rather than a
// descriptor: `blocking-write-and-flush` takes the whole chunk it is
// handed, so the count is the chunk's length and the file still ends up
// with both writes in order.
func TestWASMWriteSome(t *testing.T) {
	stdout, stderr, ec, _ := runWasmInDirOpts(t, writeSomeSource("out.txt"),
		map[string]string{"seed.txt": ""}, runOpts{stdin: ""})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Errorf("main = %d, want 0 — the code names the case (see writeSomeSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
}

// The preview-1 half reaches the fd_write body. It writes into the
// module's own working directory, which the wasmbin runner does not
// mount, so this one asks only about a STDIO descriptor — and fd 2
// rather than fd 1, because the runner reads main's answer off stdout.
const writeSomePreview1Source = `import "std/i64";

function main(): i32 {
    match (stderr().write_some("ok\n")) {
        Err(_) => { return 1; },
        Ok(n) => { if (n != 3 as i64) { return 2; } }
    }
    match (stderr().write_some("")) {
        Err(_) => { return 3; },
        Ok(n) => { if (n != 0 as i64) { return 4; } }
    }
    return 0;
}
`

func TestWASMPreview1WriteSome(t *testing.T) {
	if code := compileAndRunWasmbinMain(t, writeSomePreview1Source); code != 0 {
		t.Errorf("preview-1 write_some: main = %d, want 0 (1/3=Err, 2=count not 3, 4=empty count not 0)", code)
	}
}

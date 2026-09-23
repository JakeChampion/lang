package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// selfHostSpliceToSource drives `r.splice_to(w, max)` through the self-host's
// lowering: the `reader_splice` op and the Fern runtime leaf behind it, which
// is splice(2) on the two Linux targets, through the pipe __raw_splice_pipe()
// holds when neither handle is one, and Unsupported on wasm.
//
// The program copies with the fallback a caller writes — splice until
// Unsupported, then read_chunk and write from the same offset — and checks
// that an appending writer is refused without the reader losing a byte.
// `spliced` is whether the target must have moved bytes itself, so a leaf that
// always refused would fail on Linux rather than pass through the fallback.
// Each failure returns its own exit code.
func selfHostSpliceToSource(dir string, spliced bool) string {
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
	return fmt.Sprintf(`function copy(r: Reader, w: Writer): i32 {
    var spliced: i32 = 0;
    var more: boolean = true;
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
    return spliced;
}

function main(): i32 {
    var data: string = "splice me through a pipe\n";
    var k: i32 = 0;
    while (k < 13) {
        data = data + data;
        k = k + 1;
    }
    match (write_file(%[1]q, data)) { Ok(_) => {}, Err(_) => { return 10; } }
    match (open_reader(%[1]q)) {
        Err(_) => { return 11; },
        Ok(r) => {
            match (open_writer(%[2]q)) {
                Err(_) => { return 12; },
                Ok(w) => {
                    var how: i32 = copy(r, w);
                    w.close();
                    if (how < 0) { return 13; }
                    if (how != %[3]d) { return 14; }
                }
            }
            r.close();
        }
    }
    match (read_file(%[2]q)) {
        Ok(c) => { if (c != data) { return 15; } },
        Err(_) => { return 16; }
    }
    match (open_reader(%[1]q)) {
        Err(_) => { return 20; },
        Ok(r2) => {
            match (open_appender(%[2]q)) {
                Err(_) => { return 21; },
                Ok(a) => {
                    match (r2.splice_to(a, 4096)) {
                        Ok(_) => { return 22; },
                        Err(e) => {
                            match (e) {
                                Unsupported => {},
                                _ => { return 23; }
                            }
                        }
                    }
                    a.close();
                }
            }
            match (r2.read_chunk(6)) {
                Ok(c) => { if (c != "splice") { return 24; } },
                Err(_) => { return 25; }
            }
            r2.close();
        }
    }
    return 0;
}
`, p("data.txt"), p("copy.txt"), want)
}

// TestSelfHostSpliceToIR is the x86-64 leg: the op lowers to a call into the
// Fern-compiled __fern_reader_splice with its three operands re-pushed in
// parameter order, as reader_seek's are.
func TestSelfHostSpliceToIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("splice_to test runs only natively (opens host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(selfHostSpliceToSource(dir, true)), "-ir")
	if !bytes.Contains(asm, []byte("call __fn___fern_reader_splice")) {
		t.Fatal("asm has no `call __fn___fern_reader_splice`: the op did not reach the runtime leaf")
	}
	progBin := buildBin(t, gcc, dir, "splice_prog", string(asm))
	stdout, code := runPiped(t, exec.Command(progBin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostSpliceToSource)\n%s", code, stdout)
	}
}

// TestSelfHostSpliceToArm64IR is the arm64 leg, through asm_ir_run -target
// arm64-linux under qemu, whose syscall translation passes splice through.
func TestSelfHostSpliceToArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("splice_to test runs only natively (opens host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(selfHostSpliceToSource(dir, true)), "-target", "arm64-linux", "-ir")
	if !bytes.Contains(asm, []byte("bl __fn___fern_reader_splice")) {
		t.Fatal("asm has no `bl __fn___fern_reader_splice`: the op did not reach the runtime leaf")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "splice_prog_arm64", string(asm))
	stdout, code := runPiped(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostSpliceToSource)\n%s", code, stdout)
	}
}

// TestSelfHostSpliceToWasmIRUnsupported is the preview-1 leg, where every call
// is the refusal and the fallback carries the bytes.
func TestSelfHostSpliceToWasmIRUnsupported(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host splice_to wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	wat := runCapture(t, gcc, runner, driverBin, []byte(selfHostSpliceToSource("", false)), "-ir")
	if !bytes.Contains(wat, []byte("call $__fern_reader_splice")) {
		t.Fatal("wat has no `call $__fern_reader_splice`: the op did not reach the runtime leaf")
	}
	watFile := filepath.Join(dir, "splice_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wasm program exited %d, want 0 — the code names the case (see selfHostSpliceToSource)", code)
	}
}

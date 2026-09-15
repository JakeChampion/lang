package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// selfHostDupOntoSource drives `r.dup_onto(fd)` / `w.dup_onto(fd)` through the
// self-host's lowering: the `fd_dup_onto` op and the Fern runtime leaf behind
// it, which is dup3(2) on the two Linux targets and dup2(2) on Darwin.
//
// One op for both handle types, because a Reader and a Writer are both their
// fd there. Each failure returns its own exit code; the assertion that the
// descriptor TABLE moved, rather than that an errno classified, is the last
// one — `print` after the redirect has to land in the file.
//
// 7 is above the three standard descriptors and nothing these runners hold,
// so the success case is a real duplication rather than a replacement.
func selfHostDupOntoSource(data string, out string) string {
	return fmt.Sprintf(`function main(): i32 {
    match (write_file(%[1]q, "hello\n")) { Ok(_) => {}, Err(_) => { return 10; } }
    match (open_reader(%[1]q)) {
        Err(_) => { return 14; },
        Ok(r) => {
            match (r.dup_onto(7))    { None => {}, Some(_) => { return 11; } }
            match (r.dup_onto(0 - 1)) { None => { return 12; }, Some(_) => {} }
            r.close();
            match (r.dup_onto(7))    { None => { return 13; }, Some(_) => {} }
        }
    }
    match (open_writer(%[2]q)) {
        Err(_) => { return 22; },
        Ok(w) => {
            match (w.dup_onto(1))    { None => {}, Some(_) => { return 21; } }
            print("redirected");
            w.close();
        }
    }
    return 0;
}
`, data, out)
}

// selfHostDupOntoCheck reads the redirected line back off disk. Closing the
// handle after the print leaves fd 1 alone — dup2 semantics, and what `nohup`
// relies on — so the file holds the line either way.
func selfHostDupOntoCheck(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	if string(got) != "redirected\n" {
		t.Errorf("%s = %q, want %q — fd 1 was not replaced", filepath.Base(path), got, "redirected\n")
	}
}

// TestSelfHostDupOntoIR is the x86-64 IR leg: the op lowers to a call into
// the Fern-compiled __fern_fd_dup_onto, which swaps its two stack operands
// into parameter order the way writer_truncate does — so a lowering that
// skipped the swap asks dup3 to duplicate a descriptor nobody opened.
func TestSelfHostDupOntoIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("dup_onto test runs only natively (opens host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	out := filepath.Join(dir, "out.txt")
	src := selfHostDupOntoSource(filepath.Join(dir, "data.txt"), out)
	asm := runCapture(t, gcc, runner, driverBin, []byte(src), "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	if !bytes.Contains(asm, []byte("call __fn___fern_fd_dup_onto")) {
		t.Error("asm has no `call __fn___fern_fd_dup_onto`: the op did not reach the runtime leaf")
	}
	progBin := buildBin(t, gcc, dir, "dup_onto_prog", string(asm))
	stdout, code := runPiped(t, exec.Command(progBin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostDupOntoSource)\n%s", code, stdout)
	}
	selfHostDupOntoCheck(t, out)
}

// TestSelfHostDupOntoArm64IR is the arm64 leg of the same programme, through
// asm_ir_run -target arm64-linux under qemu.
func TestSelfHostDupOntoArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("dup_onto test runs only natively (opens host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	out := filepath.Join(dir, "out.txt")
	src := selfHostDupOntoSource(filepath.Join(dir, "data.txt"), out)
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux", "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_fd_dup_onto")) {
		t.Error("asm has no `bl __fn___fern_fd_dup_onto`: the op did not reach the runtime leaf")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "dup_onto_prog_arm64", string(asm))
	stdout, code := runPiped(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostDupOntoSource)\n%s", code, stdout)
	}
	selfHostDupOntoCheck(t, out)
}

// TestSelfHostDupOntoWasmIRUnsupported is the preview-1 leg, where the answer
// is the refusal: no WASI preview can install a descriptor at a chosen
// number, so the self-host's wasm emitter backs the op with a body that
// drops both operands and reports Unsupported. Every arm flips, and the
// closed-handle case is left out — the point here is that none of them
// silently succeeds.
func TestSelfHostDupOntoWasmIRUnsupported(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dup_onto wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := `function main(): i32 {
    match (write_file("data.txt", "hello\n")) { Ok(_) => {}, Err(_) => { return 10; } }
    match (open_reader("data.txt")) {
        Err(_) => { return 14; },
        Ok(r) => {
            match (r.dup_onto(7))    { None => { return 11; }, Some(_) => {} }
            match (r.dup_onto(0 - 1)) { None => { return 12; }, Some(_) => {} }
            r.close();
        }
    }
    match (open_writer("out.txt")) {
        Err(_) => { return 22; },
        Ok(w) => {
            match (w.dup_onto(1))    { None => { return 21; }, Some(_) => {} }
            w.close();
        }
    }
    return 0;
}
`
	wat := runCapture(t, gcc, runner, driverBin, []byte(src), "-ir")
	if len(wat) == 0 {
		t.Fatal("driver emitted no wat")
	}
	if !bytes.Contains(wat, []byte("call $__fern_fd_dup_onto")) {
		t.Error("wat has no `call $__fern_fd_dup_onto`: the op did not reach the runtime leaf")
	}
	watFile := filepath.Join(dir, "dup_onto_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wasm program exited %d, want 0 — the code names the case\n--- WAT ---\n%s", code, wat)
	}
}

package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostHandleMethodSource drives `Reader.stat()` / `Writer.stat()`,
// `Reader.seek(offset, whence)` and `Writer.truncate(length)` through the
// self-host's lowering: the `fd_stat`, `reader_seek` and `writer_truncate`
// ops, and the Fern runtime leaves behind them. Every failure returns its own
// exit code; the reads after each seek are the assertion that the position
// moved, and the stat after the resize is the assertion that the length did.
// `minus2` is bound first because the self-host's lower_expr has no `as i64`
// arm for a mixed-width subtraction.
func selfHostHandleMethodSource(path string, out string) string {
	return fmt.Sprintf(`function main(): i32 {
    var minus2: i64 = (0 as i64) - (2 as i64);
    match (open_reader(%[1]q)) {
        Err(_) => { return 30; },
        Ok(r) => {
            match (r.stat()) {
                Err(_) => { return 1; },
                Ok(st) => {
                    if (!st.is_file) { return 2; }
                    if (st.size != 5 as i64) { return 3; }
                }
            }
            match (r.seek(minus2, 2)) {
                Err(_) => { return 4; },
                Ok(pos) => { if (pos != 3 as i64) { return 5; } }
            }
            match (r.read_chunk(10)) {
                Err(_) => { return 6; },
                Ok(s) => { if (s != "lo") { return 7; } }
            }
            match (r.seek(0 as i64, 1)) {
                Err(_) => { return 8; },
                Ok(pos) => { if (pos != 5 as i64) { return 9; } }
            }
            match (r.seek(1 as i64, 0)) {
                Err(_) => { return 10; },
                Ok(pos) => { if (pos != 1 as i64) { return 11; } }
            }
            match (r.read_chunk(2)) {
                Err(_) => { return 12; },
                Ok(s) => { if (s != "el") { return 13; } }
            }
            r.close();
        }
    }
    match (stdin().seek(0 as i64, 1)) {
        Ok(_) => { return 15; },
        Err(e) => {
            match (e) {
                Other(_, msg) => { if (msg != "Illegal seek") { return 16; } },
                _ => { return 17; }
            }
        }
    }
    match (stdout().stat()) {
        Err(_) => { return 18; },
        Ok(st) => { if (st.is_file) { return 19; } if (st.is_dir) { return 20; } }
    }
    match (open_writer(%[2]q)) {
        Err(_) => { return 31; },
        Ok(w) => {
            match (w.write("0123456789")) { Some(_) => { return 21; }, None => {} }
            match (w.truncate(4 as i64)) { Some(_) => { return 22; }, None => {} }
            match (w.truncate(6 as i64)) { Some(_) => { return 23; }, None => {} }
            match (w.stat()) {
                Err(_) => { return 24; },
                Ok(st) => { if (st.size != 6 as i64) { return 25; } }
            }
            match (w.truncate((0 as i64) - (1 as i64))) {
                Some(_) => { },
                None => { return 26; }
            }
            w.close();
        }
    }
    match (read_file(%[2]q)) {
        Err(_) => { return 27; },
        Ok(s) => { if (s.len() != 6) { return 28; } }
    }
    return 0;
}
`, path, out)
}

func selfHostHandleProbeFile(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runPiped runs cmd with stdin and stdout both pipes, so a seek on stdin has
// to answer ESPIPE and stdout's stat describes something that is not a file.
func runPiped(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	cmd.Stdin = strings.NewReader("")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally (out=%q)", out.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

// TestSelfHostHandleMethodsIR is the x86-64 IR leg: the three ops lower to
// calls into the Fern-compiled __fern_fd_stat / __fern_reader_seek /
// __fern_writer_truncate, which share stat's record projection, lseek's
// 64-bit result and ftruncate's.
func TestSelfHostHandleMethodsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("handle stat/seek test runs only natively (opens host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	src := selfHostHandleMethodSource(selfHostHandleProbeFile(t, dir), filepath.Join(dir, "resized.txt"))
	asm := runCapture(t, gcc, runner, driverBin, []byte(src), "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	for _, sym := range []string{"call __fn___fern_fd_stat", "call __fn___fern_reader_seek", "call __fn___fern_writer_truncate"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Errorf("asm has no `%s`: the op did not reach the runtime leaf", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "handle_prog", string(asm))
	out, code := runPiped(t, exec.Command(progBin))
	if code != 0 {
		t.Errorf("program exited %d, want 0 — the code names the case (see selfHostHandleMethodSource)\n%s", code, out)
	}
}

// TestSelfHostHandleMethodsArm64IR is the arm64 leg of the same programme,
// through asm_ir_run -target arm64-linux under qemu.
func TestSelfHostHandleMethodsArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("handle stat/seek test runs only natively (opens host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	src := selfHostHandleMethodSource(selfHostHandleProbeFile(t, dir), filepath.Join(dir, "resized.txt"))
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux", "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	for _, sym := range []string{"bl __fn___fern_fd_stat", "bl __fn___fern_reader_seek", "bl __fn___fern_writer_truncate"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Errorf("asm has no `%s`: the op did not reach the runtime leaf", sym)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "handle_prog_arm64", string(asm))
	out, code := runPiped(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Errorf("program exited %d, want 0 — the code names the case (see selfHostHandleMethodSource)\n%s", code, out)
	}
}

// TestSelfHostHandleStatSeekWasmIR is the preview-1 leg: the self-host's wasm
// emitter backs the two ops with fd_filestat_get and fd_seek. wasmtime's
// preopen is the temp dir, so the file is named relative to it; stdin is
// /dev/null under `wasmtime run`, which lseek accepts, so the stdin case is
// the one assertion this leg does not make.
func TestSelfHostHandleStatSeekWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host handle stat/seek wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := `function main(): i32 {
    var minus2: i64 = (0 as i64) - (2 as i64);
    match (open_reader("hello.txt")) {
        Err(_) => { return 30; },
        Ok(r) => {
            match (r.stat()) {
                Err(_) => { return 1; },
                Ok(st) => {
                    if (!st.is_file) { return 2; }
                    if (st.size != 5 as i64) { return 3; }
                }
            }
            match (r.seek(minus2, 2)) {
                Err(_) => { return 4; },
                Ok(pos) => { if (pos != 3 as i64) { return 5; } }
            }
            match (r.read_chunk(10)) {
                Err(_) => { return 6; },
                Ok(s) => { if (s != "lo") { return 7; } }
            }
            match (r.seek(0 as i64, 1)) {
                Err(_) => { return 8; },
                Ok(pos) => { if (pos != 5 as i64) { return 9; } }
            }
            match (r.seek(1 as i64, 0)) {
                Err(_) => { return 10; },
                Ok(pos) => { if (pos != 1 as i64) { return 11; } }
            }
            match (r.read_chunk(2)) {
                Err(_) => { return 12; },
                Ok(s) => { if (s != "el") { return 13; } }
            }
            match (r.seek(minus2, 0)) {
                Ok(_) => { return 14; },
                Err(_) => {}
            }
            r.close();
        }
    }
    return 0;
}
`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "handle_prog.wat")
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

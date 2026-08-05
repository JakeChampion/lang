package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// readLineIRCases exercise the `read_line()` builtin through the IR path on
// x86-64, arm64, and wasm. read_line returns Option[string] — Some(line INCLUDING
// the trailing '\n') / None at EOF, matching native (#4369; was a bare newline-
// stripped string). The programs `match` on it: `wantExit` cases return the Some
// payload's len (newline now retained, so 5 not 4) or a code from the None arm and
// check the exit code. This pins all three fixed divergences (type shape, newline
// retention, EOF signaling) end-to-end.
//
// The MULTI-CALL cases at the bottom exist because every case above calls
// read_line exactly ONCE, and #6052 only appeared on the second call: the
// runtime issued a single read(0, buf, 256), boxed as far as the first '\n', and
// discarded the rest of the buffer, so the next read returned a genuine EOF.
// Five single-call cases across three backends all passed while
// `10\n15\n17\n` summed to 10 instead of 42. A one-shot case cannot see this
// class of bug no matter how many backends run it — the second call is the
// whole test. Note also that these must receive all their input at once (the
// harness hands cmd.Stdin a reader over the full string, which is what a shell
// pipe does): dribbled line-by-line, even the broken runtime passes.
var readLineIRCases = []struct {
	name, src, stdin, wantOut string
	wantExit                  int // used when wantOut == ""
}{
	{"len", `function main(): i32 { match (read_line()) { Some(s) => { return s.len(); }, None => { return 0; }, } }`, "abcd\n", "", 5},
	{"no-newline", `function main(): i32 { match (read_line()) { Some(s) => { return s.len(); }, None => { return 0; }, } }`, "bare", "", 4},
	{"empty-line", `function main(): i32 { match (read_line()) { Some(s) => { return s.len(); }, None => { return 9; }, } }`, "\nx", "", 1},
	{"eof-none", `function main(): i32 { match (read_line()) { Some(s) => { return 1; }, None => { return 7; }, } }`, "", "", 7},
	{"method", `function main(): i32 { var r: Reader = stdin(); match (r.read_line()) { Some(s) => { return s.len(); }, None => { return 0; }, } }`, "xy\n", "", 3},
	// Two consecutive calls: the minimal reproduction. Under the single-read
	// runtime the second call returned None and this exited 0.
	{"two-calls", `function main(): i32 { var a: i32 = 0; match (read_line()) { Some(s) => { a = s.len(); }, None => { a = 0; }, } match (read_line()) { Some(s) => { return a + s.len(); }, None => { return 0; }, } }`, "ab\ncd\n", "", 6},
	// Read-until-EOF, the multiline_stdin fixture's shape without the stdlib
	// (these programs compile with no stdlib root, so no trim/parse_int).
	// Counted 1 instead of 5 before the fix.
	{"count-until-eof", `function main(): i32 { var n: i32 = 0; var done: boolean = false; while (!done) { match (read_line()) { Some(s) => { n = n + 1; }, None => { done = true; }, } } return n; }`, "a\nb\nc\nd\ne\n", "", 5},
	// The same loop where the final line has NO trailing newline: it must still
	// come back as Some, so three lines in, three out.
	{"count-unterminated-tail", `function main(): i32 { var n: i32 = 0; var done: boolean = false; while (!done) { match (read_line()) { Some(s) => { n = n + 1; }, None => { done = true; }, } } return n; }`, "a\nb\nc", "", 3},
}

func TestSelfHostReadLineIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range readLineIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_read_line")) {
				t.Fatalf("%s: no call to __fern_read_line — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.stdin))
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			_ = cmd.Run()
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("%s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("%s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

// TestSelfHostReadLineIRWasm runs the same cases through the wasm IR backend
// under wasmtime, piping stdin in. read_line lowers on the wasm IR path: wasm_ir
// emits `call $__fern_read_line` and wasm_ir_run pulls in the fd_read import +
// the read_line_func helper (reads up to 256 bytes from fd 0, scans for '\n',
// boxes the line INCLUDING the '\n' as Some / None at EOF — Option[string], #4369).
func TestSelfHostReadLineIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host read_line wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range readLineIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_read_line")) {
				t.Fatalf("%s: no `call $__fern_read_line` — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "rl_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			run.Stdin = bytes.NewReader([]byte(tc.stdin))
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally:\n%s", tc.name, wat)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_line wasm IR %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_line wasm IR %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

func TestSelfHostReadLineIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range readLineIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_read_line")) {
				t.Fatalf("%s: no bl __fern_read_line — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "rl_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			run.Stdin = bytes.NewReader([]byte(tc.stdin))
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_line arm64 %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_line arm64 %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

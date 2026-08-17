package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// readIntIRCases exercise the `read_int()` builtin through the IR path on x86-64,
// arm64, and wasm. read_int lowers to a value IR op; on the register backends it
// calls __fn___fern_read_int, the mangled name of the Fern runtime helper
// (asmcore.rt_src_read_int, #2649). On wasm it reads fd 0 via fd_read into
// scratch and parses (allocating nothing — no heap runtime needed). The program
// returns the parsed int as its exit code; stdin supplies the digits.
var readIntIRCases = []struct {
	name, src, stdin string
	want             int
}{
	{"basic", `function main(): i32 { return read_int(); }`, "7", 7},
	{"two-digit", `function main(): i32 { return read_int(); }`, "42", 42},
	{"trailing-newline", `function main(): i32 { return read_int(); }`, "13\n", 13},
	{"arith", `function main(): i32 { var n: i32 = read_int(); return n + 1; }`, "9", 10},
}

// readIntIRDriverX86 builds the x86-64 IR driver the read_int cases compile with.
func readIntIRDriverX86(t *testing.T) (gcc string, runner []string, dir, driverBin string) {
	t.Helper()
	gcc, runner = x86_64Tooling(t)
	dir = writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	return gcc, runner, dir, buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
}

func TestSelfHostReadIntIRX86_64(t *testing.T) {
	gcc, runner, dir, driverBin := readIntIRDriverX86(t)
	for _, tc := range readIntIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fn___fern_read_int")) {
				t.Fatalf("%s: no call to __fn___fern_read_int — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.stdin))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostReadIntIRWasm runs the same cases through the wasm IR backend
// under wasmtime, piping stdin in. read_int now lowers on the wasm IR path:
// wasm_ir emits `call $__fern_read_int` and wasm_ir_run pulls in the fd_read
// import + the read_int_func helper (which reads stdin into the 16-byte scratch
// and parses, needing no heap runtime).
func TestSelfHostReadIntIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host read_int wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
	for _, tc := range readIntIRCases {
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
			if !bytes.Contains(wat, []byte("call $__fern_read_int")) {
				t.Fatalf("%s: no `call $__fern_read_int` — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "ri_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			run.Stdin = bytes.NewReader([]byte(tc.stdin))
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("read_int wasm IR %s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// readIntIRDriverArm64 builds the arm64 IR driver (an x86-64 binary emitting
// arm64 asm) the read_int cases compile with.
func readIntIRDriverArm64(t *testing.T) (arm64gcc, qemu string, x86runner []string, dir, driverBin string) {
	t.Helper()
	arm64gcc, qemu = arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir = t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern",
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
	return arm64gcc, qemu, x86runner, dir, buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
}

// runReadIntIRDriverArm64 compiles src to arm64 asm with the arm64 IR driver.
func runReadIntIRDriverArm64(t *testing.T, x86runner []string, driverBin, src string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed for %q: %v", src, err)
	}
	return asm
}

func TestSelfHostReadIntIRArm64(t *testing.T) {
	arm64gcc, qemu, x86runner, dir, driverBin := readIntIRDriverArm64(t)
	for _, tc := range readIntIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runReadIntIRDriverArm64(t, x86runner, driverBin, tc.src)
			if !bytes.Contains(asm, []byte("bl __fn___fern_read_int")) {
				t.Fatalf("%s: no bl __fn___fern_read_int — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ri_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			run.Stdin = bytes.NewReader([]byte(tc.stdin))
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("read_int arm64 IR %s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// readIntFailedReadProg pins that a FAILED read(2) parses as 0 rather than out of
// the dead stack frame below it. `dirty` leaves 0x39393939 — the bytes "9999" —
// in the frame read_int then reuses: the hand-asm helper took the raw negative
// read return as a length, so its end pointer sat behind the start of the buffer
// and the digit loop, which only advances, walked that garbage until a non-digit
// stopped it. It printed 9999.
const readIntFailedReadProg = `function dirty(a: i32, b: i32, c: i32, d: i32): i32 {
    var p: i32 = a; var q: i32 = b; var r: i32 = c; var s: i32 = d;
    var t: i32 = p + q; var u: i32 = r + s;
    return t + u;
}
function main(): i32 {
    var junk: i32 = dirty(960051513, 960051513, 960051513, 960051513);
    var n: i32 = read_int();
    print_str("read_int gave: ");
    print_int(n);
    putchar(10);
    return 0;
}
`

const readIntFailedReadWant = "read_int gave: 0"

// writeOnlyStdin opens a write-only file to hand the program as fd 0, so read(2)
// fails with -EBADF. /dev/null (cmd.Stdin = nil) returns 0 for EOF instead and
// never reaches the failure path.
func writeOnlyStdin(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "wo"), os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("open write-only stdin: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestSelfHostReadIntIRFailedReadX86_64(t *testing.T) {
	gcc, runner, dir, driverBin := readIntIRDriverX86(t)
	asm := runCapture(t, gcc, runner, driverBin, []byte(readIntFailedReadProg))
	progBin := buildBin(t, gcc, dir, "ri_failed_read", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	cmd.Stdin = writeOnlyStdin(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("program failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != readIntFailedReadWant {
		t.Errorf("read_int x86-64 IR failed read = %q, want %q", got, readIntFailedReadWant)
	}
}

func TestSelfHostReadIntIRFailedReadArm64(t *testing.T) {
	arm64gcc, qemu, x86runner, dir, driverBin := readIntIRDriverArm64(t)
	asm := runReadIntIRDriverArm64(t, x86runner, driverBin, readIntFailedReadProg)
	bin := buildBinArm64(t, arm64gcc, dir, "ri_failed_read", string(asm))
	run := runArm64Bin(qemu, bin)
	run.Stdin = writeOnlyStdin(t)
	out, err := run.Output()
	if err != nil {
		t.Fatalf("program failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != readIntFailedReadWant {
		t.Errorf("read_int arm64 IR failed read = %q, want %q", got, readIntFailedReadWant)
	}
}

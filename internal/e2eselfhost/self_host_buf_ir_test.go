package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// bufIRCases exercise the capacity-carrying string builder (#8773) on the IR
// path. Each builtin lowers to its own IR op rather than a call_direct, the
// way the strbuf ops beside them do, and every register backend emits it as a
// call into the same __fern_buf_* runtime the native compiler writes.
//
// The cases that matter beyond "it appends": a take leaves the builder usable,
// two builders accumulate independently (the property the singleton cannot
// have), a range push reads out of both a heap-form and an inline-form source,
// and growth past the reserved capacity reads a byte back from beyond the
// first buffer so a botched grow-copy corrupts the answer rather than hiding.
var bufIRCases = []struct {
	name, src, want string
}{
	{"build", `function main(): i32 { var b: usize = buf_new(16); buf_push(b, "ab"); buf_push(b, "cd"); write(buf_take(b)); buf_free(b); return 0; }`, "abcd"},
	{"empty-take", `function main(): i32 { var b: usize = buf_new(16); write(buf_take(b)); write("end"); buf_free(b); return 0; }`, "end"},
	{"reuse-after-take", `function main(): i32 { var b: usize = buf_new(16); buf_push(b, "x"); write(buf_take(b)); buf_push(b, "y"); write(buf_take(b)); buf_free(b); return 0; }`, "xy"},
	{"two-builders", `function main(): i32 { var a: usize = buf_new(16); var c: usize = buf_new(16); buf_push(a, "A"); buf_push(c, "B"); buf_push(a, "A"); write(buf_take(a)); write(buf_take(c)); buf_free(a); buf_free(c); return 0; }`, "AAB"},
	{"push-byte-and-range", `function main(): i32 { var b: usize = buf_new(16); buf_push_byte(b, 33); buf_push_range(b, "0123456789", 2, 5); buf_push_range(b, "xyz", 0, 2); write(buf_take(b)); buf_free(b); return 0; }`, "!234xy"},
	// take-len asserts through the exit code: buf_take's result is a string
	// box, so its .len() dispatches as a string, which is what says the
	// self-host's expr_is_str tracks the result.
	{"take-len", `function main(): i32 { var b: usize = buf_new(16); buf_push(b, "abcde"); var s: string = buf_take(b); buf_free(b); return s.len(); }`, ""},
	// GROWTH: 100 pushes of "xyz" = 300 bytes out of a 16-byte reserve, so the
	// buffer doubles several times. Reads s[250] ('y' = 121), well past every
	// intermediate buffer, so a dropped byte in a grow-copy shows up.
	{"grow-byte", `function main(): i32 { var b: usize = buf_new(16); var i: i32 = 0; while (i < 100) { buf_push(b, "xyz"); i = i + 1; } var s: string = buf_take(b); buf_free(b); return s[250] as i32; }`, ""},
	// The eight-byte push (#9221). It carries a value 64 bits WIDE, which is
	// the part the self-host has to get right beyond the store itself: wasm
	// types its stack, so an i32 reaching the helper's i64 parameter is a
	// module the host refuses rather than a truncation nobody sees. The
	// constants are printable ASCII, which is what makes the ORDER readable —
	// 0x4847464544434241 least-significant-byte-first is "ABCDEFGH".
	{"push-u64", `function main(): i32 { var b: usize = buf_new(16); buf_push_u64(b, 0x4847464544434241); buf_push(b, "-"); buf_push_u64(b, 0x5a59585756555453); write(buf_take(b)); buf_free(b); return 0; }`, "ABCDEFGH-STUVWXYZ"},
	// The value from ARITHMETIC rather than a literal, and a grow: 40 pushes
	// of eight bytes out of a 16-byte reserve. Answers with the length and the
	// last of the 320 bytes, so a short grow-copy shows up.
	{"push-u64-grow", `function main(): i32 { var b: usize = buf_new(16); var v: u64 = 0; var k: i32 = 0; while (k < 8) { v = v * 256 + 65; k = k + 1; } var i: i32 = 0; while (i < 40) { buf_push_u64(b, v); i = i + 1; } var s: string = buf_take(b); buf_free(b); return s.len() / 10 + (s[319] as i32); }`, ""},
}

// bufExpectedExit returns the want exit code for an exit-code-checked case, or
// -1 for a stdout-checked one.
func bufExpectedExit(name string) int {
	switch name {
	case "take-len":
		return 5
	case "grow-byte":
		return 121
	case "push-u64-grow":
		// 320 bytes, every one of them 'A'.
		return 32 + 65
	}
	return -1
}

// TestSelfHostBufIRX86_64 — the x86-64 self-host emitter lowers a builder
// program through the IR path and the resulting binary accumulates and emits
// the expected bytes.
func TestSelfHostBufIRX86_64(t *testing.T) {
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
	for _, tc := range bufIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_buf_take")) {
				t.Fatalf("%s: no call to __fern_buf_take — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "buf_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			out, _ := cmd.Output()
			if ex := bufExpectedExit(tc.name); ex >= 0 {
				if code := cmd.ProcessState.ExitCode(); code != ex {
					t.Errorf("%s: exit %d, want %d", tc.name, code, ex)
				}
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

// TestSelfHostBufIRArm64 — the aarch64 counterpart (CI-gated, qemu-aarch64).
func TestSelfHostBufIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range bufIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_buf_take")) {
				t.Fatalf("%s: no bl __fern_buf_take — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "buf_"+tc.name, string(asm))
			rc := runArm64Bin(qemu, bin)
			out, _ := rc.Output()
			if ex := bufExpectedExit(tc.name); ex >= 0 {
				if code := rc.ProcessState.ExitCode(); code != ex {
					t.Errorf("%s: exit %d, want %d", tc.name, code, ex)
				}
				return
			}
			if string(out) != tc.want {
				t.Errorf("buf arm64 IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

// TestSelfHostBufIRWasm — the wasm IR path, run under wasmtime.
func TestSelfHostBufIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host builder wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range bufIRCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_buf_take")) {
				t.Fatalf("%s: no call $__fern_buf_take — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "buf_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, _ := run.Output()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if ex := bufExpectedExit(tc.name); ex >= 0 {
				if code := run.ProcessState.ExitCode(); code != ex {
					t.Errorf("%s: exit %d, want %d", tc.name, code, ex)
				}
				return
			}
			if string(out) != tc.want {
				t.Errorf("buf wasm IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

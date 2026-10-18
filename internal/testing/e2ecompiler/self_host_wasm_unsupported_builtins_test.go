package e2ecompiler

import (
	"bytes"
	"strings"
	"testing"
)

// TestSelfHostWasmUnsupportedBuiltins pins the wasm ERROR ENDPOINTS: builtins
// with no wasm meaning at all, which both wasm drivers reject before emit with
// the CLI's target gate — E066 naming the builtin, non-zero exit, and NO WAT —
// rather than emitting a call against a symbol nothing defines.
//
//   - subprocess (#4320) — child-process spawning, unsupportable on wasm/WASI.
//   - timer_fd (#4317) — the native fd-based CLOCK_MONOTONIC timerfd. wasm has
//     no pollable file descriptors, and its analog already lowers:
//     wasm_timer_pollable, which returns a wasi:io/poll pollable. Emitted, a
//     call fails at load with the unhelpful
//     `unknown func: failed to find name $timer_fd`.
//   - __c_call<n> (#4375) — the C-FFI call primitive. wasm has no C ABI, so
//     there is no __c_call runtime on any wasm path.
//   - the raw-memory / syscall floor (#6946) — `rawmem` (__raw_alloc,
//     __raw_environ, …) and `syscall` (__syscall3 … __syscall6). They exist so
//     the register backends' runtime helpers can be written in Fern; wasm
//     serves those helpers with hand-written WAT and lowers none of them.
//
// Both stdin drivers, wasm_ir_run and wasm_run, are exercised.
func TestSelfHostWasmUnsupportedBuiltins(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern", "drivers/wasm_run.fern")
	irDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")
	prodDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	// run feeds src to a driver and returns (exit, stdout, stderr).
	run := func(bin, src string) (int, string, string) {
		cmd := runX86_64Bin(runner, bin)
		cmd.Stdin = strings.NewReader(src)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()
	}

	drivers := []struct {
		name string
		bin  string
	}{
		{"wasm_ir_run", irDriver},
		{"wasm_run", prodDriver},
	}
	rejected := []struct {
		name    string
		src     string
		mustSay string
	}{
		{
			name:    "subprocess",
			src:     `function main(): i32 { let r = subprocess("/bin/echo", [], ""); return r.exit_code; }` + "\n",
			mustSay: "subprocess",
		},
		{
			name:    "timer_fd",
			src:     `function main(): i32 { let fd: i32 = timer_fd(10); if (fd >= 0) { return 0; } return 1; }` + "\n",
			mustSay: "timer_fd",
		},
		{
			// FFI __c_call<n> (#4375) is written directly as a builtin call. Wasm
			// has no C ABI — no __c_call runtime on any wasm path — so both drivers
			// reject it on `cabi`.
			name:    "c_call",
			src:     `function main(): i32 { let cb: usize = 0; return __c_call0(cb) as i32; }` + "\n",
			mustSay: "__c_call0",
		},
		// One per shape of the raw floor (#6946): a syscall, an argument-taking
		// raw-memory op, and the zero-argument one — the last because an op with
		// no operands is the arity that keeps the wasm operand stack balanced
		// when it is dropped, so it is the one a silent miscompile hides in.
		{
			name:    "syscall3",
			src:     `function main(): i32 { return __syscall3(1, 1, 0, 0); }` + "\n",
			mustSay: "__syscall3",
		},
		{
			name:    "syscall6",
			src:     `function main(): i32 { return __syscall6(1, 1, 0, 0, 0, 0, 0); }` + "\n",
			mustSay: "__syscall6",
		},
		{
			name:    "raw_alloc",
			src:     `function main(): i32 { let p: i32 = __raw_alloc(64); return 0; }` + "\n",
			mustSay: "__raw_alloc",
		},
		{
			name:    "raw_environ",
			src:     `function main(): i32 { return __raw_environ(); }` + "\n",
			mustSay: "__raw_environ",
		},
		{
			name:    "raw_splice_pipe",
			src:     `function main(): i32 { return __raw_splice_pipe(); }` + "\n",
			mustSay: "__raw_splice_pipe",
		},
		{
			name:    "raw_cover",
			src:     `function main(): i32 { return __raw_cover(); }` + "\n",
			mustSay: "__raw_cover",
		},
		{
			name:    "raw_vg_request",
			src:     `function main(): i32 { let a: usize = __alloc(48); return __raw_vg_request(a) as i32; }` + "\n",
			mustSay: "__raw_vg_request",
		},
	}

	for _, d := range drivers {
		for _, tc := range rejected {
			t.Run(d.name+"/rejects_"+tc.name, func(t *testing.T) {
				code, out, errOut := run(d.bin, tc.src)
				if code == 0 {
					t.Fatalf("expected non-zero exit for a %s program, got 0 (stdout %d bytes)", tc.name, len(out))
				}
				if strings.TrimSpace(out) != "" {
					t.Errorf("expected NO WAT on stdout for a rejected %s program, got %d bytes", tc.name, len(out))
				}
				if !strings.Contains(errOut, "required by `"+tc.mustSay+"`") || !strings.Contains(errOut, "error[E066]") {
					t.Errorf("stderr %q is not E066 naming %s", errOut, tc.mustSay)
				}
			})
		}

		// The gate must not reject ordinary modules on either driver.
		t.Run(d.name+"/ordinary_module_still_emits", func(t *testing.T) {
			code, out, errOut := run(d.bin, `function main(): i32 { return 42; }`+"\n")
			if code != 0 {
				t.Fatalf("ordinary module rejected: exit %d, stderr %q", code, errOut)
			}
			if !strings.Contains(out, "(module") {
				t.Errorf("ordinary module emitted no WAT module (%d bytes)", len(out))
			}
		})
	}

	// timer_fd's diagnostic must point at the wasm analog — the whole reason it
	// is an error rather than a silent deferral is that there IS a right answer.
	t.Run("timer_fd_names_the_analog", func(t *testing.T) {
		_, _, errOut := run(prodDriver, rejected[1].src)
		if !strings.Contains(errOut, "wasm_timer_pollable") {
			t.Errorf("timer_fd diagnostic %q does not point at wasm_timer_pollable", errOut)
		}
	})
}

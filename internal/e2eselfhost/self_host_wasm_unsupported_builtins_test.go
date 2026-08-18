package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmUnsupportedBuiltins pins the wasm ERROR ENDPOINTS: builtins
// with no wasm meaning at all, which both wasm drivers must reject before emit
// with a clean diagnostic — non-zero exit, a message naming the feature on
// stderr, and NO WAT — rather than routing to the AST emitter, which has no
// runtime for them and emits a call against a symbol nothing defines.
//
//   - subprocess (#4320) — child-process spawning, unsupportable on wasm/WASI.
//   - timer_fd (#4317) — the native fd-based CLOCK_MONOTONIC timerfd. wasm has
//     no pollable file descriptors, and its analog already lowers:
//     wasm_timer_pollable, which returns a wasi:io/poll pollable. Before the
//     fix, timer_fd was merely DEFERRED to the AST path, so the program failed
//     at load with the unhelpful `unknown func: failed to find name $timer_fd`.
//   - __c_call<n> (#4375) — the C-FFI call primitive. wasm has no C ABI, so there
//     is no __c_call runtime on any wasm path; before this it deferred to the AST
//     emitter, which emitted a call against an undefined $__c_call<n>.
//   - the raw-memory / syscall floor (#6946) — __raw_alloc, __raw_store8,
//     __raw_load8, __raw_string, __raw_scratch, __raw_environ, __raw_addr,
//     __raw_arr_box, __syscall3, __syscall4, __syscall5. They exist so the
//     register backends' runtime helpers can be written in Fern; wasm has
//     neither a raw address space nor syscalls. Unclassified, they reached
//     instruction selection, which named an IR op nobody wrote (and, before
//     #6981, emitted the op as a WAT comment the assembler then choked on).
//
// Both drivers are exercised. The differential wasm_ir_run driver has rejected
// subprocess since #4320, but the PRODUCTION wasm_run driver had no such gate
// at all — so every one of these programs miscompiled through the path users
// actually take.
func TestSelfHostWasmUnsupportedBuiltins(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern",
		"wasm_ir_run.fern", "wasm_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	irDriver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	prodDriver := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// run feeds src to a driver and returns (exit, stdout, stderr). extraArgs
	// carries the differential driver's `-ir` flag; wasm_run takes none.
	run := func(bin, src string, extraArgs ...string) (int, string, string) {
		argv := append([]string{bin}, extraArgs...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), argv...)...)
		}
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
		args []string
	}{
		{"wasm_ir_run", irDriver, []string{"-ir"}},
		{"wasm_run", prodDriver, nil},
	}
	rejected := []struct {
		name    string
		src     string
		mustSay string
	}{
		{
			name:    "subprocess",
			src:     `function main(): i32 { var r = subprocess("/bin/echo", [], ""); return r.exit_code; }` + "\n",
			mustSay: "subprocess",
		},
		{
			name:    "timer_fd",
			src:     `function main(): i32 { var fd: i32 = timer_fd(10); if (fd >= 0) { return 0; } return 1; }` + "\n",
			mustSay: "timer_fd",
		},
		{
			// FFI __c_call<n> (#4375) is written directly as a builtin call, so its
			// callee ident is collected by wasm_unsupported_builtin. Wasm has no C
			// ABI — no __c_call runtime on any wasm path — so both drivers reject it
			// (before this it deferred to the AST emitter, which also emitted a call
			// against an undefined $__c_call0). asm_ir.is_c_call classifies the ident.
			name:    "c_call",
			src:     `function main(): i32 { var cb: usize = 0; return __c_call0(cb) as i32; }` + "\n",
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
			name:    "raw_alloc",
			src:     `function main(): i32 { var p: i32 = __raw_alloc(64); return 0; }` + "\n",
			mustSay: "__raw_alloc",
		},
		{
			name:    "raw_environ",
			src:     `function main(): i32 { return __raw_environ(); }` + "\n",
			mustSay: "__raw_environ",
		},
	}

	for _, d := range drivers {
		for _, tc := range rejected {
			t.Run(d.name+"/rejects_"+tc.name, func(t *testing.T) {
				code, out, errOut := run(d.bin, tc.src, d.args...)
				if code == 0 {
					t.Fatalf("expected non-zero exit for a %s program, got 0 (stdout %d bytes)", tc.name, len(out))
				}
				if strings.TrimSpace(out) != "" {
					t.Errorf("expected NO WAT on stdout for a rejected %s program, got %d bytes", tc.name, len(out))
				}
				if !strings.Contains(errOut, tc.mustSay) || !strings.Contains(errOut, "not supported") {
					t.Errorf("stderr %q does not mention %s / not supported", errOut, tc.mustSay)
				}
			})
		}

		// The gate must not reject ordinary modules on either driver.
		t.Run(d.name+"/ordinary_module_still_emits", func(t *testing.T) {
			code, out, errOut := run(d.bin, `function main(): i32 { return 42; }`+"\n", d.args...)
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

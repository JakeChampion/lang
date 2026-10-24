package e2ecompiler

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelfHostWasmUnsupportedBuiltins(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern", "drivers/wasm_run.fern")
	irDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")
	prodDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

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
			name:    "c_call",
			src:     `function main(): i32 { let cb: usize = 0; return __c_call0(cb) as i32; }` + "\n",
			mustSay: "__c_call0",
		},
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

	t.Run("timer_fd_names_the_analog", func(t *testing.T) {
		_, _, errOut := run(prodDriver, rejected[1].src)
		if !strings.Contains(errOut, "wasm_timer_pollable") {
			t.Errorf("timer_fd diagnostic %q does not point at wasm_timer_pollable", errOut)
		}
	})
}

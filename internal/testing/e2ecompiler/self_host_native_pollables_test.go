package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostNativePollables(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "pollables.fern")
	const program = `
@noinline
function mark(h: usize, value: i32): i32 { buf_push_byte(h, 65); return value; }
function main(): i32 {
 let h: usize = buf_new(8);
 let live: i32 = mark(h, 37);
 let fd: i32 = tcp_pollable(mark(h, -1));
 let low: i32 = tcp_pollable(mark(h, -2147483647 - 1));
 let high: i32 = tcp_pollable(mark(h, 2147483647));
 tcp_pollable(mark(h, 3));
 let count: i32 = buf_len(h);
 let text: string = buf_take(h);
 buf_free(h);
 if (fd != -1 || low != -2147483647 - 1 || high != 2147483647 || tcp_pollable(0) != 0) { return 91; }
 if (live != 37 || count != 5 || text.len() != 5) { return 93; }
 for b in text { if (b != 65 as u8) { return 94; } }
 return 0;
}`
	for _, c := range []struct{ builtin, call, hint string }{
		{"wasm_timer_pollable", "wasm_timer_pollable(5i64)", "use `timer_fd`, which this target provides"},
		{"wasm_pollable_drop", "wasm_pollable_drop(3)", ""},
		{"wasm_block", "wasm_block(3)", ""},
		{"wasm_poll", "wasm_poll([3])", ""},
	} {
		gated := filepath.Join(dir, c.builtin+".fern")
		body := "function main(): i32 { return " + c.call + "; }\n"
		if err := os.WriteFile(gated, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
			t.Run("refused/"+c.builtin+"/"+target, func(t *testing.T) {
				out, err := exec.Command(h.cli, "-target", target, "-o", filepath.Join(dir, "gated"), gated, h.stdlib).CombinedOutput()
				code := -1
				if ee, ok := err.(*exec.ExitError); ok {
					code = ee.ExitCode()
				}
				want := "does not provide `pollable`, required by `" + c.builtin + "`"
				if code != 1 || !strings.Contains(string(out), "E066") || !strings.Contains(string(out), want) {
					t.Fatalf("exit %d, want 1 with E066 %q:\n%s", code, want, out)
				}
				if c.hint != "" && !strings.Contains(string(out), c.hint) {
					t.Errorf("refusal does not suggest the native equivalent %q:\n%s", c.hint, out)
				}
			})
		}
	}
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		t.Run("emit/"+target, func(t *testing.T) {
			path := filepath.Join(dir, target+".s")
			if out, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", path, src, h.stdlib).CombinedOutput(); err != nil {
				t.Fatalf("emit: %v\n%s", err, out)
			}
			asm, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(asm), "__fern_tcp_pollable") {
				t.Errorf("retired native helper tcp_pollable remains")
			}
		})
	}
	for _, tg := range h.targets {
		for _, mode := range []string{"normal", "debug", "sanitize"} {
			t.Run(tg.target+"/"+mode, func(t *testing.T) {
				binary := filepath.Join(dir, tg.target+"-"+mode)
				args := []string{"-target", tg.target, "-o", binary, src, h.stdlib}
				if mode == "debug" {
					args = append([]string{"-g"}, args...)
				}
				compile := exec.Command(h.cli, args...)
				compile.Env = append(os.Environ(), "FERN_STRICT_IR=1")
				if mode == "sanitize" {
					compile.Env = append(compile.Env, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				}
				if out, err := compile.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				run := exec.Command(binary)
				if len(tg.runner) > 0 {
					run = exec.Command(tg.runner[0], append(tg.runner[1:], binary)...)
				}
				out, err := run.CombinedOutput()
				if err != nil {
					t.Fatalf("run: %v\n%s", err, out)
				}
				if mode == "sanitize" {
					assertBalancedCensus(t, string(out))
				}
			})
		}
	}
}

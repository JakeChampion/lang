package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Native readiness uses descriptors and poll timeouts. These five adapters
// must evaluate their arguments while returning their native identity/constant
// without emitting runtime helper bodies. WASI keeps its host operations.
func TestSelfHostNativePollableAdapters(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "pollables.fern")
	const program = `
@noinline
function mark(h: usize, value: i32): i32 { buf_push_byte(h, 65); return value; }
@noinline
function items(h: usize): i32[] { return [mark(h, 5)]; }
function main(): i32 {
 let h: usize = buf_new(8);
 let live: i32 = mark(h, 37);
 let fd: i32 = tcp_pollable(mark(h, -1));
 let low: i32 = tcp_pollable(mark(h, -2147483647 - 1));
 let high: i32 = tcp_pollable(mark(h, 2147483647));
 let deadline: i32 = wasm_timer_pollable((mark(h, 123) as i64) << 40);
 let dropped: i32 = wasm_pollable_drop(mark(h, 9));
 let blocked: i32 = wasm_block(mark(h, 9));
 let ready: i32 = wasm_poll(items(h));
 tcp_pollable(mark(h, 3));
 wasm_timer_pollable(mark(h, 4) as i64);
 wasm_pollable_drop(mark(h, 5));
 wasm_block(mark(h, 6));
 wasm_poll(items(h));
 let count: i32 = buf_len(h);
 let text: string = buf_take(h);
 buf_free(h);
 if (fd != -1 || low != -2147483647 - 1 || high != 2147483647 || tcp_pollable(0) != 0) { return 91; }
 if (deadline != -1 || ready != -1 || dropped != 0 || blocked != 0) { return 92; }
 if (live != 37 || count != 13 || text.len() != 13) { return 93; }
 for b in text { if (b != 65 as u8) { return 94; } }
 return 0;
}`
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
			for _, name := range []string{"tcp_pollable", "wasm_timer_pollable", "wasm_pollable_drop", "wasm_block", "wasm_poll"} {
				if strings.Contains(string(asm), "__fern_"+name) {
					t.Errorf("retired native helper %s remains", name)
				}
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

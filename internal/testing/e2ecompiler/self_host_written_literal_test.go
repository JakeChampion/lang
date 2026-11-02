package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A literal that a builtin writes in place is a heap box, directly and through
// a callee's parameter. A literal that a callee only reads stays a constant
// (#14013).
func TestSelfHostWrittenLiteralIsHeapX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)
	const watch = "@noinline function watch(r: i32, events: i32[]): i32 { return reactor_wait(r, events, 0); }\n"
	const relay = "@noinline function relay(r: i32, events: i32[]): i32 { return watch(r, events); }\n"
	const forward = "@noinline function forward(r: i32, events: i32[]): i32 { return relay(r, events); }\n"
	const peek = "@noinline function peek(events: i32[]): i32 { return events[0]; }\n"
	for _, tc := range []struct {
		name, src string
		heap      bool
	}{
		{"direct", "function main(): i32 { let r: i32 = reactor_new(); let e: i32[] = [0, 0, 0, 0]; return reactor_wait(r, e, 0) + e[0]; }", true},
		{"through-a-call", watch + "function main(): i32 { let r: i32 = reactor_new(); let e: i32[] = [0, 0, 0, 0]; return watch(r, e) + e[0]; }", true},
		{"through-two-calls", watch + relay + "function main(): i32 { let r: i32 = reactor_new(); let e: i32[] = [0, 0, 0, 0]; return relay(r, e) + e[0]; }", true},
		{"caller-first-chain", forward + relay + watch + "function main(): i32 { let r: i32 = reactor_new(); let e: i32[] = [0, 0, 0, 0]; return forward(r, e) + e[0]; }", true},
		{"leaf-first-chain", watch + relay + forward + "function main(): i32 { let r: i32 = reactor_new(); let e: i32[] = [0, 0, 0, 0]; return forward(r, e) + e[0]; }", true},
		{"ring-wait", "function main(): i32 { let e: i32[] = [0, 0, 0, 0]; let n = ring_wait(0 - 1 as i64, e, 0); return e[0]; }", true},
		{"tcp-address", "function main(): i32 { let e: u8[] = [0u8, 0u8, 0u8, 0u8]; let n = tcp_addr_into(0 - 1, false, e); return e[0] as i32; }", true},
		{"udp-data", "function main(): i32 { let e: u8[] = [0u8, 0u8, 0u8, 0u8]; let n = udp_recvfrom(0 - 1, e, __alloc_u8(19)); return e[0] as i32; }", true},
		{"udp-sender", "function main(): i32 { let e: u8[] = [0u8, 0u8, 0u8, 0u8]; let n = udp_recvfrom(0 - 1, __alloc_u8(4), e); return e[0] as i32; }", true},
		{"read-only-callee", peek + "function main(): i32 { let e: i32[] = [0, 0, 0, 0]; return peek(e); }", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src)
			body := selfHostFnBody(t, []byte(asm), "main")
			// Byte literals use a constant template and copy it into a fresh box.
			copied := strings.Contains(body, "call __fn___fern_arr_slice_u8")
			if got := strings.Contains(body, ".K0(%rip)") && !copied; got == tc.heap {
				t.Errorf("constant-pool load present=%v, want %v:\n%s", got, !tc.heap, body)
			}
			if got := strings.Contains(body, "call __fern_arr_box") || copied; got != tc.heap {
				t.Errorf("heap allocation present=%v, want %v:\n%s", got, tc.heap, body)
			}
			if code, out := cli.runX86(t, asm); code != 0 {
				t.Errorf("exit %d, want 0: %s", code, out)
			}
		})
	}
}

// The constant data is read-only in the image. A literal handed to
// reactor_wait, directly and through a callee, and a length write on the
// shared empty array must not touch it.
func TestSelfHostWrittenLiteralImage(t *testing.T) {
	cli := buildSelfHostCLI(t)
	if len(cli.runner) != 0 {
		t.Skip("requires native x86-64 Linux memory protection")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(`@noinline function watch(r: i32, events: i32[]): i32 { return reactor_wait(r, events, 0); }
function main(): i32 {
  let r: i32 = reactor_new();
  let a: i32[] = [0, 0, 0, 0];
  let b: i32[] = [0, 0, 0, 0];
  let empty: u8[] = __alloc_u8(0);
  unsafe { __arr_set_len(empty, 0); }
  return reactor_wait(r, a, 0) + watch(r, b) + empty.len() + a[0] + b[0];
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{nil, {"-pie"}} {
		name := strings.TrimPrefix(strings.Join(flags, "-"), "-")
		if name == "" {
			name = "plain"
		}
		t.Run(name, func(t *testing.T) {
			bin := filepath.Join(dir, name)
			cmd := cli.compileCmd("x86-64-linux", bin, src, flags...)
			cmd.Env = childEnv()
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, msg)
			}
			if err := os.Chmod(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			c := withoutCoreDump(runX86_64Bin(cli.runner, bin))
			out, _ := c.CombinedOutput()
			if c.ProcessState == nil || c.ProcessState.ExitCode() != 0 {
				t.Fatalf("want exit 0, got %v\n%s", c.ProcessState, out)
			}
		})
	}
}

package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// A builder op on the register path loads its operands straight into the
// argument registers and calls its helper (ssa_buf_call), rather than pushing
// them for the stack machine's arm to pop. `swap` receives the handle in %rsi
// and the string in %rdi, the reverse of what buf_push takes, so the pair is
// exchanged; `four` passes all four of buf_push_range's operands.
const bufCallProg = `@noinline function swap(x: i32, h: usize, s: string): i32 { buf_push(h, s); return x; }
@noinline function four(h: usize, s: string, a: i32, b: i32): void { buf_push_range(h, s, a, b); }
@noinline function one(h: usize, c: i32): void { buf_push_byte(h, c); }
function main(): i32 {
    var h: usize = buf_new(4);
    var k: i32 = swap(7, h, "ab" + "");
    four(h, "0123456789" + "", 2, 6);
    one(h, 90);
    var out: string = buf_take(h);
    if (out != "ab2345Z") { return 1; }
    return 35 + k;
}
`

func TestSelfHostBufCallDirect(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "bufcall.fern")
	if err := os.WriteFile(src, []byte(bufCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	stacked := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`popq %r[a-z0-9]+\n\s+call __fern_buf_|call __fern_buf_[a-z_]+\n\s+pushq %rax`),
		"arm64-linux":  regexp.MustCompile(`ldr x[0-3], \[sp\], #16\n\s+bl __fern_buf_|bl __fern_buf_[a-z_]+\n\s+str x0, \[sp, #-16\]!`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"swap", "four", "one"} {
			body := condBranchBody(t, string(asm), fn)
			if !regexp.MustCompile(`__fern_buf_`).MatchString(body) {
				t.Fatalf("%s: %s calls no builder helper:\n%s", target, fn, body)
			}
			if stacked[target].MatchString(body) {
				t.Errorf("%s: %s passes its builder operands through the stack:\n%s", target, fn, body)
			}
		}
		if target == "x86-64-linux" && !regexp.MustCompile(`xchgq %rdi, %rsi\n\s+call __fern_buf_push\n`).MatchString(condBranchBody(t, string(asm), "swap")) {
			t.Errorf("%s: swap does not exchange its crossed operands:\n%s", target, condBranchBody(t, string(asm), "swap"))
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42 (1: the builder's bytes)", tg.target, got)
		}
	}
}

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A constant index into an array or a string is an immediate (ssa.imm_use):
// the element is read or written at a displacement and the bounds test
// compares the length with the constant, so the index never takes a register.
// A closure call reads its body's address this way, out of slot 0.
const constIndexProg = `@noinline function third(xs: i64[]): i64 { return xs[2]; }
@noinline function second_byte(s: string): i32 { return s[1] as i32; }
@noinline function packed(bs: u8[]): i32 { return bs[1] as i32; }
@noinline function put(xs: i64[]): i64[] { return xs.with(1, 40i64); }
@noinline function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function inc(x: i32): i32 { return x + 1; }
function main(): i32 {
    let xs: i64[] = [5i64, 6i64, 7i64];
    let bs: u8[] = [3 as u8, 4 as u8];
    let ys: i64[] = put(xs);
    return (third(xs) + ys[1]) as i32 + second_byte("ab") + packed(bs) + apply(inc, 10);
}
`

// The same read past the end still traps.
const constIndexOOBProg = `@noinline function fifth(xs: i32[]): i32 { return xs[4]; }
function main(): i32 { return fifth([1, 2]); }
`

func TestSelfHostConstIndexImmediate(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "index.fern")
	if err := os.WriteFile(src, []byte(constIndexProg), 0o644); err != nil {
		t.Fatal(err)
	}
	oob := filepath.Join(dir, "oob.fern")
	if err := os.WriteFile(oob, []byte(constIndexOOBProg), 0o644); err != nil {
		t.Fatal(err)
	}
	// What each function's body must hold, and the indexed form it must not.
	want := map[string]map[string]string{
		"x86-64-linux": {"third": "24(%", "put": "16(%", "packed": "9(%", "second_byte": "1(%rdx)", "apply": "8(%"},
		"arm64-linux":  {"third": "#24]", "put": "#16]", "packed": "#9]", "second_byte": "#1]", "apply": "#8]"},
	}
	indexed := map[string]string{"x86-64-linux": ",8)", "arm64-linux": "lsl #3"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for fn, disp := range want[target] {
			body := condBranchBody(t, string(asm), fn)
			if !strings.Contains(body, disp) {
				t.Errorf("%s: %s does not address its element at %q:\n%s", target, fn, disp, body)
			}
			if strings.Contains(body, indexed[target]) {
				t.Errorf("%s: %s still indexes through a register:\n%s", target, fn, body)
			}
		}
	}
	for _, tg := range h.targets {
		for _, c := range []struct {
			src  string
			want int
		}{{src, 7 + 40 + 98 + 4 + 11}, {oob, 134}} {
			if c.want == 134 && strings.HasPrefix(tg.target, "wasm") {
				continue
			}
			bin := filepath.Join(dir, tg.target+filepath.Base(c.src)+".bin")
			if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, c.src, h.stdlib).CombinedOutput(); err != nil {
				t.Fatalf("%s: building %s: %v\n%s", tg.target, c.src, err, combined)
			}
			run := exec.Command(bin)
			if len(tg.runner) > 0 {
				run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
			}
			_ = run.Run()
			if got := run.ProcessState.ExitCode(); got != c.want {
				t.Errorf("%s: %s exits %d, want %d", tg.target, filepath.Base(c.src), got, c.want)
			}
		}
	}
}

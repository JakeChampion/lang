package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const closureRegCallProg = `@noinline function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 { return f(a, b); }
@noinline function seven(f: (i32, i32, i32, i32, i32, i32, i32) => i32): i32 { return f(1, 2, 3, 4, 5, 6, 7); }
function main(): i32 {
    let k: i32 = 3;
    let add = (x: i32, y: i32): i32 => x + y + k;
    let mul = (x: i32, y: i32): i32 => x * y;
    let s7 = (a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32): i32 => a + b * 2 + c * 3 + d * 4 + e * 5 + f * 6 + g * 7 + k;
    return apply(add, 4, 5) + apply(mul, 6, 7) + seven(s7);
}
`

const closureFloatCallProg = `function applyf(f: (f64, i32, f64) => f64, a: f64, n: i32, b: f64): f64 { return f(a, n, b); }
function many(f: (i32, f64, i32, i32, i32, i32, i32, f64, i32) => f64): f64 { return f(1, 2.5, 3, 4, 5, 6, 7, 0.25, 9); }
function main(): i32 {
    let k: f64 = 1.5;
    let g = (x: f64, n: i32, y: f64): f64 => x * (n as f64) + y + k;
    let h = (a: i32, b: f64, c: i32, d: i32, e: i32, f: i32, g2: i32, q: f64, r: i32): f64 => (a + c + d + e + f + g2 + r) as f64 + b * q + k;
    let r1: f64 = applyf(g, 2.0, 3, 0.5);
    let r2: f64 = many(h);
    return ((r1 * 10.0) as i32) + ((r2 * 100.0) as i32);
}
`

func TestSelfHostClosureCallInRegisters(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "clo.fern")
	if err := os.WriteFile(src, []byte(closureRegCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	emit := func(target string) string {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(asm)
	}
	asm := []byte(emit("x86-64-linux"))
	argPush := regexp.MustCompile(`\n\s+pushq [^\n]+(\n\s+\.cfi_def_cfa_offset)?`)
	pushes := func(body string) int {
		n := 0
		for _, m := range argPush.FindAllStringSubmatch(body, -1) {
			if m[1] == "" {
				n++
			}
		}
		return n
	}
	apply := condBranchBody(t, string(asm), "apply")
	if !strings.Contains(apply, "call *8(%rax)") || pushes(apply) != 0 {
		t.Errorf("apply: want a call through the environment in %%rax with no argument pushed:\n%s", apply)
	}
	sev := condBranchBody(t, string(asm), "seven")
	if !strings.Contains(sev, "call *8(%rax)") || pushes(sev) != 8 {
		t.Errorf("seven: want its eight arguments pushed for a call through the environment in %%rax:\n%s", sev)
	}
	if !regexp.MustCompile(`__fn_\S*clo\d+\.r\b`).Match(asm) {
		t.Errorf("no function value names a closure's .r entry")
	}
	arm := emit("arm64-linux")
	armPush := regexp.MustCompile(`(?s)\.Lssa_\w+_0:.*str x\d+, \[sp, #-16\]!`)
	for _, fn := range []string{"apply", "seven"} {
		if body := condBranchBody(t, arm, fn); !strings.Contains(body, "blr x6") || armPush.MatchString(body) {
			t.Errorf("arm64 %s: want a call through x6 with no argument pushed:\n%s", fn, body)
		}
	}
	if !regexp.MustCompile(`__fn_\S*clo\d+\.k\b`).MatchString(arm) {
		t.Errorf("arm64: no function value names a closure's .k entry")
	}
	dar := emit("arm64-darwin")
	if !regexp.MustCompile(`\bL__fn_\S*clo\d+\.k\b`).MatchString(dar) || regexp.MustCompile(`(^|[^L\w])__fn_\S*\.k\b`).MatchString(dar) {
		t.Errorf("arm64-darwin: a .k entry is not a local label")
	}
	for _, c := range []struct {
		name, src string
		want      int
	}{{"clo", closureRegCallProg, 197}, {"clof", closureFloatCallProg, 208}} {
		prog := filepath.Join(dir, c.name+".fern")
		if err := os.WriteFile(prog, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, tg := range h.targets {
			bin := filepath.Join(dir, c.name+"-"+tg.target+".bin")
			if combined, err := exec.Command(h.cli, "-target", tg.target, "-o", bin, prog, h.stdlib).CombinedOutput(); err != nil {
				t.Fatalf("%s %s: building: %v\n%s", c.name, tg.target, err, combined)
			}
			run := exec.Command(bin)
			if len(tg.runner) > 0 {
				run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
			}
			_ = run.Run()
			if got := run.ProcessState.ExitCode(); got != c.want {
				t.Errorf("%s %s: exit %d, want %d (the interpreter's)", c.name, tg.target, got, c.want)
			}
		}
	}
}

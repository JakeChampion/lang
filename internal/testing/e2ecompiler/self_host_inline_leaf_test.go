package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const inlineLeafProg = `function lo32(x: i64): i32 { return (x & 4294967295i64) as i32; }
@noinline function kept(x: i64): i32 { return (x & 4294967295i64) as i32; }
@noinline function sum(n: i64): i32 {
    let t: i32 = 0;
    let i: i64 = 0i64;
    while (i < n) { t = t + lo32(i * 3i64) + kept(i); i = i + 1i64; }
    return t;
}
function main(): i32 { return sum(10i64); }
`

func TestSelfHostInlineLeaf(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "leaf.fern")
	if err := os.WriteFile(src, []byte(inlineLeafProg), 0o644); err != nil {
		t.Fatal(err)
	}
	call := map[string]string{"x86-64-linux": "call __fn_", "arm64-linux": "bl __fn_"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		body := condBranchBody(t, string(asm), "sum")
		if strings.Contains(body, call[target]+"lo32") {
			t.Errorf("%s: sum still calls lo32:\n%s", target, body)
		}
		if !strings.Contains(body, call[target]+"kept") {
			t.Errorf("%s: sum inlined kept, which is @noinline:\n%s", target, body)
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
		if got := run.ProcessState.ExitCode(); got != 180 {
			t.Errorf("%s: exit %d, want 180 (the interpreter's)", tg.target, got)
		}
	}
}

const inlinePrimitiveLeafProg = `function red(h: u64, l: u64, n: u64): u64 {
    let t: u64 = __mulhi_u64(l, n);
    if (h >= t) { return h - t; }
    return h - t + n;
}
@noinline function sq(a: u64, n: u64): u64 { return red(__mulhi_u64(a, a), a * a, n); }
@noinline function mul(a: u64, b: u64, n: u64): u64 { return red(__mulhi_u64(a, b), a * b, n); }
function main(): i32 { return ((sq(123456789123u64, 1000003u64) + mul(987654321987u64, 55555555555u64, 1000003u64)) % 200u64) as i32; }
`

func TestSelfHostInlinePrimitiveLeaf(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "red.fern")
	if err := os.WriteFile(src, []byte(inlinePrimitiveLeafProg), 0o644); err != nil {
		t.Fatal(err)
	}
	call := map[string]string{"x86-64-linux": "call __fn_red", "arm64-linux": "bl __fn_red"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(asm), call[target]) {
			t.Errorf("%s: red is still called:\n%s", target, asm)
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
		if got := run.ProcessState.ExitCode(); got != 3 {
			t.Errorf("%s: exit %d, want 3 (the interpreter's)", tg.target, got)
		}
	}
}

const inlineShortCircuitLeafProg = `function is_digit(b: i32): boolean { return b >= 48 && b <= 57; }
function is_alpha(b: i32): boolean { return b >= 65 && b <= 90 || b >= 97 && b <= 122 || b == 95; }
function is_alphanumeric(b: i32): boolean { return is_digit(b) || is_alpha(b); }
@noinline function count(n: i32): i32 {
    let t: i32 = 0;
    let b: i32 = 0;
    while (b < n) { if (is_alphanumeric(b)) { t = t + 1; } b = b + 1; }
    return t;
}
@noinline function first_other(n: i32): i32 {
    let b: i32 = 0;
    while (b < n && is_alphanumeric(b)) { b = b + 1; }
    return b;
}
function main(): i32 { return count(128) + first_other(4); }
`

func TestSelfHostInlineShortCircuitLeaf(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "alnum.fern")
	if err := os.WriteFile(src, []byte(inlineShortCircuitLeafProg), 0o644); err != nil {
		t.Fatal(err)
	}
	call := map[string]string{"x86-64-linux": "call __fn_is_alnum", "arm64-linux": "bl __fn_is_alnum"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"count", "first_other"} {
			if body := condBranchBody(t, string(asm), fn); strings.Contains(body, call[target]) {
				t.Errorf("%s: %s still calls is_alnum:\n%s", target, fn, body)
			}
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
		if got := run.ProcessState.ExitCode(); got != 63 {
			t.Errorf("%s: exit %d, want 63 (the interpreter's)", tg.target, got)
		}
	}
}

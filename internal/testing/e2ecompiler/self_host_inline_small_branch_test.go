package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pick is small, has no loop, returns no box and has two callers, so both
// calls splice into count. slow is called once, from pick, and is too large to
// splice into pick without pushing pick past its limit, so it stays a call.
const inlineSmallBranchProg = `function slow(a: i32, b: i32): i32 {
    let t: i32 = a - b;
    t = t * 31 + (a ^ 7) % 3 - (b & 5) / 2;
    t = t * 31 + (a ^ 14) % 4 - (b & 10) / 3;
    t = t * 31 + (a ^ 21) % 5 - (b & 15) / 4;
    t = t * 31 + (a ^ 28) % 6 - (b & 20) / 1;
    t = t * 31 + (a ^ 35) % 7 - (b & 25) / 2;
    t = t * 31 + (a ^ 42) % 8 - (b & 30) / 3;
    t = t * 31 + (a ^ 49) % 9 - (b & 35) / 4;
    t = t * 31 + (a ^ 56) % 10 - (b & 40) / 1;
    t = t * 31 + (a ^ 63) % 11 - (b & 45) / 2;
    t = t * 31 + (a ^ 70) % 12 - (b & 50) / 3;
    t = t * 31 + (a ^ 77) % 13 - (b & 55) / 4;
    t = t * 31 + (a ^ 84) % 14 - (b & 60) / 1;
    t = t * 31 + (a ^ 91) % 15 - (b & 65) / 2;
    t = t * 31 + (a ^ 98) % 16 - (b & 70) / 3;
    t = t * 31 + (a ^ 105) % 17 - (b & 75) / 4;
    t = t * 31 + (a ^ 112) % 18 - (b & 80) / 1;
    t = t * 31 + (a ^ 119) % 19 - (b & 85) / 2;
    t = t * 31 + (a ^ 126) % 20 - (b & 90) / 3;
    t = t * 31 + (a ^ 133) % 21 - (b & 95) / 4;
    t = t * 31 + (a ^ 140) % 22 - (b & 100) / 1;
    t = t * 31 + (a ^ 147) % 23 - (b & 105) / 2;
    t = t * 31 + (a ^ 154) % 24 - (b & 110) / 3;
    t = t * 31 + (a ^ 161) % 25 - (b & 115) / 4;
    t = t * 31 + (a ^ 168) % 26 - (b & 120) / 1;
    return t % 1000;
}
function pick(fast: boolean, a: i32, b: i32): boolean {
    if (fast) {
        return a < b;
    }
    return slow(a, b) < 0;
}
@noinline function count(n: i32, fast: boolean): i32 {
    let c: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        if (pick(fast, i, n - i)) {
            c = c + 1;
        }
        if (pick(fast, n - i, i * 2)) {
            c = c + 2;
        }
        i = i + 1;
    }
    return c;
}
function main(): i32 {
    return count(12, true) * 3 + count(9, false);
}
`

func TestSelfHostInlineSmallBranch(t *testing.T) {
	h := selfHostCLIForHost(t)
	interpBin := buildLangBinForInterp(t)
	want := interpExit(t, interpBin, inlineSmallBranchProg)
	dir := t.TempDir()
	src := filepath.Join(dir, "branch.fern")
	if err := os.WriteFile(src, []byte(inlineSmallBranchProg), 0o644); err != nil {
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
		body := condBranchBody(t, string(asm), "count")
		if strings.Contains(body, call[target]+"pick") {
			t.Errorf("%s: count still calls pick, a small body with two callers:\n%s", target, body)
		}
		if !strings.Contains(body, call[target]+"slow") {
			t.Errorf("%s: count does not call slow, which is too large to splice into pick:\n%s", target, body)
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
		if got := run.ProcessState.ExitCode(); got != want {
			t.Errorf("%s: exit %d, want %d (the interpreter's)", tg.target, got, want)
		}
	}
}

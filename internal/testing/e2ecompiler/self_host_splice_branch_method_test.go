package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Meter.take has three blocks and one caller, feed. No export, interface or
// dyn arm names it, so it is spliced into feed and its body is dropped.
const spliceBranchMethodProg = `struct Meter { n: i64, lo: i64, hi: i64, sum: i64, log: i64[] }
function (m: Meter) take(v: i64): Meter {
    let w = v * 3 + m.n;
    if (w < m.lo) {
        return Meter { ...m, n: m.n + 1, lo: w, sum: m.sum + w * 7 - (v & 5) };
    }
    if (w > m.hi) {
        return Meter { ...m, n: m.n + 1, hi: w, sum: m.sum + w * 11 - (v & 9) };
    }
    return Meter { ...m, n: m.n + 1, sum: m.sum + (w ^ 13) * 5 };
}
@noinline function feed(m: Meter, v: i64): Meter {
    return m.take(v);
}
function main(): i32 {
    let m = Meter { n: 0, lo: 10, hi: 50, sum: 0, log: [1, 2] };
    for i in 0..40 {
        m = feed(m, (i * 7 % 23) as i64);
    }
    return ((m.sum + m.lo + m.hi + m.n + m.log.len() as i64) % 200) as i32;
}
`

func TestSelfHostSpliceBranchMethod(t *testing.T) {
	h := selfHostCLIForHost(t)
	interpBin := buildLangBinForInterp(t)
	want := interpExit(t, interpBin, spliceBranchMethodProg)
	dir := t.TempDir()
	src := filepath.Join(dir, "meter.fern")
	if err := os.WriteFile(src, []byte(spliceBranchMethodProg), 0o644); err != nil {
		t.Fatal(err)
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
		if strings.Contains(string(asm), "__fn_Meter__take") {
			t.Errorf("%s: Meter.take is still a function, a method with a branch and one caller:\n%s", target, asm)
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

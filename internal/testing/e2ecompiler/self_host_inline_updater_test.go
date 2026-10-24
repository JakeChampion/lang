package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const inlineUpdaterProg = `struct P { xs: i32[], n: i32 }
function put(p: P, i: i32, v: i32): P { return P { ...p, xs: p.xs.with(i, v) }; }
@noinline function kept(p: P, i: i32, v: i32): P { return P { ...p, xs: p.xs.with(i, v) }; }
@noinline function via_one(p: P): P { return put(p, 0, 7); }
@noinline function via_two(p: P): P { return put(kept(p, 1, 8), 2, 9); }
@noinline function via_three(p: P): P { return put(p, 1, p.n); }
function main(): i32 {
    let p: P = P { xs: [1, 2, 3], n: 5 };
    let q: P = via_three(via_two(via_one(p)));
    return q.xs[0] + q.xs[1] + q.xs[2] + p.xs[0];
}
`

func TestSelfHostInlineUpdater(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "updater.fern")
	if err := os.WriteFile(src, []byte(inlineUpdaterProg), 0o644); err != nil {
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
		for _, caller := range []string{"via_one", "via_two", "via_three"} {
			if body := condBranchBody(t, string(asm), caller); strings.Contains(body, call[target]+"put") {
				t.Errorf("%s: %s still calls put:\n%s", target, caller, body)
			}
		}
		if body := condBranchBody(t, string(asm), "via_two"); !strings.Contains(body, call[target]+"kept") {
			t.Errorf("%s: via_two inlined kept, which is @noinline:\n%s", target, body)
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
		if got := run.ProcessState.ExitCode(); got != 22 {
			t.Errorf("%s: exit %d, want 22 (the interpreter's)", tg.target, got)
		}
	}
}

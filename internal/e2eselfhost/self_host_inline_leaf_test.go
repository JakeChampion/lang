package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A small function that calls nothing and returns a value is a leaf
// (ssa.leaf_of): its callers take its instructions in place of the call
// (ssa.inline_leaves). `lo32` lifts to a chain of blocks, which a leaf may be;
// `kept` is the same body declared `@noinline`, which stays a call.
const inlineLeafProg = `function lo32(x: i64): i32 { return (x & 4294967295i64) as i32; }
@noinline function kept(x: i64): i32 { return (x & 4294967295i64) as i32; }
@noinline function sum(n: i64): i32 {
    var t: i32 = 0;
    var i: i64 = 0i64;
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

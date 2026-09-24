package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A spilled loop-carried value shares its phi's frame slot when the two do not
// interfere (ssa.assign_spill_slots), as they share a register under the same
// rule, so the back edge moves nothing between slots. `carry` keeps sixteen
// values live round the loop, more than x86-64 has registers for; before the
// slots were paired, each spilled one was copied slot to slot through %rcx on
// every turn.
const spillSlotMatesProg = `@noinline function carry(n: i64, x: i64): i64 {
    var a: i64 = 1i64; var b: i64 = 2i64; var c: i64 = 3i64; var d: i64 = 4i64;
    var e: i64 = 5i64; var f: i64 = 6i64; var g: i64 = 7i64; var h: i64 = 8i64;
    var p: i64 = 9i64; var q: i64 = 10i64; var r: i64 = 11i64; var s: i64 = 12i64;
    var t: i64 = 13i64; var u: i64 = 14i64; var w: i64 = 15i64; var y: i64 = 16i64;
    var i: i64 = 0i64;
    while (i < n) {
        a = a + x; b = b + a; c = c + b; d = d + c; e = e + d; f = f + e; g = g + f; h = h + g;
        p = p + h; q = q + p; r = r + q; s = s + r; t = t + s; u = u + t; w = w + u; y = y + w;
        i = i + 1i64;
    }
    return a + b + c + d + e + f + g + h + p + q + r + s + t + u + w + y;
}
function main(): i32 { return (carry(1000i64, 3i64) % 200i64) as i32; }
`

func TestSelfHostSpillSlotMates(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "carry.fern")
	if err := os.WriteFile(src, []byte(spillSlotMatesProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "carry.s")
	if combined, err := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)\n__fn_carry\.r:\n(.*?)\n__fn_`).FindStringSubmatch(string(asm))
	if m == nil {
		t.Fatal("__fn_carry.r not in the listing")
	}
	lines := strings.Split(m[1], "\n")
	load := regexp.MustCompile(`^\s*movq (-?\d+)\(%rbp\), %rcx$`)
	store := regexp.MustCompile(`^\s*movq %rcx, (-?\d+)\(%rbp\)$`)
	slot := regexp.MustCompile(`^\s*movq .*-\d+\(%rbp\)`)
	spills := 0
	for i, l := range lines {
		if slot.MatchString(l) {
			spills++
		}
		if i+1 < len(lines) && load.MatchString(l) && store.MatchString(lines[i+1]) {
			t.Errorf("carry copies one slot to another:\n%s\n%s\n\n%s", l, lines[i+1], m[1])
		}
	}
	if spills == 0 {
		t.Fatalf("carry spills nothing, so the absence of slot copies proves nothing:\n%s", m[1])
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
		if got := run.ProcessState.ExitCode(); got != 164 {
			t.Errorf("%s: exit %d, want 164 (the interpreter's)", tg.target, got)
		}
	}
}

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The default arm64 emitter must reclaim a string local that was passed to a
// user function, and the bar is that retention does NOT grow with the input.
//
// This is what the default flip to -backend ssa (#9511) got wrong and nothing
// caught. The SSA backends run the single-word string ABI, where
// rc_analysis.go conservatively taints a string ident passed to a user
// function so it is never reclaimed caller-side (#4174 — the taint exists so a
// callee that retained the string cannot be left with a freed buffer). The
// arm64 stack-machine emitter runs the two-word ABI, which has no such taint.
// Defaulting to SSA therefore turned O(1) retention into O(input) on ordinary
// string-processing programs; the leak matrix caught the same shape as a
// verdict move on two rows, after the flip had merged.
//
// Measuring two input sizes rather than one absolute number is deliberate: the
// absolute figure moves with allocator and stdlib changes, while "does it grow
// with the input" is the property that actually distinguishes the two ABIs.
// The shape matters in three ways, and dropping any of them makes the fixture
// measure nothing:
//
//   - `seed` returns one of two literals chosen on `i`, so const-fold cannot
//     collapse the concat below into a literal. A folded concat never
//     allocates and the test passes on both emitters without proving anything.
//   - the string is long enough that the small-string optimisation cannot keep
//     it inline, so it really is a heap buffer.
//   - `tag` ALIASES its parameter into a local. Without that alias both
//     emitters reclaim the string; the alias is what the single-word ABI's
//     taint keys on, and it is the shape the leak matrix's alias_param cells
//     pin.
const defaultStringReclaimSrc = `function seed(i: i32): string {
    if (i % 2 == 0) { return "a string long enough to defeat the small-string optimisation A"; }
    return "a string long enough to defeat the small-string optimisation B";
}
function mkstr(a: string): string { return a + "!"; }
function tag(s: string, n: i32): i32 {
    var x: string = s;
    return x.len() + n;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < ROUNDS) {
        var line: string = mkstr(seed(i));
        acc = (acc + tag(line, i)) % 101;
        i = i + 1;
    }
    return acc % 83;
}
`

var leakcheckLive = regexp.MustCompile(`live_bytes=(\d+)`)

// liveBytesAtExit builds src with the CLI's DEFAULT emitter for arm64-linux —
// no -backend flag, which is the whole point — and returns the live_bytes
// FERN_LEAKCHECK reports when it exits.
func liveBytesAtExit(t *testing.T, bin, qemu, dir, name, src string) int {
	t.Helper()
	srcPath := filepath.Join(dir, name+".fern")
	binPath := filepath.Join(dir, name+".bin")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", srcPath, err)
	}
	compile := exec.Command(bin, "-target", "arm64-linux", "-o", binPath, srcPath)
	compile.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("default arm64 build failed: %v\n%s", err, out)
	}
	run := runArm64Bin(qemu, binPath)
	run.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	var errBuf strings.Builder
	run.Stderr = &errBuf
	_ = run.Run()
	m := leakcheckLive.FindStringSubmatch(errBuf.String())
	if m == nil {
		t.Fatalf("no leakcheck line in stderr for %s:\n%s", name, errBuf.String())
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse live_bytes %q: %v", m[1], err)
	}
	return n
}

func TestArm64DefaultReclaimsStringPassedToAFunction(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	small := liveBytesAtExit(t, bin, qemu, dir, "reclaim_small",
		strings.Replace(defaultStringReclaimSrc, "ROUNDS", "50", 1))
	large := liveBytesAtExit(t, bin, qemu, dir, "reclaim_large",
		strings.Replace(defaultStringReclaimSrc, "ROUNDS", "800", 1))

	// 16x the iterations. An emitter that reclaims holds the same handful of
	// bytes either way; one that does not holds 16x as much. Allow generous
	// slack for per-process fixed overhead without allowing growth.
	if large > small+4096 {
		t.Errorf("the default arm64 emitter retains %d bytes at 50 rounds and %d at 800 — "+
			"retention grows with the input, so a string passed to a user function is not "+
			"being reclaimed.\n\n"+
			"This is the single-word string ABI's #4174 taint (internal/ir/rc_analysis.go): "+
			"the SSA backends never set ast.TwoWordOverride, so a string ident passed to a "+
			"user function is tainted out of reclaim. The arm64 stack-machine emitter runs "+
			"the two-word ABI and has no such taint.\n\n"+
			"If this failed because the default moved to -backend ssa, that flip needs one "+
			"of the two gaps closed first: arm64ssa on the two-word ABI, or #4174's taint "+
			"replaced by an interprocedural answer to whether a callee retains its string "+
			"argument. Do not relax this bound to land the flip.", small, large)
	}
	if testing.Verbose() {
		fmt.Printf("arm64 default retention: 50 rounds=%d bytes, 800 rounds=%d bytes\n", small, large)
	}
}

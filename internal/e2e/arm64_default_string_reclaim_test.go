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

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The default arm64 emitter must reclaim a string local that was passed to a
// user function, and the bar is that retention does NOT grow with the input.
//
// This is what the default flip to -backend ssa (#9511) got wrong and nothing
// caught: arm64ssa retains one buffer whose size tracks the input, so ordinary
// string-processing programs went from O(1) retention to O(input). #9558 has
// the reproducer and the measurement — the object counts are a step apart
// while live_bytes differ a thousandfold, which is what says "one growing
// buffer" rather than "more objects leaked".
//
// The single-word string ABI is NOT the cause, though it was the first
// reading. x86-64 runs that ABI under its own default and is clean on the same
// programs, so rc_analysis.go's #4174 reclaim taint cannot be what this
// measures.
//
// Measuring two input sizes rather than one absolute number is deliberate: the
// absolute figure moves with allocator and stdlib changes, while "does it grow
// with the input" is the property that actually distinguishes a reclaiming
// emitter from one that does not. The shape matters in three ways, and
// dropping any of them makes the fixture measure nothing:
//
//   - `seed` returns one of two literals chosen on `i`, so const-fold cannot
//     collapse the concat below into a literal. A folded concat never
//     allocates and the test passes on both emitters without proving anything.
//   - the string is long enough that the small-string optimisation cannot keep
//     it inline, so it really is a heap buffer.
//   - `tag` ALIASES its parameter into a local, the shape the leak matrix's
//     alias_param cells pin. This no longer changes the verdict on either ABI
//     — frameBoundStringAliases credits it (#9549) — but it is kept because a
//     fixture whose callee only reads its parameter directly exercises the
//     narrowest path a caller can take, and the alias is how most Fern is
//     written.
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
	compile.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("default arm64 build failed: %v\n%s", err, out)
	}
	run := runArm64Bin(qemu, binPath)
	run.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
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
			"If this failed because the default moved to -backend ssa, the flip needs #9558 "+
			"closed first: arm64ssa retains one buffer whose size tracks the input. Do not "+
			"relax this bound to land the flip, and do not reach for the single-word string "+
			"ABI as the explanation — x86-64 runs that ABI under its own default and is "+
			"clean, which is what rules it out.", small, large)
	}
	if testing.Verbose() {
		fmt.Printf("arm64 default retention: 50 rounds=%d bytes, 800 rounds=%d bytes\n", small, large)
	}
}

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// On the single-word string ABI a string local passed to a user function is
// tainted out of reclaim unless the counted-retain summary clears the callee's
// position, and the summary had no arm for the plainest thing a callee can do
// with a string parameter: bind it to a local. The caller's buffer was then
// never freed, once per call (#9549).
//
// x86-64 is where this is measured, and that is the point: it runs the
// single-word ABI under its DEFAULT emitter, so the shape is reachable without
// naming a backend at all. arm64's default runs the two-word ABI and has no
// such taint — TestArm64DefaultReclaimsStringPassedToAFunction is that side.
//
// The two spellings are compiled and run as a pair rather than asserting an
// absolute figure. `tag` differs between them only in whether it binds the
// parameter to a local before reading it, which changes nothing about what
// either body retains, so any gap between their free counts is the defect.
// An absolute bound would move with the allocator and the stdlib; this does
// not.
const singleWordAliasReclaimSrc = `function seed(i: i32): string {
    if (i % 2 == 0) { return "a string long enough to defeat the small-string optimisation A"; }
    return "a string long enough to defeat the small-string optimisation B";
}
function mkstr(a: string): string { return a + "!"; }
function tag(s: string, n: i32): i32 {
    BODY
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var line: string = mkstr(seed(i));
        acc = (acc + tag(line, i)) % 101;
        i = i + 1;
    }
    return acc % 83;
}
`

var leakcheckCensus = regexp.MustCompile(`allocs=(\d+) frees=(\d+)`)

// censusAtExit builds src for x86-64-linux with the CLI's default emitter and
// returns the allocation and free counts FERN_LEAKCHECK reports at exit.
func censusAtExit(t *testing.T, bin string, runner []string, dir, name, src string) (allocs, frees int) {
	t.Helper()
	srcPath := filepath.Join(dir, name+".fern")
	binPath := filepath.Join(dir, name+".bin")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", srcPath, err)
	}
	compile := exec.Command(bin, "-target", "x86-64-linux", "-o", binPath, srcPath)
	compile.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("x86-64 build of %s failed: %v\n%s", name, err, out)
	}
	run := e2eharness.RunX86_64Bin(runner, binPath)
	run.Env = e2eharness.ChildEnv()
	var errBuf strings.Builder
	run.Stderr = &errBuf
	// The program exits with `acc % 83`, so a non-zero status is the normal
	// path and says nothing about whether the run was good. The census line
	// is the result, and its absence is the failure.
	_ = run.Run()
	m := leakcheckCensus.FindStringSubmatch(errBuf.String())
	if m == nil {
		t.Fatalf("no leakcheck census in stderr for %s:\n%s", name, errBuf.String())
	}
	a, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse allocs %q: %v", m[1], err)
	}
	f, err := strconv.Atoi(m[2])
	if err != nil {
		t.Fatalf("parse frees %q: %v", m[2], err)
	}
	return a, f
}

func TestSingleWordABIReclaimsAStringParamBoundToALocal(t *testing.T) {
	runner, ok := x86_64RunnerOrEmpty()
	if !ok {
		t.Skip("no way to run an x86-64 binary on this host")
	}
	bin := buildFernCLI(t)
	dir := t.TempDir()

	directAllocs, directFrees := censusAtExit(t, bin, runner, dir, "reclaim_direct",
		strings.Replace(singleWordAliasReclaimSrc, "BODY", "return s.len() + n;", 1))
	aliasAllocs, aliasFrees := censusAtExit(t, bin, runner, dir, "reclaim_alias",
		strings.Replace(singleWordAliasReclaimSrc, "BODY", "var x: string = s;\n    return x.len() + n;", 1))

	if directAllocs != aliasAllocs {
		t.Fatalf("the two spellings allocate differently (%d direct, %d through an alias) — "+
			"the fixture no longer compares like with like, so the free counts below mean nothing",
			directAllocs, aliasAllocs)
	}
	if aliasFrees != directFrees {
		t.Errorf("`var x: string = s; return x.len() + n;` frees %d of %d allocations where "+
			"`return s.len() + n;` frees %d — binding the parameter to a local cost the CALLER "+
			"its reclaim.\n\n"+
			"Both bodies read the parameter and return a scalar, so neither retains anything "+
			"past the return. The local takes no reference of its own either: the borrowed-alias "+
			"cancellation elides the transfer inc against an exit sweep that never touches the "+
			"slot. What differs is only whether internal/ir's counted-retain summary has an arm "+
			"for the shape (frameBoundStringAliases) — and when it does not, computeFreeEligible's "+
			"single-word string taint refuses the argument at every call site.\n\n"+
			"Do not fix this by relaxing the comparison: the two numbers are supposed to be equal.",
			aliasFrees, aliasAllocs, directFrees)
	}
	// Non-vacuous: the shape must really allocate. A const-folded concat or an
	// SSO-inline result would make both columns zero and the test silent.
	if aliasAllocs < 400 {
		t.Errorf("only %d allocations for 400 rounds — the fixture stopped heap-allocating "+
			"(const-fold collapsed the concat, or the strings became SSO-inline), so it no "+
			"longer measures reclaim at all", aliasAllocs)
	}
}

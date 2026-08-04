package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Heap event tracer (#6068) ------------------------------------
//
// ast.RcTrace (FERN_RC_TRACE=1) makes __fern_alloc and __fern_free each
// write one line to stderr:
//
//	rctrace <a|f> <ptr> <size> <site>
//
// three fixed-width 16-hex-digit numbers, where `site` is the caller's
// return address — the code that asked for or released the memory.
//
// It is to FERN_LEAKCHECK what FERN_RC_UNDERFLOW_TRAP is to the
// underflow counter: leakcheck says a leak happened, this says which
// alloc site it came from. So the load-bearing properties these tests
// pin are (1) the two agree, exactly, on every number they both
// report, (2) sites are per-call-site rather than a constant, and
// (3) allocs pair with frees by pointer on a balanced program.
//
// x86-64 only, like RcFreeDebug and RcUnderflowTrap.

// emitRcTrace compiles src with ast.RcTrace (and optionally
// ast.LeakCheckEnabled) toggled on, returning the asm text. Mirrors
// emitLeakCheck's pipeline.
func emitRcTrace(t *testing.T, src string, rcTrace, leakCheck bool) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	prevFree, prevLc, prevTrace := ast.RcFreeEnabled, ast.LeakCheckEnabled, ast.RcTrace
	t.Cleanup(func() {
		ast.RcFreeEnabled, ast.LeakCheckEnabled, ast.RcTrace = prevFree, prevLc, prevTrace
	})
	ast.RcFreeEnabled = true
	ast.LeakCheckEnabled = leakCheck
	ast.RcTrace = rcTrace
	asm, emitErr := x86_64.Emit(prog, info)
	ast.RcFreeEnabled, ast.LeakCheckEnabled, ast.RcTrace = prevFree, prevLc, prevTrace
	if emitErr != nil {
		t.Fatalf("x86_64 emit: %v", emitErr)
	}
	return asm
}

// runRcTraceX86_64 compiles src with the tracer on and runs it,
// returning stdout, stderr and the exit code separately (the tracer's
// contract is "stderr only, stdout untouched").
func runRcTraceX86_64(t *testing.T, src string, leakCheck bool) (string, string, int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	asm := emitRcTrace(t, src, true, leakCheck)
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	return runSplit(t, cmd)
}

var rcTraceLineRe = regexp.MustCompile(`^rctrace ([af]) ([0-9a-f]{16}) ([0-9a-f]{16}) ([0-9a-f]{16})$`)

// rcTraceEvent is one parsed `rctrace` line.
type rcTraceEvent struct {
	kind       string // "a" | "f"
	ptr        uint64
	size, site uint64
}

// parseRcTrace pulls every well-formed rctrace line out of stderr.
// Lines that are not rctrace records (e.g. a leakcheck summary, when
// both flags are on) are returned separately rather than ignored, so a
// test can assert on them too and a malformed trace line can't hide as
// "some other output".
func parseRcTrace(t *testing.T, stderr string) ([]rcTraceEvent, []string) {
	t.Helper()
	var evs []rcTraceEvent
	var other []string
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if line == "" {
			continue
		}
		m := rcTraceLineRe.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "rctrace") {
				t.Fatalf("malformed rctrace line: %q", line)
			}
			other = append(other, line)
			continue
		}
		ptr, _ := strconv.ParseUint(m[2], 16, 64)
		size, _ := strconv.ParseUint(m[3], 16, 64)
		site, _ := strconv.ParseUint(m[4], 16, 64)
		evs = append(evs, rcTraceEvent{kind: m[1], ptr: ptr, size: size, site: site})
	}
	return evs, other
}

// rcTraceBalancedSrc: 40 paired __alloc/__free of one class, plus a
// print so stdout preservation is observable. Every block is released,
// so allocs and frees must pair exactly.
const rcTraceBalancedSrc = `function main(): i32 {
    var i: i32 = 0;
    while (i < 40) {
        var a: usize = __alloc(64);
        __free(a, 64);
        i = i + 1;
    }
    print("done");
    return 7;
}`

// rcTraceTwoSitesSrc allocates from two distinct functions, one of
// whose buffers stays live to process exit. The dropped one is
// released each iteration; the kept one never is.
const rcTraceTwoSitesSrc = `function make_kept(): i32[] { return [1, 2, 3, 4]; }
function make_dropped(): i32 { var t: i32[] = [9, 9]; return t.len(); }
function main(): i32 {
    var keep: i32[] = make_kept();
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { n = n + make_dropped(); i = i + 1; }
    return keep.len() + n - 10;
}`

func TestRcTraceX86_64PairsAllocsWithFrees(t *testing.T) {
	stdout, stderr, code := runRcTraceX86_64(t, rcTraceBalancedSrc, false)
	if code != 7 {
		t.Errorf("exit code = %d, want 7 (the tracer must not disturb the exit code)", code)
	}
	if stdout != "done\n" {
		t.Errorf("stdout = %q, want %q — the trace belongs on stderr only", stdout, "done\n")
	}
	evs, other := parseRcTrace(t, stderr)
	if len(other) != 0 {
		t.Errorf("unexpected non-rctrace stderr lines: %q", other)
	}
	if len(evs) == 0 {
		t.Fatal("no rctrace events; expected an alloc/free pair per iteration")
	}
	// Every block handed out is handed back: pointer multiset of allocs
	// equals that of frees, and the two sides report the same size for
	// the same block.
	live := map[uint64]uint64{}
	for _, e := range evs {
		switch e.kind {
		case "a":
			live[e.ptr] = e.size
		case "f":
			sz, ok := live[e.ptr]
			if !ok {
				t.Fatalf("free of %016x was never traced as an alloc", e.ptr)
			}
			if sz != e.size {
				t.Errorf("block %016x: alloc size %d != free size %d — the two hooks must report the same 16-rounded size", e.ptr, sz, e.size)
			}
			delete(live, e.ptr)
		}
	}
	if len(live) != 0 {
		t.Errorf("%d block(s) never freed in a fully paired program: %v", len(live), live)
	}
}

func TestRcTraceX86_64SiteIsPerCallSite(t *testing.T) {
	_, stderr, _ := runRcTraceX86_64(t, rcTraceTwoSitesSrc, false)
	evs, _ := parseRcTrace(t, stderr)
	sites := map[uint64]int{}
	for _, e := range evs {
		if e.kind == "a" {
			sites[e.site]++
		}
	}
	if len(sites) < 2 {
		t.Fatalf("alloc sites = %d (%v), want >= 2 — two different functions allocate here, so a constant site means the return address isn't being captured", len(sites), sites)
	}
	// The kept buffer is allocated once, the dropped one three times:
	// site attribution should reflect that asymmetry rather than
	// splitting evenly.
	var counts []int
	for _, n := range sites {
		counts = append(counts, n)
	}
	if len(counts) == 2 && counts[0] == counts[1] {
		t.Errorf("both alloc sites fired %d times; want 1 (make_kept) and 3 (make_dropped)", counts[0])
	}
}

// TestRcTraceX86_64AgreesWithLeakCheck is the cross-check that makes
// the tracer trustworthy: with both flags on, the trace's own tallies
// must reproduce every number leakcheck reports independently. They
// share the rounding but not the counting path — leakcheck ticks BSS
// counters inside the helpers, the tracer prints from the call
// boundary — so agreement is real evidence, not a tautology.
func TestRcTraceX86_64AgreesWithLeakCheck(t *testing.T) {
	_, stderr, _ := runRcTraceX86_64(t, rcTraceTwoSitesSrc, true)
	evs, other := parseRcTrace(t, stderr)
	if len(other) != 1 {
		t.Fatalf("want exactly one non-rctrace line (the leakcheck summary), got %q", other)
	}
	m := leakCheckLineRe.FindStringSubmatch(other[0] + "\n")
	if m == nil {
		t.Fatalf("not a leakcheck report line: %q", other[0])
	}
	lcAllocs, _ := strconv.ParseInt(m[1], 10, 64)
	lcFrees, _ := strconv.ParseInt(m[2], 10, 64)
	lcLive, _ := strconv.ParseInt(m[3], 10, 64)

	var allocs, frees int64
	var allocBytes, freeBytes uint64
	for _, e := range evs {
		if e.kind == "a" {
			allocs++
			allocBytes += e.size
		} else {
			frees++
			freeBytes += e.size
		}
	}
	if allocs != lcAllocs {
		t.Errorf("traced allocs = %d, leakcheck allocs = %d", allocs, lcAllocs)
	}
	if frees != lcFrees {
		t.Errorf("traced frees = %d, leakcheck frees = %d", frees, lcFrees)
	}
	if live := int64(allocBytes) - int64(freeBytes); live != lcLive {
		t.Errorf("traced live_bytes = %d, leakcheck live_bytes = %d", live, lcLive)
	}
}

// TestRcTraceX86_64OffEmitsNothing is the cheap proxy for the
// byte-identical-when-off guarantee: a flag-off build must not contain
// a single tracer symbol, string or call.
func TestRcTraceX86_64OffEmitsNothing(t *testing.T) {
	off := emitRcTrace(t, rcTraceTwoSitesSrc, false, false)
	for _, needle := range []string{"__fern_rct_ev", ".Lrct_str_pre", ".Lrct_wrhex", "rctrace "} {
		if strings.Contains(off, needle) {
			t.Errorf("flag-off asm contains %q; the tracer must leave nothing behind", needle)
		}
	}
	on := emitRcTrace(t, rcTraceTwoSitesSrc, true, false)
	for _, needle := range []string{"__fern_rct_ev", ".Lrct_str_pre", ".Lrct_wrhex"} {
		if !strings.Contains(on, needle) {
			t.Errorf("flag-on asm is missing %q", needle)
		}
	}
}

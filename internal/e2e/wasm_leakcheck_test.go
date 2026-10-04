package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// --- The wasm leak census (#7912) ---------------------------------
//
// Until this landed, wasm was the one shipped backend with NO leak
// detector: `TestWASMRcCorrectnessCorpus` ran the same 216 programs as
// its two native siblings and could only ever see a wrong ANSWER, never
// a block the program never gave back. docs/TEST-GATES.md named that
// blindness on the rc corpus's row.
//
// The census is the register backends' contract, byte for byte:
//
//	leakcheck: allocs=<N> frees=<M> live_bytes=<K>
//
// on stderr, once, with stdout and the exit status untouched — plus the
// sanitizer's `fern-sanitizer: leak <K> bytes in <N> blocks` verdict
// under FERN_SANITIZE. Every allocation in this runtime reaches
// $__fern_alloc and every reclamation a free path that counts, so the
// counters are a census of the whole heap.
//
// Each test below has BOTH legs: a program that genuinely leaks, whose
// numbers must show it, and a program that does not, which must report
// a balanced census and no verdict line.

// buildLeakCheckComponent compiles src with the self-host compiler to a WASI
// core module with the census on (FERN_LEAKCHECK=1), or under the whole
// sanitizer (FERN_SANITIZE=1, which implies the census). The flag is read at
// EMIT time, like the register backends' — it changes the module the compiler
// produces, so it goes on the build, not the run. runComponent runs the core
// with `--invoke main`, and the exported main reports on its return.
func buildLeakCheckComponent(t *testing.T, src string, sanitize bool) string {
	t.Helper()
	skipIfPreview2Missing(t)
	return e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, src, leakCheckEnv(sanitize))
}

// buildLeakCheckCLIComponent is buildLeakCheckComponent for a program whose
// host surface is preview 2 (sockets, clocks, http): the census core, with its
// stdio and exit through preview 1, becomes a wasi:cli/run component by way of
// the preview-1 adapter, and the report comes out of _start's exit.
func buildLeakCheckCLIComponent(t *testing.T, src string, sanitize bool) string {
	t.Helper()
	skipIfPreview2Missing(t)
	core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, src, leakCheckEnv(sanitize))
	return e2eharness.AdaptPreview1Component(t, core)
}

func leakCheckEnv(sanitize bool) []string {
	if sanitize {
		return []string{"FERN_SANITIZE=1"}
	}
	return []string{"FERN_LEAKCHECK=1"}
}

// runLeakCheckWasm builds src with the census on and runs it, returning
// stdout, stderr and the exit code separately — the report contract is
// "stderr only, stdout untouched", so combined output won't do.
func runLeakCheckWasm(t *testing.T, src string, sanitize bool) (string, string, int) {
	t.Helper()
	return runComponent(t, buildLeakCheckComponent(t, src, sanitize), runOpts{})
}

// wasmLeakBalancedSrc: 100 paired __alloc/__free of one class. The same
// fixture the natives' leakCheckBalancedSrc uses, so the two censuses
// are comparable number for number.
const wasmLeakBalancedSrc = `function main(): i32 {
    let i: i32 = 0;
    while (i < 100) {
        let a: usize = __alloc(64);
        __free(a, 64);
        i = i + 1;
    }
    return 0;
}`

// wasmLeakRawSrc allocates two 64-byte blocks and frees neither, so the
// census must read exactly 128 live bytes in 2 blocks. Raw __alloc
// rather than a language-level shape on purpose: it is the one leak
// whose exact size does not depend on the current state of rc
// reclamation.
const wasmLeakRawSrc = `function main(): i32 {
    let a: usize = __alloc(64);
    let b: usize = __alloc(64);
    if (a == 0 || b == 0) { return 1; }
    return 42;
}`

// wasmLeakRcDropSrc is the rc-driven drop-everything loop: each
// iteration builds a fresh array and lets it die at the end of the
// scope. Precise drop frees every one, so the census must balance —
// this is the leg that would go red if a reclamation path stopped
// reaching __free.
const wasmLeakRcDropSrc = `function main(): i32 {
    let i: i32 = 0;
    while (i < 50) {
        let row: i32[] = [i, i + 1, i + 2];
        if (row[0] != i) { return 1; }
        i = i + 1;
    }
    return 0;
}`

// The census reports one well-formed line on stderr for a program that
// balances, and leaves stdout and the exit status alone.
func TestWASMLeakCheckBalancedCensus(t *testing.T) {
	stdout, stderr, code := runLeakCheckWasm(t, wasmLeakBalancedSrc, false)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (the census must not move the exit status)", code)
	}
	if !strings.Contains(stdout, "0") {
		t.Errorf("stdout=%q, want main's printed result (the census must not touch stdout)", stdout)
	}
	allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
	if allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live_bytes=%d, want 100 / 100 / 0", allocs, frees, live)
	}
}

// The catching leg: two never-freed 64-byte blocks have to show up as
// 128 live bytes. Before this change the same program reported nothing
// at all on any wasm build.
func TestWASMLeakCheckReportsLeakedBytes(t *testing.T) {
	_, stderr, code := runLeakCheckWasm(t, wasmLeakRawSrc, false)
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
	if live != 128 {
		t.Errorf("live_bytes=%d, want 128 (two unfreed 64-byte blocks)", live)
	}
	if allocs-frees != 2 {
		t.Errorf("allocs-frees=%d, want 2 (two blocks outstanding)", allocs-frees)
	}
}

// rc reclamation reaches __free: a loop whose arrays are all precisely
// dropped balances exactly, the same as its native siblings.
func TestWASMLeakCheckRcDropBalances(t *testing.T) {
	_, stderr, code := runLeakCheckWasm(t, wasmLeakRcDropSrc, false)
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Error("expected a non-zero alloc count (one array per iteration)")
	}
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

// Under FERN_SANITIZE the census gains the verdict line, whose text and
// numbers must match the natives'. A leak is a `fern-sanitizer:` line;
// that is the whole pass condition, without reading the three numbers.
func TestWASMSanitizeLeakVerdict(t *testing.T) {
	_, stderr, code := runLeakCheckWasm(t, wasmLeakRawSrc, true)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (a leak verdict must not clobber the exit status)", code)
	}
	m := sanLeakVerdictRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leak verdict line in stderr: %q", stderr)
	}
	bytes, _ := strconv.Atoi(m[1])
	blocks, _ := strconv.Atoi(m[2])
	if bytes != 128 || blocks != 2 {
		t.Errorf("verdict says %d bytes in %d blocks, want 128 / 2 (must match x86-64 exactly)", bytes, blocks)
	}
}

// The clean leg of the same mode: a balanced program prints no
// `fern-sanitizer:` line at all. Silence is the pass condition, so a
// verdict that fired on every run would be worth nothing.
func TestWASMSanitizeCleanRunIsSilent(t *testing.T) {
	stdout, stderr, code := runLeakCheckWasm(t, wasmLeakBalancedSrc, true)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.Contains(stdout, "0") {
		t.Errorf("stdout=%q, want main's printed result", stdout)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("clean program reported a sanitizer finding: %q", stderr)
	}
	allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

// The report is emitted at every exit seam but must print ONCE: the exit()
// builtin reports through $proc_exit and never returns to the exported main,
// which reports on a return.
func TestWASMLeakCheckReportsOnce(t *testing.T) {
	src := `function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    exit(0);
    return 1;
}`
	_, stderr, _ := runLeakCheckWasm(t, src, false)
	if got := strings.Count(stderr, "leakcheck: allocs="); got != 1 {
		t.Errorf("%d census lines in stderr, want exactly 1: %q", got, stderr)
	}
}

// Flag off, no census symbol is emitted at all — the cheap proxy for
// "a build without the flag is what a compiler that never had the
// feature would have produced". Checked on the module bytes, where the
// reporter's fixed text would appear verbatim.
func TestWASMLeakCheckOffEmitsNoCensus(t *testing.T) {
	skipIfPreview2Missing(t)
	_, stderr, _ := runComponent(t, buildComponent(t, wasmLeakBalancedSrc), runOpts{})
	if strings.Contains(stderr, "leakcheck:") {
		t.Errorf("census-off build still reported: %q", stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("census-off build emitted a sanitizer line: %q", stderr)
	}
}

// parseWasmLeakCheckLine asserts stderr carries a well-formed census line
// and returns its three numbers. Unlike the register backends' fixture
// this does not demand stderr be ONLY that line: wasmtime is free to add
// its own noise around a run.
func parseWasmLeakCheckLine(t *testing.T, stderr string) (allocs, frees, live int64) {
	t.Helper()
	m := wasmLeakCheckLineRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leakcheck report line in stderr: %q", stderr)
	}
	allocs, _ = strconv.ParseInt(m[1], 10, 64)
	frees, _ = strconv.ParseInt(m[2], 10, 64)
	live, _ = strconv.ParseInt(m[3], 10, 64)
	return allocs, frees, live
}

// wasmLeakCheckLineRe matches the census line anywhere in stderr.
var wasmLeakCheckLineRe = regexp.MustCompile(`leakcheck: allocs=(-?\d+) frees=(-?\d+) live_bytes=(-?\d+)\n`)

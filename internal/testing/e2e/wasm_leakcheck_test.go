package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func buildLeakCheckWasmCore(t *testing.T, src string, sanitize bool) string {
	t.Helper()
	skipIfPreview2Missing(t)
	return e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, src, leakCheckEnv(sanitize))
}

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

func runLeakCheckWasm(t *testing.T, src string, sanitize bool) (string, string, int) {
	t.Helper()
	return runWasmArtifact(t, buildLeakCheckWasmCore(t, src, sanitize), runOpts{})
}

const wasmLeakBalancedSrc = `function main(): i32 {
    let i: i32 = 0;
    while (i < 100) {
        let a: usize = __alloc(64);
        __free(a, 64);
        i = i + 1;
    }
    return 0;
}`

const wasmLeakRawSrc = `function main(): i32 {
    let a: usize = __alloc(64);
    let b: usize = __alloc(64);
    if (a == 0 || b == 0) { return 1; }
    return 42;
}`

const wasmLeakRcDropSrc = `function main(): i32 {
    let i: i32 = 0;
    while (i < 50) {
        let row: i32[] = [i, i + 1, i + 2];
        if (row[0] != i) { return 1; }
        i = i + 1;
    }
    return 0;
}`

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

func TestWASMLeakCheckOffEmitsNoCensus(t *testing.T) {
	skipIfPreview2Missing(t)
	_, stderr, _ := runWasmArtifact(t, buildWasmCore(t, wasmLeakBalancedSrc), runOpts{})
	if strings.Contains(stderr, "leakcheck:") {
		t.Errorf("census-off build still reported: %q", stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("census-off build emitted a sanitizer line: %q", stderr)
	}
}

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

var wasmLeakCheckLineRe = regexp.MustCompile(`leakcheck: allocs=(-?\d+) frees=(-?\d+) live_bytes=(-?\d+)\n`)

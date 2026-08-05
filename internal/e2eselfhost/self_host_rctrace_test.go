package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// --- Self-host heap event tracer (#6068) --------------------------
//
// The self-host x86-64 runtime's port of FERN_RC_TRACE / FERN_LEAKCHECK.
// __fern_alloc and every freelist-push site call __fern_hev_a / __fern_hev_f,
// which write one line to stderr:
//
//	rctrace <a|f> <ptr> <size> <site>
//
// three fixed-width 16-hex numbers, and tick the leakcheck counters that
// __fern_lc_report prints at exit.
//
// The native side has had this since #6076; the self-host runtime had only the
// over-release detector, so it could see a block freed twice but never one that
// was never freed. These tests pin the properties that make the pair usable as
// a differential against native: (1) allocs pair with frees by POINTER with
// matching sizes on a balanced program, (2) sites are per-call-site rather than
// a constant, (3) the trace's own tallies reproduce every number leakcheck
// reports independently, (4) a retaining program's unpaired allocs account for
// live_bytes exactly, and (5) a flag-off build carries no trace of the feature.
//
// x86-64 only, like the native original.

// hevBalancedSrc allocates and drops: every box it makes is dead before main
// returns, so a correct trace pairs completely and live_bytes is 0.
const hevBalancedSrc = `function sum(xs: i32[]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i]; i = i + 1; }
    return t;
}

function main(): i32 {
    var a: i32 = sum([1, 2, 3]);
    var s: string = "ab" + "cd";
    return a + s.len();
}`

// hevRetainSrc keeps every array it builds alive in `keep` until exit, so the
// blocks behind it are never freed. The unpaired allocs are the leak, and they
// all come from one construction site.
const hevRetainSrc = `function make_buf(n: i32): i32[] {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs;
}

function main(): i32 {
    var keep: i32[][] = [];
    var i: i32 = 0;
    while (i < 3) { keep = keep.append(make_buf(4)); i = i + 1; }
    return keep.len();
}`

// hevEvent is one parsed `rctrace` line.
type hevEvent struct {
	kind string // "a" | "f"
	ptr  uint64
	size uint64
	site uint64
}

// runCaptureEnv is RunCapture with environment overrides — the self-host
// compiler reads FERN_RC_TRACE / FERN_LEAKCHECK at EMIT time, so the flags go
// to the driver process, not to the program it produces.
func runCaptureEnv(t *testing.T, runner []string, bin string, stdin []byte, env []string, extraArgs ...string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, extraArgs...)
	} else {
		args := append([]string{}, runner[1:]...)
		args = append(args, bin)
		args = append(args, extraArgs...)
		cmd = exec.Command(runner[0], args...)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s: %v", bin, err)
	}
	return out
}

// hevCompile emits asm for src with the given diagnostic env applied to the
// self-host driver.
func hevCompile(t *testing.T, runner []string, driverBin, src string, env []string) string {
	t.Helper()
	full := append([]string{"PATH=/usr/bin:/bin"}, env...)
	asm := runCaptureEnv(t, runner, driverBin, []byte(src), full)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	return string(asm)
}

// hevRun runs a built binary and returns its stderr plus exit code.
func hevRun(t *testing.T, runner []string, bin string) (string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	return errBuf.String(), cmd.ProcessState.ExitCode()
}

// parseHev pulls the `rctrace` lines and the `leakcheck` summary out of a
// program's stderr. The fixed-width hex is the point of the format: a pairing
// consumer needs no field-splitting beyond whitespace to match an `a` line to
// its `f` line by pointer.
func parseHev(t *testing.T, stderr string) ([]hevEvent, string) {
	t.Helper()
	var evs []hevEvent
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
			continue
		}
		if !strings.HasPrefix(line, "rctrace ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 5 {
			t.Fatalf("malformed trace line %q: want 5 fields, got %d", line, len(f))
		}
		for _, n := range f[2:] {
			if len(n) != 16 {
				t.Errorf("trace field %q in %q is %d hex digits, want a fixed-width 16", n, line, len(n))
			}
		}
		ptr, err := strconv.ParseUint(f[2], 16, 64)
		if err != nil {
			t.Fatalf("ptr %q: %v", f[2], err)
		}
		size, err := strconv.ParseUint(f[3], 16, 64)
		if err != nil {
			t.Fatalf("size %q: %v", f[3], err)
		}
		site, err := strconv.ParseUint(f[4], 16, 64)
		if err != nil {
			t.Fatalf("site %q: %v", f[4], err)
		}
		if f[1] != "a" && f[1] != "f" {
			t.Fatalf("trace kind %q in %q, want a or f", f[1], line)
		}
		evs = append(evs, hevEvent{kind: f[1], ptr: ptr, size: size, site: site})
	}
	return evs, summary
}

// TestSelfHostRcTracePairsX86_64 — a balanced program's allocs pair with its
// frees by pointer, at matching sizes, and the sites are per-call-site.
func TestSelfHostRcTracePairsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, hevBalancedSrc, []string{"FERN_RC_TRACE=1"})
	progBin := buildBin(t, gcc, dir, "hev_pairs", asm)
	stderr, _ := hevRun(t, runner, progBin)

	evs, _ := parseHev(t, stderr)
	if len(evs) == 0 {
		t.Fatal("FERN_RC_TRACE=1 produced no rctrace lines")
	}

	// Pair by pointer. A live block's alloc has no matching free; a size
	// mismatch means the two hooks disagree about the block, which would make
	// live_bytes drift even on a program that frees everything.
	live := map[uint64]hevEvent{}
	allocs, frees := 0, 0
	for _, e := range evs {
		if e.kind == "a" {
			if prev, dup := live[e.ptr]; dup {
				t.Errorf("block %#016x allocated twice without an intervening free (sizes %d then %d)", e.ptr, prev.size, e.size)
			}
			live[e.ptr] = e
			allocs++
			continue
		}
		frees++
		a, ok := live[e.ptr]
		if !ok {
			t.Errorf("free of %#016x with no matching alloc", e.ptr)
			continue
		}
		if a.size != e.size {
			t.Errorf("block %#016x: alloc reported size %d, free reported %d — the two hooks disagree", e.ptr, a.size, e.size)
		}
		delete(live, e.ptr)
	}
	if allocs == 0 || frees == 0 {
		t.Fatalf("want both allocs and frees, got %d/%d", allocs, frees)
	}

	// Sites must name the code that asked for the memory, not a constant.
	sites := map[uint64]bool{}
	for _, e := range evs {
		if e.kind == "a" {
			sites[e.site] = true
		}
		if e.site == 0 {
			t.Errorf("event %+v has a zero site", e)
		}
	}
	if len(sites) < 2 {
		t.Errorf("all %d allocs report site %v — sites are not per-call-site", allocs, sites)
	}
}

// TestSelfHostLeakCheckAgreesX86_64 — the trace's own tallies reproduce every
// number the leakcheck summary reports. Two independent counting paths over
// the same events (leakcheck accumulates in .bss as it goes; the trace is
// re-derived here from the emitted lines), so a drift in either shows up as a
// disagreement rather than as two consistent wrong answers.
func TestSelfHostLeakCheckAgreesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"balanced", hevBalancedSrc},
		{"retaining", hevRetainSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_RC_TRACE=1", "FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "hev_agree_"+tc.name, asm)
			stderr, _ := hevRun(t, runner, progBin)

			evs, summary := parseHev(t, stderr)
			if summary == "" {
				t.Fatal("FERN_LEAKCHECK=1 produced no leakcheck summary")
			}
			var wantA, wantF, wantLive int64
			if _, err := fmtSscan(summary, &wantA, &wantF, &wantLive); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}

			var gotA, gotF, allocBytes, freeBytes int64
			for _, e := range evs {
				if e.kind == "a" {
					gotA++
					allocBytes += int64(e.size)
				} else {
					gotF++
					freeBytes += int64(e.size)
				}
			}
			if gotA != wantA || gotF != wantF {
				t.Errorf("trace counted %d allocs / %d frees, leakcheck reported %d / %d", gotA, gotF, wantA, wantF)
			}
			if got := allocBytes - freeBytes; got != wantLive {
				t.Errorf("trace live_bytes %d, leakcheck reported %d", got, wantLive)
			}
		})
	}
}

// TestSelfHostRcTraceLocatesLeakX86_64 — the property the whole feature exists
// for. A program that retains everything it builds leaves unpaired allocs; they
// must account for live_bytes exactly and attribute to a single site, which is
// what turns "live_bytes=168" from a true statement nothing can act on into a
// pointer at the construction site that leaked.
func TestSelfHostRcTraceLocatesLeakX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, hevRetainSrc, []string{"FERN_RC_TRACE=1", "FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "hev_leak", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 3 {
		t.Fatalf("retaining program exited %d, want 3", exit)
	}

	evs, summary := parseHev(t, stderr)
	live := map[uint64]hevEvent{}
	for _, e := range evs {
		if e.kind == "a" {
			live[e.ptr] = e
		} else {
			delete(live, e.ptr)
		}
	}
	if len(live) == 0 {
		t.Fatal("a program that retains every array it builds reported no unpaired allocs")
	}

	var leaked int64
	sites := map[uint64]int{}
	for _, e := range live {
		leaked += int64(e.size)
		sites[e.site]++
	}
	var wantA, wantF, wantLive int64
	if _, err := fmtSscan(summary, &wantA, &wantF, &wantLive); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if leaked != wantLive {
		t.Errorf("unpaired allocs total %d bytes, leakcheck reported live_bytes=%d — "+
			"pairing must account for the leak exactly, or it cannot localise it", leaked, wantLive)
	}
	if len(sites) != 1 {
		t.Errorf("the retained arrays came from %d sites %v, want 1 — every one is the same construction", len(sites), sites)
	}
}

// TestSelfHostHeapEventFlagOffX86_64 — with neither flag set the emitted asm
// must carry no trace of the feature: no hook symbol, no call, no format
// string. A diagnostic build mode that leaks into ordinary output is a
// diagnostic that changes what it measures.
func TestSelfHostHeapEventFlagOffX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	off := hevCompile(t, runner, driverBin, hevBalancedSrc, nil)
	for _, marker := range []string{
		"__fern_hev_a", "__fern_hev_f", "__fern_lc_report", "__fern_alloc_inner",
		"__fern_lc_alloc_count", "__fern_lc_free_bytes", "rctrace", "leakcheck",
		".Lhev_", ".Llc_",
	} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the feature is not fully gated", marker)
		}
	}

	// And the hooks really do appear when asked for, so the check above is
	// testing a gate rather than a typo.
	on := hevCompile(t, runner, driverBin, hevBalancedSrc, []string{"FERN_RC_TRACE=1", "FERN_LEAKCHECK=1"})
	for _, marker := range []string{"__fern_hev_a", "__fern_hev_f", "__fern_lc_report", "__fern_alloc_inner"} {
		if !strings.Contains(on, marker) {
			t.Errorf("flag-on asm is missing %q", marker)
		}
	}
}

// fmtSscan pulls the three numbers out of a leakcheck summary line without
// depending on its exact spacing.
func fmtSscan(summary string, allocs, frees, live *int64) (int, error) {
	fields := map[string]*int64{"allocs=": allocs, "frees=": frees, "live_bytes=": live}
	n := 0
	for _, tok := range strings.Fields(summary) {
		for prefix, dst := range fields {
			if !strings.HasPrefix(tok, prefix) {
				continue
			}
			v, err := strconv.ParseInt(strings.TrimPrefix(tok, prefix), 10, 64)
			if err != nil {
				return n, err
			}
			*dst = v
			n++
		}
	}
	if n != 3 {
		return n, errShortSummary
	}
	return n, nil
}

var errShortSummary = errSummary("leakcheck summary did not carry allocs/frees/live_bytes")

type errSummary string

func (e errSummary) Error() string { return string(e) }

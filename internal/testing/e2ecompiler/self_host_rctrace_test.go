package e2ecompiler

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const hevBalancedSrc = `function sum(xs: i32[]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i]; i = i + 1; }
    return t;
}

function main(): i32 {
    let a: i32 = sum([1, 2].append(3));
    let s: string = "ab" + "cd";
    return a + s.len();
}`

const hevExitLeakSrc = `function bail(n: i32): i32 { exit(n); return 0; }
function main(): i32 {
    let keep: i32[][] = [];
    let i: i32 = 0;
    while (i < 3) { keep = keep.append([i, 2, 3, 4]); i = i + 1; }
    let r: i32 = bail(keep.len());
    return r + keep.len();
}`

type hevEvent struct {
	kind   string
	ptr    uint64
	size   uint64
	site   uint64
	caller uint64
}

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
	cmd.Env = probeEnv(env...)
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = ee.Stderr
		}
		t.Fatalf("run %s: %v\n%s", bin, err, stderr)
	}
	return out
}

func hevCompile(t *testing.T, runner []string, driverBin, src string, env []string) string {
	t.Helper()
	full := append([]string{"PATH=/usr/bin:/bin"}, env...)
	asm := runCaptureEnv(t, runner, driverBin, []byte(src), full)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	return string(asm)
}

func loadCompile(t *testing.T, runner []string, driverBin, src string, env []string, args ...string) (string, string, error) {
	t.Helper()
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := append([]string{path, stdlibRoot}, args...)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, argv...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), argv...)...)
	}
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil && len(out) == 0 {
		err = errors.New("the driver emitted 0 bytes")
	}
	return string(out), stderr.String(), err
}

func mustLoadCompile(t *testing.T, runner []string, driverBin, src string, env []string, args ...string) string {
	t.Helper()
	out, stderr, err := loadCompile(t, runner, driverBin, src, env, args...)
	if err != nil {
		t.Fatalf("asm_load_run: %v\n%s", err, stderr)
	}
	return out
}

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
		if len(f) != 6 {
			t.Fatalf("malformed trace line %q: want 6 fields, got %d", line, len(f))
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
		caller, err := strconv.ParseUint(f[5], 16, 64)
		if err != nil {
			t.Fatalf("caller %q: %v", f[5], err)
		}
		if f[1] != "a" && f[1] != "f" {
			t.Fatalf("trace kind %q in %q, want a or f", f[1], line)
		}
		evs = append(evs, hevEvent{kind: f[1], ptr: ptr, size: size, site: site, caller: caller})
	}
	return evs, summary
}

func TestSelfHostRcTracePairsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, hevBalancedSrc, []string{"FERN_RC_TRACE=1"})
	progBin := buildBin(t, gcc, dir, "hev_pairs", asm)
	stderr, _ := hevRun(t, runner, progBin)

	evs, _ := parseHev(t, stderr)
	if len(evs) == 0 {
		t.Fatal("FERN_RC_TRACE=1 produced no rctrace lines")
	}

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

func TestSelfHostLeakCheckAgreesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"balanced", hevBalancedSrc},
		{"unreclaimed_at_exit", hevExitLeakSrc},
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

func TestSelfHostRcTraceLocatesLeakX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, hevExitLeakSrc, []string{"FERN_RC_TRACE=1", "FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "hev_leak", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 3 {
		t.Fatalf("probe exited %d, want 3", exit)
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
		t.Fatal("a program that exits with live locals reported no unpaired allocs — " +
			"exit() does not unwind, so those blocks are never freed")
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
	for site, n := range sites {
		if site == 0 {
			t.Errorf("%d unpaired allocs carry a zero site — an unattributed leak is exactly "+
				"the unactionable report this feature exists to replace", n)
		}
	}
}

func TestSelfHostHeapEventFlagOffX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

	on := hevCompile(t, runner, driverBin, hevBalancedSrc, []string{"FERN_RC_TRACE=1", "FERN_LEAKCHECK=1"})
	for _, marker := range []string{"__fern_hev_a", "__fern_hev_f", "__fern_lc_report", "__fern_alloc_inner"} {
		if !strings.Contains(on, marker) {
			t.Errorf("flag-on asm is missing %q", marker)
		}
	}
}

func parseLeakcheck(t *testing.T, name, stderr string) (int64, int64, int64) {
	t.Helper()
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", name)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: parse %q: %v", name, summary, err)
	}
	return allocs, frees, live
}

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

const hevTwoAppendSrc = `@noinline
function grow_local(n: i32): i32[] {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs;
}
@noinline
function grow_param(ys: i32[], n: i32): i32[] {
    let j: i32 = 0;
    while (j < n) { ys = ys.append(j * 2); j = j + 1; }
    return ys;
}
function main(): i32 {
    let a: i32[] = grow_local(64);
    let b: i32[] = grow_param(a, 40);
    exit(b.len());
    return 0;
}`

func TestSelfHostRcTraceCallerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, hevTwoAppendSrc, []string{"FERN_RC_TRACE=1"})
	progBin := buildBin(t, gcc, dir, "hev_caller", asm)
	stderr, _ := hevRun(t, runner, progBin)

	evs, _ := parseHev(t, stderr)
	if len(evs) == 0 {
		t.Fatal("FERN_RC_TRACE=1 produced no rctrace lines")
	}

	perSite := map[uint64]map[uint64]bool{}
	differ := 0
	for _, e := range evs {
		if e.kind != "a" {
			continue
		}
		if perSite[e.site] == nil {
			perSite[e.site] = map[uint64]bool{}
		}
		perSite[e.site][e.caller] = true
		if e.caller != e.site && e.caller != 0 {
			differ++
		}
	}
	if differ == 0 {
		t.Fatal("every allocation reported caller == site: the frame walk is one link short, " +
			"so the field duplicates site instead of attributing past the helper")
	}

	best := 0
	for _, callers := range perSite {
		if len(callers) > best {
			best = len(callers)
		}
	}
	if best < 2 {
		t.Errorf("no site reported more than one caller (max %d); two appending functions "+
			"share the __fern_arr_push allocation site, so caller must separate them", best)
	}
}

func TestSelfHostRcTraceDeepX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	plain := hevCompile(t, runner, driverBin, hevTwoAppendSrc, []string{"FERN_RC_TRACE=1"})
	plainBin := buildBin(t, gcc, dir, "hev_deep_off", plain)
	if _, err := hevRows(t, runner, plainBin, 6); err != nil {
		t.Fatalf("FERN_RC_TRACE alone: %v", err)
	}

	deep := hevCompile(t, runner, driverBin, hevTwoAppendSrc,
		[]string{"FERN_RC_TRACE=1", "FERN_RC_TRACE_DEEP=1"})
	deepBin := buildBin(t, gcc, dir, "hev_deep_on", deep)
	rows, err := hevRows(t, runner, deepBin, 7)
	if err != nil {
		t.Fatalf("FERN_RC_TRACE_DEEP: %v", err)
	}

	const caller, caller2 = 3, 4
	differs := 0
	for _, r := range rows {
		if r[caller] != 0 && r[caller2] != 0 && r[caller2] != r[caller] {
			differs++
		}
	}
	if differs == 0 {
		t.Fatal("no allocation reported a caller2 that differs from caller: the second walk " +
			"is not stepping a frame, so the sixth field duplicates the fifth")
	}
}

func hevRows(t *testing.T, runner []string, bin string, wantFields int) ([][]uint64, error) {
	t.Helper()
	stderr, _ := hevRun(t, runner, bin)
	var rows [][]uint64
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(line, "rctrace ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != wantFields {
			return nil, fmt.Errorf("line %q has %d fields, want %d", line, len(f), wantFields)
		}
		if f[1] != "a" {
			continue
		}
		row := make([]uint64, 0, len(f)-2)
		for _, n := range f[2:] {
			if len(n) != 16 {
				return nil, fmt.Errorf("field %q in %q is %d hex digits, want 16", n, line, len(n))
			}
			v, err := strconv.ParseUint(n, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("field %q: %v", n, err)
			}
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no rctrace allocation lines")
	}
	return rows, nil
}

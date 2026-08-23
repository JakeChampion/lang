package e2eselfhost

import (
	"strings"
	"testing"
)

// --- The arm64 census (#5362's arm64 half, in the self-host) -----------------
//
// FERN_LEAKCHECK is read by the self-host compiler at EMIT time and, until
// this port, only the x86-64 backend acted on it — the reason the leak matrix
// and the sanitize instruments are x86-only (docs/SELFHOST-RC-PLAN-PROMOTION.md
// names porting this emitter as what extends that gate). The port counts at
// the allocator's own 8-byte-word granularity on both sides — __fern_alloc's
// rounded request and each free site's class index are the same number for
// the same block — and reports through __fern_lc_report from both exit paths
// (the _start epilogue and the exit() builtin), OUTSIDE the heap gate, so a
// heap-free program still links and reports zeros.
//
// Deliberately NOT ported here: the sanitizer (over-release trap, quarantine,
// leak verdict). On arm64 FERN_SANITIZE stays inert rather than becoming a
// census-only mode that would misreport what "sanitize" checks.
//
// Counts are asserted as PROPERTIES (balanced/unbalanced, zero/non-zero), not
// exact numbers: the x86 legs pin exact counts per shape elsewhere, and this
// suite gates the instrument, not the reclaim behavior.

type arm64LcCase struct {
	name string
	src  string
	want int
	// balanced: allocs == frees and live == 0 (a clean program).
	// leaky: allocs > frees and live > 0 (a refused shape, sound leak).
	// zero: allocs == 0 && frees == 0 (a heap-free program).
	verdict string
}

func arm64LcCases() []arm64LcCase {
	return []arm64LcCase{
		{
			// String churn, fully reclaimed: the fresh-string credit fires per
			// round and the census balances. Exit oracle-confirmed (21, the
			// x86 alias-suite family's number for this shape).
			name: "clean_string_churn",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); return t.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "balanced",
		},
		{
			// The refused alias chain (string_alias_chain_refused's shape):
			// leaks soundly per round, so the census must SAY so — the half a
			// green exit code cannot.
			name: "leak_alias_chain",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); var v: string = t; var u: string = v; return u.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "leaky",
		},
		{
			// HEAP-FREE, returning main: the report rides the _start epilogue
			// and must link with no allocator emitted — the reason the
			// counters and report live outside the heap gate.
			name: "heapfree_return",
			src:  `function main(): i32 { return 7; }`,
			want: 7, verdict: "zero",
		},
		{
			// HEAP-FREE through the exit() builtin — the other report site.
			name: "heapfree_exit_builtin",
			src:  `function main(): i32 { exit(9); return 0; }`,
			want: 9, verdict: "zero",
		},
	}
}

func TestSelfHostArm64Leakcheck(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arm64LcCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCaptureEnv(t, x86runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux"))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "lc_"+tc.name, asm)
			cmd := runArm64Bin(qemu, bin)
			var errBuf strings.Builder
			cmd.Stderr = &errBuf
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Fatalf("%s exited %d, want %d — the census must not disturb the program", tc.name, code, tc.want)
			}
			summary := leakSummaryLine(errBuf.String())
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary on stderr (%q)", tc.name, errBuf.String())
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			switch tc.verdict {
			case "balanced":
				if allocs == 0 {
					t.Errorf("%s: allocs=0 — the probe exercised no allocation", tc.name)
				}
				if allocs != frees || live != 0 {
					t.Errorf("%s: %s — want a balanced census (allocs == frees, live 0)", tc.name, summary)
				}
			case "leaky":
				if allocs <= frees || live <= 0 {
					t.Errorf("%s: %s — this shape leaks soundly per round; a balanced census "+
						"here means the counters miss a free path or an alloc path", tc.name, summary)
				}
			case "zero":
				if allocs != 0 || frees != 0 || live != 0 {
					t.Errorf("%s: %s — a heap-free program must report zeros", tc.name, summary)
				}
			}
		})
	}
}

// Flag off, nothing reaches the emitted asm and the run is silent — the cheap
// proxy for byte-identical, in both directions (the markers DO appear when
// asked for, so this tests a gate rather than a typo).
func TestSelfHostArm64LeakcheckOffEmitsNothing(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	src := arm64LcCases()[0].src
	off := string(runCaptureEnv(t, x86runner, driverBin, []byte(src),
		[]string{"PATH=/usr/bin:/bin"}, "-target", "arm64-linux"))
	for _, marker := range []string{"__fern_lc_", "leakcheck:"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the census is not fully gated", marker)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "lc_off", off)
	cmd := runArm64Bin(qemu, bin)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 21 {
		t.Fatalf("flag-off run exited %d, want 21", code)
	}
	if errBuf.String() != "" {
		t.Errorf("flag-off run wrote stderr: %q", errBuf.String())
	}

	on := string(runCaptureEnv(t, x86runner, driverBin, []byte(src),
		[]string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux"))
	for _, marker := range []string{"__fern_lc_report", "__fern_lc_alloc_count", "leakcheck: allocs="} {
		if !strings.Contains(on, marker) {
			t.Errorf("flag-on asm is missing %q", marker)
		}
	}
}

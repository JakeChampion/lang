package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An UNMATCHED Option[Option[T]] local releases both boxes (#7714) --------
//
// The nested-Option member of the Option reclaim family, beside #7710 (matched
// Option[string]) and #7712 (reassigned Option[string]). An unmatched
// `Option[Option[i32]]` local must release both boxes as the matched one does;
// missing it strands 80 B/round, unbounded (400/0, live 16000). The matched
// case balances even with no arm binding at all, so the binding is never the
// releaser.
//
// For a scalar inner, the inner option box owns no pointer, so a flat dec of
// the payload after the null and tag checks is its complete release. An rc
// inner payload must not take that flat dec — it frees the inner box but
// nothing the box owns — so it takes a guarded two-level walk instead (#7718,
// the `rc_inner_*` rows). Releasing one slot both ways would free the same box
// twice.
//
// A REASSIGNED nested-Option local whose every rebind allocates its own inner
// box releases each superseded box at its rebind (#7716), matched or not.
//
// Every want was confirmed against bin/fern -interp, never read off the
// self-host run under test, and every row is sanitizer-clean under
// FERN_SANITIZE=1.
type unmatchedOptoptCase struct {
	name string
	src  string
	want int
}

const unmatchedOptoptMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func unmatchedOptoptCases() []unmatchedOptoptCase {
	return []unmatchedOptoptCase{
		{
			// THE REPRO. Was 400/0 live 16000.
			name: "unmatched_nested_option",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			// The None-payload spelling of the same construction.
			name: "unmatched_nested_none",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(None);
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			// Matched: covered by the consuming-match analysis before this change
			// and still covered. Must not be credited TWICE — a second release is
			// an over-release, caught by the 99 guard rather than by any byte count.
			name: "matched_control",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63,
		},
		{
			// Matched with NO arm binding — the row that showed the binding was
			// never the releaser.
			name: "matched_wildcard_control",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 4,
		},
		{
			// THE REBIND HALF (#7716): reassigned, every rebind allocating its own
			// inner box. Was 600/0 live 24000.
			name: "reassigned_unmatched",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(Some(i + 1)); }
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			// The MATCHED half of the same rebind, which never reaches this
			// collector — its escape gate reads a bare-ident match scrutinee as an
			// escape — and belongs to the consuming-match family instead. That
			// family releases the box the match CONSUMES; the "OPTOPTRB:" credit
			// releases each SUPERSEDED box at its own rebind. The two act on
			// disjoint values, which is what lets them coexist.
			//
			// Relaxing the consuming-match gate ALONE gets only 600/400 — it
			// reclaims the consumed box and leaks every superseded one — so this
			// row balancing is what distinguishes the complete fix from that
			// partial one.
			name: "reassigned_matched",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(Some(i + 1)); }
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 80,
		},
		{
			// THREE rebinds, the last a `Some(None)`, so the assign-path release
			// runs repeatedly and over an inner that owns nothing. Every
			// superseded box is released at its own assignment; native agrees at
			// 700/700.
			name: "reassigned_matched_multi_rebind",
			src: `function round(i: i32): i32 {
    let o: Option[Option[i32]] = Some(Some(i));
    o = Some(Some(i + 1));
    if (i % 2 == 0) { o = Some(Some(i + 2)); }
    o = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19,
		},
		{
			// A rebind ALIASING an inner box the function still reads.
			// Releasing it at the next rebind would free a box under a live
			// reference.
			name: "refuses_rebind_aliasing_inner",
			src: `function round(i: i32): i32 {
    let keep: Option[i32] = Some(i);
    let o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(keep); }
    match (keep) { Some(v) => { return v; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63,
		},
		{
			// THE rc-INNER SHAPE (#7718): outer box, inner box and string data are
			// all released by a GUARDED two-level walk, since an exit sweep knows
			// neither that the box is live nor which inner variant it holds.
			// Leaked, that was 800/0 live 22400.
			name: "rc_inner_unmatched",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some(w("ab")));
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			// The inner-tag guard is what this row is for: the inner is statically
			// None, so there is no string to release, and an unguarded walk would
			// hand __fern_str_free whatever the payload word holds. Was 400/0.
			name: "rc_inner_none_payload",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(None);
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13,
		},
		{
			// Was 800/400 live 6400 — the two option boxes freed, the STRING's own
			// two blocks stranded, with live_bytes scaling by the string's LENGTH
			// (19200 for the same shape with a 64-char suffix at identical counts).
			// The cause was not this analysis: the arm RETURNS, and the return-path
			// sweep re-encoded the release from the payload free fn alone, which
			// cannot tell a nested-Option payload from a flat one (#7725).
			//
			// This is the row that covers the inner __fern_str_free actually
			// firing. The literal-inner row below does not: a literal allocates
			// nothing, so its 400 allocs are the two option boxes alone.
			name: "rc_inner_matched_balances",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some(w("ab")));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19,
		},
		{
			// The LITERAL-inner control for the row above. It balances — but note
			// what it does and does not cover: a literal is not heap-allocated, so
			// its 400 allocs are the two option BOXES alone and the inner
			// __fern_str_free never has anything to free. It pins the box release
			// on the matched side; it says nothing about the string release, which
			// is what the producer row above loses.
			name: "rc_inner_matched_literal",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some("ab"));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 68,
		},
		{
			// The matched `Some(None)` rc-inner shape: the consuming match releases
			// both boxes through the GUARDED walk, whose tag check keeps it from
			// handing the free fn the payload word of a None inner. Leaked, that
			// was 400/0.
			name: "rc_inner_matched_none",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19,
		},
		{
			// The inner box is ALIASED from a local the function still
			// reads. Releasing it would free a box under a live reference.
			name: "refuses_aliased_inner",
			src: `function round(i: i32): i32 {
    let inner: Option[i32] = Some(i);
    let o: Option[Option[i32]] = Some(inner);
    match (inner) { Some(v) => { return v; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63,
		},
	}
}

// TestSelfHostUnmatchedOptoptX86_64 — an unmatched nested-Option local reclaims
// both boxes, and an aliased rc inner payload is never freed under its alias.
func TestSelfHostUnmatchedOptoptX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range unmatchedOptoptCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "unmoptopt_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the inner box "+
					"released under a live reference)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}
		})
	}
}

// TestSelfHostUnmatchedOptoptWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostUnmatchedOptoptWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping unmatched nested-Option wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range unmatchedOptoptCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "unmoptopt_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("unmatched nested-Option wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostUnmatchedOptoptIRArm64 — the arm64 sibling under qemu.
func TestSelfHostUnmatchedOptoptIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range unmatchedOptoptCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "unmoptopt_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("unmatched nested-Option arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

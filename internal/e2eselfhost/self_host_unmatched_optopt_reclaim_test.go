package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An UNMATCHED Option[Option[T]] local leaks both boxes (#7714) -----------
//
// The last quadrant gap in the Option reclaim family, after #7710 (matched
// Option[string]) and #7712 (reassigned Option[string]) — and the MIRROR IMAGE
// of those: there `matched` was the hole and `unmatched` worked; here `matched`
// works and `unmatched` was the hole.
//
//	Option[Option[i32]]        native      self-host (before)
//	matched                    400/400/0   400/400/0
//	matched via Some(_)        400/400/0   400/400/0
//	UNMATCHED                  400/400/0   400/0  live 16000
//
// 80 B/round, unbounded, both boxes. The matched case is covered by the
// consuming-match analysis and balances even with no arm binding at all, so the
// binding was never the releaser — which is what identified the unmatched
// collector as the gap.
//
// NO NEW EMITTER. emit_optarr_deep_free releases an Option local as: null-guard,
// then on the Some tag __fern_rc_dec the PAYLOAD, then __fern_rc_dec the BOX. For
// Option[Option[T]] the payload IS the inner option box — an rc-headered block
// owning no pointer — so that flat dec is already its complete release. Only the
// credit was missing, exactly as the Result spelling already rides the same
// emitter.
//
// THE SCALAR-INNER GATE IS LOAD-BEARING, and it decides which RELEASE a shape
// gets rather than whether it gets one. A flat dec frees the inner box but
// nothing the box owns, so an rc inner payload must not ride it — that one takes
// the guarded two-level walk instead (#7718, the `rc_inner_*` rows). Crediting a
// slot under both tags would free the same box twice, which is why the two
// collectors are disjoint by annotation: nested_opt_inner_freefn answers
// non-empty exactly where type_is_scalar_union answers false.
//
// #7716 then admits a REASSIGNED nested-Option local whose every rebind
// allocates its own inner box, via opt_rebinds_all_fresh and an "optopt" kind —
// the same route #7712 took for strings. That fixes the UNMATCHED half only:
// the matched half never reaches this collector, whose escape gate reads a
// bare-ident match scrutinee as an escape, and belongs to the consuming-match
// family instead. `reassigned_matched_still_leaks` pins that split, so the two
// are not mistaken for one class later.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run under
// test, and every row is sanitizer-clean under FERN_SANITIZE=1.
type unmatchedOptoptCase struct {
	name      string
	src       string
	want      int
	balance   bool
	wantFrees int64 // asserted exactly on every row that does not set balance
}

const unmatchedOptoptMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

func unmatchedOptoptCases() []unmatchedOptoptCase {
	return []unmatchedOptoptCase{
		{
			// THE REPRO. Was 400/0 live 16000.
			name: "unmatched_nested_option",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(Some(i));
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13, balance: true,
		},
		{
			// The None-payload spelling of the same construction.
			name: "unmatched_nested_none",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(None);
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13, balance: true,
		},
		{
			// Matched: covered by the consuming-match analysis before this change
			// and still covered. Must not be credited TWICE — a second release is
			// an over-release, caught by the 99 guard rather than by any byte count.
			name: "matched_control",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(Some(i));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63, balance: true,
		},
		{
			// Matched with NO arm binding — the row that showed the binding was
			// never the releaser.
			name: "matched_wildcard_control",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(Some(i));
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 4, balance: true,
		},
		{
			// THE REBIND HALF (#7716): reassigned, every rebind allocating its own
			// inner box. Was 600/0 live 24000.
			name: "reassigned_unmatched",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(Some(i + 1)); }
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13, balance: true,
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
    var o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(Some(i + 1)); }
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 80, balance: true,
		},
		{
			// THREE rebinds, the last a `Some(None)`, so the assign-path release
			// runs repeatedly and over an inner that owns nothing. Every
			// superseded box is released at its own assignment; native agrees at
			// 700/700.
			name: "reassigned_matched_multi_rebind",
			src: `function round(i: i32): i32 {
    var o: Option[Option[i32]] = Some(Some(i));
    o = Some(Some(i + 1));
    if (i % 2 == 0) { o = Some(Some(i + 2)); }
    o = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v; }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19, balance: true,
		},
		{
			// REFUSED: a rebind ALIASING an inner box the function still reads.
			// Releasing it at the next rebind would free a box under a live
			// reference — the freshness requirement, load-bearing because the
			// payload is stored uncounted.
			name: "refuses_rebind_aliasing_inner",
			src: `function round(i: i32): i32 {
    var keep: Option[i32] = Some(i);
    var o: Option[Option[i32]] = Some(Some(i));
    if (i % 2 == 0) { o = Some(keep); }
    match (keep) { Some(v) => { return v; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63, wantFrees: 0,
		},
		{
			// THE rc-INNER SHAPE (#7718): what the scalar-inner gate used to refuse
			// outright. Was 800/0 live 22400 — outer box, inner box and string data
			// all leaked. Released now by emit_optopt_rc_deep_free, a GUARDED
			// two-level walk: the existing emit_nested_opt_payload_drop carries "no
			// null / tag guard" by design and runs only where the box is known live
			// and the inner variant known, neither of which an exit sweep has.
			name: "rc_inner_unmatched",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var o: Option[Option[string]] = Some(Some(w("ab")));
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13, balance: true,
		},
		{
			// The inner-tag guard is what this row is for: the inner is statically
			// None, so there is no string to release, and an unguarded walk would
			// hand __fern_str_free whatever the payload word holds. Was 400/0.
			name: "rc_inner_none_payload",
			src: `function round(i: i32): i32 {
    var o: Option[Option[string]] = Some(None);
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13, balance: true,
		},
		{
			// STILL LEAKING, deliberately, and one of THREE matched rc-inner shapes
			// that behave differently — the split is recorded on #7718:
			//
			//	Some(Some("ab"))     literal inner    400/400/0   BALANCED
			//	Some(Some(w("ab")))  producer inner   800/400     this row
			//	Some(None)           no inner payload 400/0       rc_inner_matched_none
			//
			// So the machinery works; what the producer form loses is the string's
			// own two blocks, which is the shape of a freshness-proof gap rather
			// than a missing release. Pinned so it cannot drift into an
			// over-release while that is worked.
			name: "rc_inner_matched_still_partial",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var o: Option[Option[string]] = Some(Some(w("ab")));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19, wantFrees: 400,
		},
		{
			// The LITERAL-inner control for the row above: same shape, inner string
			// a literal rather than a producer result, and it BALANCES. This is what
			// says the two-level release is present and working on the matched side,
			// so the producer form's loss is about proving the inner fresh.
			name: "rc_inner_matched_literal",
			src: `function round(i: i32): i32 {
    var o: Option[Option[string]] = Some(Some("ab"));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 68, balance: true,
		},
		{
			// STILL LEAKING: the matched `Some(None)` rc-inner shape frees NOTHING
			// (400/0, 16000, doubling with rounds), where the UNMATCHED spelling of
			// the same program balances via #7718's guarded walk. Unbounded and
			// sanitizer-clean, so it is an under-release, and it was unpinned until
			// now — this row is what stops it drifting into an over-release.
			name: "rc_inner_matched_none",
			src: `function round(i: i32): i32 {
    var o: Option[Option[string]] = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 19, wantFrees: 0,
		},
		{
			// REFUSED: the inner box is ALIASED from a local the function still
			// reads. Releasing it would free a box under a live reference — the
			// family's freshness requirement, load-bearing because the payload is
			// stored uncounted.
			name: "refuses_aliased_inner",
			src: `function round(i: i32): i32 {
    var inner: Option[i32] = Some(i);
    var o: Option[Option[i32]] = Some(inner);
    match (inner) { Some(v) => { return v; }, None => { return 2; } }
    return 0;
}` + unmatchedOptoptMain,
			want: 63, wantFrees: 0,
		},
	}
}

// TestSelfHostUnmatchedOptoptX86_64 — an unmatched nested-Option local reclaims
// both boxes, and an rc inner payload stays refused.
func TestSelfHostUnmatchedOptoptX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}
			if !tc.balance && frees != tc.wantFrees {
				t.Errorf("%s: %s — want exactly %d frees. A HIGHER count is the "+
					"refusal breaking down: an rc inner payload stranded by a flat "+
					"dec, or an aliased inner box freed under a live reference",
					tc.name, summary, tc.wantFrees)
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range unmatchedOptoptCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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

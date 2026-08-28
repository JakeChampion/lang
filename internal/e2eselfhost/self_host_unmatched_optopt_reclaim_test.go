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
// THE SCALAR-INNER GATE IS LOAD-BEARING. A flat dec frees the inner box but
// nothing the box owns, so an rc inner payload would be STRANDED. `Option
// [Option[string]]` therefore stays uncredited and keeps leaking rather than
// being half-released — pinned below, because admitting it here would look like
// an improvement while quietly dropping the string.
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
			// REFUSED, and the reason the scalar-inner gate exists: a flat dec
			// would free the inner box and STRAND its string. Left leaking rather
			// than half-released; it wants the deep release, which is its own
			// change. A rise here is that gate breaking down.
			name: "refuses_rc_inner_payload",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var o: Option[Option[string]] = Some(Some(w("ab")));
    return i % 7;
}` + unmatchedOptoptMain,
			want: 13, wantFrees: 0,
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

package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// General enum->enum cross-local box reuse (Perceus FBIP): a `var c = V([..])`
// construction of a uniform-layout array-payload enum reuses the heap box of an
// EARLIER, same-enum donor `a = W([..])` that is DEAD, non-escaping, and provably
// non-aliased by the construction site (enum_only_wildcard_used_rec). Instead of
// allocating a fresh box for c, the reuse RELEASES a's old payload arrays, re-shapes
// a's box to V in place, writes V's fresh array payloads into it, binds c to it, and
// zeroes a's slot — so the box is freed exactly once, via c. Net: one fewer alloc +
// one fewer free per such construction.
//
// Each case embeds a value check (returns 90/91/99 on mismatch) and then returns
// __rc_underflow() — so want=0 means BOTH the reused value is correct AND no
// over-release occurred. A mis-balanced old-payload release or a bad reshape would
// double-free (detector > 0); a mis-freed donor payload poisoning the recycled buffer
// would surface as a wrong value (especially the -probe case that allocs after reuse).
var enumCrossReuseIRCases = []struct {
	name string
	src  string
	want int
}{
	// FIRES + value: dead donor `a = A([1,2])` (used once via a wildcard match, then
	// dead) reused for `c = B([3,4])`. c's payload [3,4] reads back through the box
	// (v = 3+4 = 7); a was variant A (t = 5). 5 + 7 = 12.
	{"fires-value", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var c: E = B([3, 4]); var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; }
function main(): i32 { return f(); }`, 12},
	// FIRES + detector (THE soundness gate): same reuse, assert value then read the
	// over-release detector. A mis-balanced old-payload release / bad reshape would
	// double-free -> detector > 0.
	{"fires-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var c: E = B([3, 4]); var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 99; } return __rc_underflow(); }
function main(): i32 { return f(); }`, 0},
	// CORRUPTION PROBE: a FRESH array allocated AFTER the reuse must read back intact
	// — a mis-freed donor payload (or a double-free recycling the block early) would
	// poison the recycled buffer. fresh = [11,22,33] -> 66; value still 12.
	{"corruption-probe-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var c: E = B([3, 4]); var fresh: i32[] = [11, 22, 33]; var fs: i32 = fresh[0] + fresh[1] + fresh[2]; var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 90; } if (fs != 66) { return 91; } return __rc_underflow(); }
function main(): i32 { return f(); }`, 0},
	// DONOR LIVE -> NO REUSE: the donor `a` is used AFTER c's construction (its
	// wildcard match sits after c), so a is NOT dead at c and the reuse must NOT fire.
	// The value stays correct via the normal fresh-alloc path; detector 0. t=3 (A),
	// v=8 (B) -> 11.
	{"donor-live-no-reuse-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { var a: E = A([1, 2]); var c: E = B([3, 4]); var t: i32 = 0; match (a) { A(_) => { t = 3; }, B(_) => { t = 4; } } var v: i32 = 0; match (c) { A(_) => { v = 7; }, B(_) => { v = 8; } } if (t + v != 11) { return 99; } return __rc_underflow(); }
function main(): i32 { return f(); }`, 0},
	// RAGGED ENUM -> NO REUSE: variants have differing field counts (A:1, B:2), so the
	// box is NOT uniform-layout (enum_all_variants_same_field_count fails) and the
	// reshape would be size-unsafe — reuse must NOT fire (falls back to fresh alloc).
	// Value correct, detector 0. t=5 (A), v=3+6=9 -> 14.
	{"ragged-no-reuse-detector", `enum E { A(i32[]), B(i32[], i32[]) }
function f(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_, _) => { t = 6; } } var c: E = B([3, 4], [5, 6]); var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w, x) => { v = w[0] + x[1]; } } if (t + v != 14) { return 99; } return __rc_underflow(); }
function main(): i32 { return f(); }`, 0},
}

// TestSelfHostEnumCrossReuseIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pinned to the "ir" path, and runs it — asserting the embedded value
// check plus __rc_underflow() == 0.
func TestSelfHostEnumCrossReuseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range enumCrossReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// enumCrossReuseFiresDeadDonor: a DEAD same-enum donor reused in place — the recipient
// box is NOT allocated, so 3 __fern_arr_box (donor box + donor [1,2] + recipient [3,4]).
const enumCrossReuseFiresDeadDonor = `enum E { A(i32[]), B(i32[]) } function main(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var c: E = B([3, 4]); var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; }`

// enumCrossReuseFiresLiveDonor: the donor `a` is read AFTER the recipient, so it is
// live at c and the reuse must NOT fire — both boxes are allocated (4 __fern_arr_box).
const enumCrossReuseFiresLiveDonor = `enum E { A(i32[]), B(i32[]) } function main(): i32 { var a: E = A([1, 2]); var c: E = B([3, 4]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; }`

// TestSelfHostEnumCrossReuseFiresX86_64 proves the enum->enum cross-reuse actually
// lowers in place: a dead same-enum donor yields ONE FEWER box alloc (its box is
// reused by the recipient) than the same program with the donor read after the
// recipient. Guards against the analysis silently regressing to a no-op that stays
// correct only because a fresh alloc is also correct.
func TestSelfHostEnumCrossReuseFiresX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	countAllocs := func(prog string) int {
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		return countUserArrBoxAllocs(asm)
	}
	if got := countAllocs(enumCrossReuseFiresDeadDonor); got != 3 {
		t.Errorf("dead enum donor: got %d box allocs, want 3 (reuse should fire)", got)
	}
	if got := countAllocs(enumCrossReuseFiresLiveDonor); got != 4 {
		t.Errorf("live enum donor: got %d box allocs, want 4 (reuse must NOT fire)", got)
	}

	// #4350 slice 5: the firing site is RUNTIME-GUARDED — the emitted asm must
	// carry the uniqueness probe and the token-degrade allocator (reused =
	// __fern_rc_is_unique(a); box = __fern_alloc_reuse(reused ? a : 0, nf+1)).
	// The degrade arm is unreachable from any statically admitted program
	// (sole-owner donors only), so it is pinned structurally here and at scale
	// by the self-compile fixpoints.
	asm := string(runCapture(t, gcc, runner, driverBin, []byte(enumCrossReuseFiresDeadDonor)))
	if !strings.Contains(asm, "call __fn___fern_rc_is_unique") {
		t.Error("enum-cross reuse site emitted no __fern_rc_is_unique guard")
	}
	if !strings.Contains(asm, "call __fn___fern_alloc_reuse") {
		t.Error("enum-cross reuse site emitted no __fern_alloc_reuse token-degrade call")
	}
}

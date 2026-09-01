package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A CALL-bound rc-enum earns the consuming-match free too ------------------
//
// `var v: E = mkv(i)` followed by a sole top-level consuming match reclaimed
// NOTHING — 200 allocs / 0 frees over 100 rounds against native's 200/200 —
// while the byte-identical shape with the constructor written INLINE was flat.
//
// Two passes decide this shape and they disagreed. collect_fresh_rcenum_names
// resolves the init with fresh_rcpayload_enum_init OR rcenum_call_init_owner (the
// "RCE:" registry of whole-program-proven fresh rc-enum ctor fns), and grants the
// "RCENUM:" credit — the one that SUPPRESSES the exit sweep. Its emission-side
// twin consumed_rcpayload_enum_frees, which places the free the credit assumes,
// resolved with fresh_rcpayload_enum_init alone. So the call bind lost its sweep
// and never got the free that was supposed to replace it.
//
// The registry proof is strictly stronger than the inline test it now joins:
// body_has_nonqualifying_rcenum_return requires EVERY return to satisfy
// fresh_rcpayload_enum_init against the strict (empty) fresh-string set, plus
// rcenum_ctor_payload_strings_fresh on top. A registered call therefore hands
// over exactly the sole-owned chain an inline ctor does.
//
// This is the rc-payload user-enum analogue of #6360, which made the same
// admission for scalar Option/Result (self_host_call_bound_enum_reclaim_test.go).
//
// Every want was confirmed against the native x86-64 backend. Exit 99 is
// reserved for __rc_underflow_count().
//
// Counts here are ONE block per heap string: #7351 fused the box into the
// buffer's reserved header. Every row was re-measured against the commit
// before it, and every live_bytes is unchanged — the clean rows stayed clean
// and each refusal-leak row leaks the same bytes — so what moved is block
// volume, not behaviour. A pre-fusion number in a row note below is the older
// one.

type rcenumCallFreeCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const recfMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func rcenumCallFreeCases() []rcenumCallFreeCase {
	const decls = `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	return []rcenumCallFreeCase{
		{
			// THE REPRO. Base: 200 allocs / 0 frees.
			name: "call_bound_sole_match",
			src: decls + `function round(i: i32): i32 {
    var v: E = mkv(i);
    var a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// The INLINE ctor bind, which was already flat. It is the control that
			// says the difference was the call and not the match.
			name: "inline_ctor_sole_match_unchanged",
			src: decls + `function round(i: i32): i32 {
    var v: E = E.A([i, i + 1]);
    var a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 200, frees: 200,
		},
		{
			// A REBOUND call-bound name. consumed_rcpayload_enum_frees checked its
			// rebind gate (all_assigns_fresh_rcenum) against an EMPTY registry while
			// the credit side passed the real one, so every assignment from a call
			// failed the same way the init did. Base: 400 / 200 — the rebind release
			// fired via the credit, the final value's free did not.
			name: "call_bound_rebound",
			src: decls + `function round(i: i32): i32 {
    var v: E = mkv(i);
    v = mkv(i + 3);
    var a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len() + xs[0]; }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 2, allocs: 400, frees: 400,
		},
		{
			// Call-bound inside a LOOP body, which is the block-level sibling of the
			// pass (lower_block runs it per block). Base: 400 / 200.
			name: "call_bound_in_loop_block",
			src: decls + `function round(i: i32): i32 {
    var a: i32 = 0;
    var k: i32 = 0;
    while (k < 2) {
        var v: E = mkv(i + k);
        match (v) { E.A(xs) => { a = a + xs.len(); }, E.B => { a = a; } }
        k = k + 1;
    }
    return a % 101;
}
` + recfMain,
			want: 12, allocs: 400, frees: 400,
		},
		{
			// A STRING payload through a registered producer. The deep drop reaches
			// __fern_str_free here, so this is the row that proves the registry's
			// extra string gate (rcenum_ctor_payload_strings_fresh) is carrying the
			// freshness the free needs. Base: 300 / 0.
			name: "call_bound_string_payload",
			src: `enum T { W(string), N }
function mkt(i: i32): T { return T.W("ab" + "cd"); }
function round(i: i32): i32 {
    var v: T = mkt(i);
    var a: i32 = 0;
    match (v) { T.W(s) => { a = s.len(); }, T.N => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 12, allocs: 200, frees: 200,
		},
		{
			// A STRUCT payload through a registered producer — the deep drop runs
			// __struct_drop_<P> on the payload. Base: 300 / 0.
			name: "call_bound_struct_payload",
			src: `struct P { xs: i32[] }
enum S { V(P), N }
function mks(i: i32): S { return S.V(P { xs: [i, i + 1] }); }
function round(i: i32): i32 {
    var v: S = mks(i);
    var a: i32 = 0;
    match (v) { S.V(p) => { a = p.xs.len(); }, S.N => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 300, frees: 300,
		},
		{
			// The payload read back as a VALUE with three fresh arrays allocated
			// after the match, so a payload freed too early would be reused before
			// it is read. Counts and the underflow guard are both blind to a
			// use-after-READ — #7505 was exactly that, and passed them plus
			// FERN_SANITIZE=1. Native returns 9.
			//
			// The modulus is 97 because the wasm leg reads the value through the
			// exit code and WASI rejects a status outside [0, 126).
			name: "payload_read_back_after_churn",
			src: `enum E { A(i32[]), B }
function mkv(): E { return E.A([7, 8]); }
function round(i: i32): i32 {
    var v: E = mkv();
    var a: i32 = 0;
    match (v) { E.A(xs) => { a = xs[0] + xs[xs.len() - 1]; }, E.B => { a = 0; } }
    var j1: i32[] = [111, 222];
    var j2: i32[] = [333, 444];
    var j3: i32[] = [555, 666];
    return a + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9, allocs: 100, frees: 100,
		},
		{
			// THE NEGATIVE CONTROL. `passthru` returns its PARAM, so every return is
			// not a fresh direct construction and the fn never enters the "RCE:"
			// registry — the call bind stays unresolved and nothing is freed here.
			// 200 / 0, unchanged by this slice: it leaks, which is the safe
			// direction, and native reclaims it through a different admission.
			//
			// If this row ever reports frees > 0 without a matching admission, the
			// widening has reached a producer that hands back a box it does not own.
			name: "non_registered_producer_refused",
			src: `enum E { A(i32[]), B }
function passthru(e: E): E { return e; }
function round(i: i32): i32 {
    var s: E = E.A([i, i + 1]);
    var v: E = passthru(s);
    var a: i32 = 0;
    match (v) { E.A(xs) => { a = xs.len(); }, E.B => { a = 0; } }
    return a % 101;
}
` + recfMain,
			want: 6, allocs: 200, frees: 0,
		},
		{
			// A GUARDED arm whose payload is stored out. consumed_rcpayload_enum_frees
			// refuses any candidate mixing a guard with a NON-EMPTY moved set
			// (guarded_move), because a guard could divert execution to an arm that
			// did not move the payload. The moved-set narrowing empties the set for
			// this shape — an rc-guarded array payload stored to an outer local is
			// not a hand-over — so the candidate is admitted and the free fires.
			// 350/150 when this row was written; native parity now.
			name: "guarded_arm_store_reclaimed",
			src: decls + `function round(i: i32): i32 {
    var v: E = mkv(i);
    var keep: i32[] = [0];
    match (v) { E.A(xs) when i % 2 == 0 => { keep = xs; }, E.A(ys) => { keep = [ys.len()]; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + recfMain,
			want: 81, allocs: 350, frees: 350,
		},
	}
}

// TestSelfHostRcEnumCallBoundFreeX86_64 is the leak-accounting leg.
func TestSelfHostRcEnumCallBoundFreeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range rcenumCallFreeCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rcecall_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a release ran "+
					"under a live claim)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER means the call bind stopped "+
					"resolving through the \"RCE:\" registry; MORE on a refused row "+
					"means the admission reached past its gate", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostRcEnumCallBoundFreeWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostRcEnumCallBoundFreeWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping rc-enum call-bound free wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range rcenumCallFreeCases() {
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
			watFile := filepath.Join(dir, "rcecall_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("rc-enum call-bound free wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostRcEnumCallBoundFreeIRArm64 — the arm64 sibling under qemu.
func TestSelfHostRcEnumCallBoundFreeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range rcenumCallFreeCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "rcecall_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostEnumStrPayloadReclaimIRX86_64 pins the #4355 slice-2 gap: enum and
// Option/Result STRING payloads now release on the self-host IR path. A FRESH
// string payload (a literal or a fresh producer — gated by
// variant_struct_payloads_fresh / rcpayload_option_cand) makes the box the
// payload's sole owner, so the consumed-by-match free releases it via the
// rc-aware __fern_str_free (per-variant variant_is dispatch for enums —
// emit_enum_variant_drops' string arm; op_opt_payload for options —
// emit_opt_str_payload_drop) before the box dec. Non-fresh payloads (a bare
// ident aliasing a live local) and escaping arm bindings keep today's sound
// leak.
//
// The reclaim is proven by a BOUNDED HIGH-WATER assertion (__heap_bump_bytes()
// stays flat across a second 5000-iteration churn), a double-free by the
// over-release detector (__rc_underflow() → 99), and values checked in Fern.
// Probes use the IR-path builtins (__rc_underflow / __heap_bump_bytes) so the
// programs stay on the IR path.
func TestSelfHostEnumStrPayloadReclaimIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = heap bump grew → payload not reclaimed; 99 = over-release; 97 = value corrupted)", name, code, want)
		}
	}

	// ENUM string payload, consumed by match: `Word(pre + "abc")` is a fresh
	// concat, so the box solely owns it; the consumed-free's Word variant_is arm
	// __fern_str_free's the payload then decs the box. After a 5000-iteration
	// warmup a second churn re-serves everything from the freelist — the bump
	// stays flat (< 256 B). A double free ticks the detector → 99.
	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string): i32 { var x = Word(pre + "abc"); var r = 0; match (x) { Word(s) => { r = s.len(); }, Num(n) => { r = n; }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"enum-str-payload-flat", 0)

	// OPTION string payload (`Option[string] = Some(<fresh concat>)`), consumed
	// by match: emit_opt_str_payload_drop frees the payload (op_opt_payload →
	// __fern_str_free) then the box. Flat across the second churn.
	run(t, `function go(pre: string): i32 { var o: Option[string] = Some(pre + "xyz"); var r = 0; match (o) { Some(s) => { r = s.len(); }, None => { r = 1; }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"option-str-payload-flat", 0)

	// RESULT Err string payload (variant-aware opt_payload_type reads E for Err).
	run(t, `function go(pre: string): i32 { var r2: Result[i32, string] = Err(pre + "e"); var r = 0; match (r2) { Ok(v) => { r = v; }, Err(e) => { r = e.len(); }, } return r; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"result-err-str-payload-flat", 0)

	// NON-FRESH payload excluded: `Some(nm)` aliases the live local nm — the
	// classifier rejects it (leak), so nm reads valid bytes after the match and
	// nothing double-frees. lens 3 + 3 = 6 over 2000 calls, detector 0.
	run(t, `function go(pre: string): i32 { var nm: string = pre + "q"; var o: Option[string] = Some(nm); var r = 0; match (o) { Some(s) => { r = s.len(); }, None => { r = 1; }, } return r + nm.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (go(pre) != 6) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"option-aliased-str-payload-excluded", 0)

	// ESCAPING arm binding via RETURN — ADMITTED under the Koka consuming-match
	// lift (#4400): `Word(s) => return s` hands the payload to the caller, so
	// the candidate is now accepted and the free site SKIPS the Word payload's
	// dec (match_moved_rc_payloads / emit_enum_variant_drops_moved — ownership
	// moved to the returned value). The returned string must stay valid and
	// nothing may double-free. len 5 over 2000 calls, detector 0.
	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string): string { var x = Word(pre + "abc"); match (x) { Word(s) => { return s; }, Num(n) => { return "n"; }, } return ""; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (go(pre).len() != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"enum-escaping-binding-return-moved", 0)

	// ESCAPING arm binding via OUTER-VAR STORE — the shape the pre-#4400 gate
	// rejected outright (box + payload both leaked per call). Now the candidate
	// is admitted: the box is deep-drop-freed right after the match with the
	// Word payload's dec SKIPPED, so `out` (which aliases the payload) reads
	// valid bytes AFTER the free site. A skipped-dec mistake here shows up as
	// 99 (the str_free/dec underflow detector) or a corrupted length.
	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string): i32 {
    var out: string = "";
    var x = Word(pre + "abc");
    match (x) { Word(s) => { out = s; }, Num(n) => { out = "n"; }, }
    return out.len();
}
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (go(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"enum-escaping-binding-store-moved", 0)

	// GUARD + MOVE mix REJECTED (the #4560 review point): the escaping Word
	// arm sits behind a guarded sibling — a guard-true run would take the
	// borrow-only arm while the moved-set skip still suppressed the payload
	// dec (a per-call leak). Admission rejects the mix (guarded_move), so the
	// candidate falls back to the exit sweep: values stay correct on both
	// guard outcomes and the detector stays 0 (no skip fired, no over-release).
	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string, k: i32): i32 {
    var out: string = "";
    var x = Word(pre + "abc");
    match (x) { Word(s) when k > 0 => { out = s[0:2]; }, Word(s) => { out = s; }, Num(n) => { out = "n"; }, }
    return out.len();
}
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (go(pre, 1) != 2) { bad = 1; } if (go(pre, 0) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"enum-guarded-move-mix-rejected", 0)

	// NESTED-if consuming match (the "opt-strpayload" precise-drop kind): the
	// shape is not admitted by the current precise-drop gates (same as the
	// pre-existing array-payload behavior — the box leaks soundly), so assert
	// correctness + detector only, no flatness.
	run(t, `function go(pre: string, k: i32): i32 { var o: Option[string] = Some(pre + "x"); var t = 0; if (k < 2) { match (o) { Some(s) => { t = s.len(); }, None => { t = 0; }, } } return t; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (go(pre, 1) != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"option-str-nested-if-sound", 0)
}

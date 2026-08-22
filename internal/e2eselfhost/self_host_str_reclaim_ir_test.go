package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strReclaimIRCases pin the reclamation of fresh, non-escaping, non-aliased heap
// STRING locals on the self-hosted stack-IR path (#2649 string RC). A self-host
// string is a header-less 16-byte box {data@0,len@8} + a separate __fern_alloc'd
// data buffer (on the asm backends). Unreclaimed that leaks one box + buffer
// per iteration, where the native backend frees it. The self-host
// classifies `var s: string = <fresh producer>` (concat / .to_ascii_upper()/.to_ascii_lower()/
// .reverse()/.repeat(n) / chr / string_from_bytes_unchecked / str_to_* / __raw_string) that
// never escapes (body_unsafe_for) and is never reassigned as reclaimable, then
// frees it via __fern_str_free (box + data buffer) at the loop-rebind and at scope
// exit. A literal / bare-ident / .trim() / .replace() binding is NOT fresh (may
// alias) and stays leaked (sound).
//
// Two contracts per case:
//   - exit code pins VALUE correctness (a double-free would corrupt the freelist
//     and crash / return garbage; a leak would still be correct but not flat);
//   - reclaimAssert pins the EMISSION: a `call __fn___fern_str_free` (the box +
//     buffer release) is required (>=1) or forbidden (0, escaping) as noted.
var strReclaimIRCases = []struct {
	name        string
	src         string
	expected    int
	mustReclaim bool
	// scope names the ONE user function the count is taken in. "" counts the
	// whole program, which is the same thing for a single-`main` case; a case
	// whose contract is about which SIDE of a call reclaims has to name it.
	scope string
}{
	// Loop-body fresh concat, used read-only (s.len() is a borrow): reclaimed each
	// iteration. tag is a loop-invariant literal local (not itself reclaimable —
	// literals may alias). s = "row" + "!" = "row!" (len 4); sum over 4 iters = 16.
	{"loop-concat-nonescaping",
		`function main(): i32 { var tag: string = "row"; var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var s: string = tag + "!"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		16, true, ""},
	// Loop-body fresh .to_ascii_upper() (a fresh copy), non-escaping: reclaimed each iter.
	// base = "abc" (len 3); s.len() = 3; sum over 4 iters = 12.
	{"loop-to-upper-nonescaping",
		`function main(): i32 { var base: string = "abc"; var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var s: string = base.to_ascii_upper(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		12, true, ""},
	// Loop-body chr(..)+"x" concat: chr produces a fresh 1-char string, +"x" a fresh
	// 2-char one bound to s. s.len() = 2; sum over 4 iters = 8.
	{"loop-chr-concat",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var s: string = chr(65 + (i % 3)) + "x"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		8, true, ""},
	// Non-loop fresh string local, freed at scope exit. s = "hi" + "!" (len 3).
	{"scope-exit-concat",
		`function main(): i32 { var s: string = "hi" + "!"; return s.len(); }`,
		3, true, ""},
	// Memory-safety at scale: 5,000,000 iterations of a fresh-concat loop. A leaked
	// box + buffer per iteration would exhaust the heap; a double-free would corrupt
	// the freelist and crash / return garbage. exit 0 (sum kept mod 100) with the
	// reclaim present proves the balance (flat heap, no double-free).
	{"str-churn-safe",
		`function main(): i32 { var tag: string = "abcd"; var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var s: string = tag + "ef"; sum = (sum + s.len()) % 100; i = i + 1; } return sum; }`,
		0, true, ""},
	// An ALIASED fresh string (`var t = s`) IS reclaimed, and the alias is what
	// makes that safe: the bind retains the box, so s and t each hold a counted
	// reference and each releases it — the refcount frees it exactly once (#7282).
	// Before that pairing the alias cost s its credit and NOTHING was freed, which
	// is the leak this row used to pin as correct.
	//
	// The concat operands are PARAMS, which slot_is_reclaimable_str refuses at its
	// first line, so a reclaim anywhere in `mk` can only be s or t and the
	// aliased-RESULT contract stays isolated. Value stays correct: 3 + 3 = 6, and
	// an over-release would show as a wrong exit rather than this count.
	{"aliased-reclaimed-once",
		`function mk(a: string, b: string): i32 { var s: string = a + b; var t: string = s; return s.len() + t.len(); } function main(): i32 { return mk("ab", "c"); }`,
		6, true, "mk"},
	// NEGATIVE: a RETURNED fresh string escapes its producer → h must not free it.
	// The box is handed to the caller, and freeing it in h would leave main reading
	// dead bytes. The operands are PARAMS so nothing else in h is reclaimable, and
	// the count is scoped to h: main DOES release the result at the `h(...).len()`
	// read, because h is in the whole-program fresh-return registry — that
	// caller-side reclaim is the point of the registry, not a violation of this
	// case.
	{"returned-not-reclaimed",
		`function h(x: string, y: string): string { var s: string = x + y; return s; } function main(): i32 { return h("xy", "z").len(); }`,
		3, false, "h"},
	// ANNOTATED i32_to_string in a loop: reclaimed each iter. On the self-host the
	// helper boxes at an allocation boundary (unlike native's mid-buffer emitter),
	// so it is cleanly reclaimable. i in 0..12 → "0".."11"; sum of digit-lengths =
	// 10*1 + 2*2 = 14.
	{"loop-i32-to-string",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 12) { var s: string = i32_to_string(i); sum = sum + s.len(); i = i + 1; } return sum; }`,
		14, true, ""},
	// UN-ANNOTATED unambiguous producer (`var s = i32_to_string(i)`, inferred string):
	// reclaimed too — str_free_producer_ident admits it without the annotation, and
	// expr_is_str marks the slot. Same value as above (14).
	{"loop-unannotated-i32-to-string",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 12) { var s = i32_to_string(i); sum = sum + s.len(); i = i + 1; } return sum; }`,
		14, true, ""},
	// UN-ANNOTATED chr(..): reclaimed. s.len() == 1 each iter; sum over 5 = 5.
	{"loop-unannotated-chr",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5) { var s = chr(65 + i); sum = sum + s.len(); i = i + 1; } return sum; }`,
		5, true, ""},
	// UN-ANNOTATED concat (`var s = tag + "!"`, inferred string): reclaimed too —
	// the fresh gate is now syntax-only and the is_str type gate (set from the
	// type-aware expr_is_str) admits the actual string concat. Same as the
	// annotated case: "row!" len 4 × 4 = 16.
	{"loop-unannotated-concat",
		`function main(): i32 { var tag: string = "row"; var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var s = tag + "!"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		16, true, ""},
	// UN-ANNOTATED string method (`var s = base.to_ascii_upper()`): reclaimed. len 3 × 4 = 12.
	{"loop-unannotated-to-upper",
		`function main(): i32 { var base: string = "abc"; var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var s = base.to_ascii_upper(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		12, true, ""},
	// NEGATIVE: an un-annotated INT `var n = a + b` matches the concat SHAPE but is
	// not is_str, so it is never reclaimed (no __fern_str_free) and stays correct.
	// Ensures the syntax-only fresh gate is safely filtered by the is_str type gate.
	{"unannotated-int-add-not-reclaimed",
		`function main(): i32 { var a: i32 = 3; var b: i32 = 4; var n = a + b; return n; }`,
		7, false, ""},
	// i32_to_string churn at scale: reclaimed per iteration (flat heap; a double
	// free would corrupt the freelist and crash / return garbage). `ok` stays 0
	// because every decimal string has len >= 1, so exit 0 proves the balance.
	{"i32-to-string-churn-safe",
		`function main(): i32 { var ok: i32 = 0; var i: i32 = 0; while (i < 5000000) { var s: string = i32_to_string(i); if (s.len() < 1) { ok = 1; } i = i + 1; } return ok; }`,
		0, true, ""},
}

// TestSelfHostStrReclaimIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserting the exit code and that the fresh
// heap-string reclaim (call __fn___fern_str_free) is (or isn't) emitted.
func TestSelfHostStrReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range strReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// A `call __fn___fern_str_free` is the fresh-string box + buffer release.
			// The bare label `__fn___fern_str_free:` (the helper definition, always
			// emitted) is not a call, so counting the call form isolates the reclaim.
			reclaims := countUserStrFreeReclaims(asm)
			if tc.scope != "" {
				reclaims = countCallsInFn(asm, tc.scope, "__fn___fern_str_free")
			}
			if tc.mustReclaim && reclaims == 0 {
				t.Errorf("%s: expected a fresh-string reclaim (call __fn___fern_str_free), found none — the string leaks", tc.name)
			}
			if !tc.mustReclaim && reclaims != 0 {
				t.Errorf("%s: expected NO fresh-string reclaim (escaping/aliased), found %d — a double-free / UAF risk", tc.name, reclaims)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

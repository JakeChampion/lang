package e2ecompiler

import (
	"os/exec"
	"testing"
)

// strReclaimIRCases pin the reclamation of fresh, non-escaping, non-aliased heap
// STRING locals on the self-hosted IR path (#2649 string RC). A self-host
// string is a header-less 16-byte box {data@0,len@8} + a separate __fern_alloc'd
// data buffer (on the asm backends); unreclaimed that leaks one box + buffer per
// iteration. A `let s: string = <fresh producer>` (concat /
// .to_ascii_upper()/.to_ascii_lower()/.repeat(n) / string_from_bytes_unchecked /
// str_to_* / __raw_string) that never escapes and is never reassigned is freed
// via __fern_str_free (box + data buffer) at the loop-rebind and at scope exit.
// A literal / bare-ident / .trim() / .replace() binding is NOT fresh (may alias)
// and stays leaked (sound).
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
		`function main(): i32 { let tag: string = "row"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s: string = tag + "!"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		16, true, ""},
	// Loop-body fresh .to_ascii_upper() (a fresh copy), non-escaping: reclaimed each iter.
	// base = "abc" (len 3); s.len() = 3; sum over 4 iters = 12.
	{"loop-to-upper-nonescaping",
		`import "std/string";
function main(): i32 { let base: string = "abc"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s: string = base.to_ascii_upper(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		12, true, ""},
	// Loop-body call-result + "x" concat: the call produces a fresh 1-char string,
	// +"x" a fresh 2-char one bound to s. s.len() = 2; sum over 4 iters = 8.
	{"loop-call-result-concat",
		`import "std/i32";
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s: string = ((65 + (i % 3)) as u8).to_ascii_string() + "x"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		8, true, ""},
	// Non-loop fresh string local, freed at scope exit. s = "hi" + "!" (len 3).
	// "hi" goes through ids so the concat is built on the heap rather than folded
	// to a constant.
	{"scope-exit-concat",
		`@noinline function ids(s: string): string { return s; } function main(): i32 { let s: string = ids("hi") + "!"; return s.len(); }`,
		3, true, ""},
	// Memory-safety at scale: 5,000,000 iterations of a fresh-concat loop. A leaked
	// box + buffer per iteration would exhaust the heap; a double-free would corrupt
	// the freelist and crash / return garbage. exit 0 (sum kept mod 100) with the
	// reclaim present proves the balance (flat heap, no double-free).
	{"str-churn-safe",
		`function main(): i32 { let tag: string = "abcd"; let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let s: string = tag + "ef"; sum = (sum + s.len()) % 100; i = i + 1; } return sum; }`,
		0, true, ""},
	// An ALIASED fresh string (`let t = s`) IS reclaimed, and the alias is what
	// makes that safe: the bind retains the box, so s and t each hold a counted
	// reference and each releases it — the refcount frees it exactly once (#7282).
	//
	// The concat operands are borrowed PARAMS that `mk` never releases, so a
	// reclaim there can only be s or t and the aliased-RESULT contract stays
	// isolated. Value stays correct: 3 + 3 = 6, and an over-release would show
	// as a wrong exit rather than this count.
	{"aliased-reclaimed-once",
		`@noinline function mk(a: string, b: string): i32 { let s: string = a + b; let t: string = s; return s.len() + t.len(); } function main(): i32 { return mk("ab", "c"); }`,
		6, true, "mk"},
	// NEGATIVE: a RETURNED fresh string escapes its producer → h must not free it.
	// The box is handed to the caller, and freeing it in h would leave main reading
	// dead bytes. The operands are PARAMS so nothing else in h is reclaimable, and
	// the count is scoped to h: main DOES release the result at the `h(...).len()`
	// read, because h is in the whole-program fresh-return registry — that
	// caller-side reclaim is the point of the registry, not a violation of this
	// case.
	{"returned-not-reclaimed",
		`@noinline function h(x: string, y: string): string { let s: string = x + y; return s; } function main(): i32 { return h("xy", "z").len(); }`,
		3, false, "h"},
	// ANNOTATED i32 `.to_string()` in a loop: reclaimed each iter. On the self-host the
	// helper boxes at an allocation boundary (unlike native's mid-buffer emitter),
	// so it is cleanly reclaimable. i in 0..12 → "0".."11"; sum of digit-lengths =
	// 10*1 + 2*2 = 14.
	{"loop-i32-to-string",
		`import "std/i32";
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 12) { let s: string = i.to_string(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		14, true, ""},
	// UN-ANNOTATED unambiguous producer (`let s = i.to_string()`, inferred string):
	// reclaimed too. Same value as above (14).
	{"loop-unannotated-i32-to-string",
		`import "std/i32";
function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 12) { let s = i.to_string(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		14, true, ""},
	// UN-ANNOTATED concat (`let s = tag + "!"`, inferred string): reclaimed too,
	// by its inferred type. Same as the annotated case: "row!" len 4 × 4 = 16.
	{"loop-unannotated-concat",
		`function main(): i32 { let tag: string = "row"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s = tag + "!"; sum = sum + s.len(); i = i + 1; } return sum; }`,
		16, true, ""},
	// UN-ANNOTATED string method (`let s = base.to_ascii_upper()`): reclaimed. len 3 × 4 = 12.
	{"loop-unannotated-to-upper",
		`import "std/string";
function main(): i32 { let base: string = "abc"; let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let s = base.to_ascii_upper(); sum = sum + s.len(); i = i + 1; } return sum; }`,
		12, true, ""},
	// NEGATIVE: an un-annotated INT `let n = a + b` matches the concat SHAPE but is
	// not a string, so it is never reclaimed (no __fern_str_free) and stays correct.
	{"unannotated-int-add-not-reclaimed",
		`function main(): i32 { let a: i32 = 3; let b: i32 = 4; let n = a + b; return n; }`,
		7, false, ""},
	// i32 `.to_string()` churn at scale: reclaimed per iteration (flat heap; a double
	// free would corrupt the freelist and crash / return garbage). `ok` stays 0
	// because every decimal string has len >= 1, so exit 0 proves the balance.
	{"i32-to-string-churn-safe",
		`import "std/i32";
function main(): i32 { let ok: i32 = 0; let i: i32 = 0; while (i < 5000000) { let s: string = i.to_string(); if (s.len() < 1) { ok = 1; } i = i + 1; } return ok; }`,
		0, true, ""},
}

// TestSelfHostStrReclaimIRX86_64 compiles each case through the self-hosted x86-64
// load driver (asm_load_run), asserting the exit code and that the fresh
// heap-string reclaim (call __fn___fern_str_free) is (or isn't) emitted.
func TestSelfHostStrReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range strReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// A `call __fn___fern_str_free` is the fresh-string box + buffer release.
			// The bare label `__fn___fern_str_free:` (the helper definition, always
			// emitted) is not a call, so counting the call form isolates the reclaim.
			reclaims := countUserStrFreeReclaims(asm)
			if tc.scope != "" {
				reclaims = countCallsInFn(t, asm, tc.scope, "__fn___fern_str_free")
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

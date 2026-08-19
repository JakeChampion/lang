package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStrArrElemReclaimIRX86_64 pins the #4355 string[] ELEMENT reclaim
// slice: a non-escaping string[] local the frame solely owns — every stored
// element provably fresh (a concat, a proven producer call) or a static
// literal, or the whole array handed over by a "STRARR:" producer — is credited
// "SARR:" by reclaimable_names_of, and the exit sweep frees it with
// __fern_str_arr_free —
// the element-walking sibling of the shallow array dec (rc==1: __fern_str_free
// every element box, then the buffer; rc>1: dec; rc<0: skip; rc==0: underflow
// detector). Anything the element-hazard walk cannot prove keeps the shallow
// buffer-only dec (elements leak — sound).
//
// The reclaim is proven by a BOUNDED HIGH-WATER assertion (__heap_bump_bytes()
// stays flat across a second 5000-iteration churn — element leaks grow it by
// ~150 B/iter; the only admitted slack is the churn frame's own literal box, a
// known pre-existing gap measured at 24 B/call), a double-free by the
// over-release detector (__rc_underflow() → 99), and admission/exclusion by an
// asm-shape assertion on the __fn___fern_str_arr_free CALL site. All probes use
// the IR-path builtins (__rc_underflow / __heap_bump_bytes) so the program
// stays on the IR path — the AST-path spelling __rc_underflow would
// silently bail the function to the legacy emitter.
func TestSelfHostStrArrElemReclaimIRX86_64(t *testing.T) {
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

	// userCodeCalls reports whether a `call <helper>` appears inside a USER
	// function — i.e. under a `__fn_<name>:` label that is not itself a
	// runtime helper (`__fn___fern_*`). The runtime block emits helper
	// BODIES unconditionally, and since #4355 slice 9 the
	// __fn___fern_strarrarr_free body itself contains a
	// `call __fn___fern_str_arr_free` (its per-element inner walk), so a
	// naive whole-asm substring match reads every compile as "admitted".
	// Only a call site in user code is a reclaim-set admission.
	userCodeCalls := func(asm, helper string) bool {
		inUser := false
		for _, ln := range strings.Split(asm, "\n") {
			if strings.HasPrefix(ln, "__fn_") && strings.HasSuffix(ln, ":") {
				// The per-type RC helpers are compiler-generated runtime, not the
				// user's frame. __struct_drop_<T> / __field_reclaim_<T> element-walk
				// a `string[]` FIELD the strarrfld scan admitted, which is a
				// different reclaim decision from the SARR local credit these rows
				// measure — conflating them made a `Box { rows: xs }` case read as
				// "build element-walked xs" when build does nothing of the kind.
				inUser = !strings.HasPrefix(ln, "__fn___fern_") &&
					!strings.HasPrefix(ln, "__fn___struct_drop_") &&
					!strings.HasPrefix(ln, "__fn___field_reclaim_")
				continue
			}
			if inUser && strings.Contains(ln, "call "+helper) {
				return true
			}
		}
		return false
	}

	// wantCall: "yes" asserts the element-walk call IS emitted (the array was
	// admitted to the SARR set); "no" asserts it is NOT (the hazard walk
	// excluded it — the shallow dec still runs).
	run := func(t *testing.T, prog, name string, want int, wantCall string) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		hasCall := userCodeCalls(string(asm), "__fn___fern_str_arr_free")
		if wantCall == "yes" && !hasCall {
			t.Fatalf("%s: emitted asm has no __fn___fern_str_arr_free call — the string[] was not admitted to the SARR reclaim set", name)
		}
		if wantCall == "no" && hasCall {
			t.Fatalf("%s: emitted asm calls __fn___fern_str_arr_free — the element-hazard walk failed to exclude an aliased/escaping element", name)
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
			t.Errorf("%s exited %d, want %d (98 = heap bump grew → elements not reclaimed; 99 = over-release; 97 = value corrupted)", name, code, want)
		}
	}

	// RECLAIM, BOUNDED HIGH-WATER: xs is built from a literal + fresh concats
	// (a literal element's box is freed, its .rodata data heap-guard-skipped)
	// and grown via the sanctioned self-append rebind; elements are only read
	// transiently (xs[j].len()). Every build() exit frees 3 element boxes +
	// their heap buffers + the array buffer, so after a 5000-iteration warmup a
	// second 5000-iteration churn re-serves every allocation from the freelist:
	// the bump high-water moves < 256 B (measured: 24 B — churn's own literal
	// box, a pre-existing gap). Without the element walk it grows ~750 KB → 98.
	// A double-free ticks the underflow detector → 99. acc value checked (97).
	run(t, `function build(pre: string): i32 { var xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"strarr-elem-reclaim-flat", 0, "yes")

	// LOOP-BODY REINIT, BOUNDED HIGH-WATER (#4353 item 4): a string[] local
	// re-DECLARED each iteration of churn's own loop (not freed at a helper's
	// exit like the flat case above — freed at the loop REBIND). Pre-fix the
	// reinit store took the shallow buffer-only dec and leaked all 3 element
	// boxes + their buffers every iteration; the strarr reinit branch
	// (emit_strarr_reclaim_store) now frees the prior iteration's elements with
	// __fern_str_arr_free before the store, so the second churn re-serves from
	// the freelist and the bump high-water stays flat. Element leaks → 98; a
	// double-free (reinit + exit sweep both freeing the final box) → 99.
	run(t, `function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var xs: string[] = ["lit", pre + "x", pre + "yy"]; acc = (acc + xs[0].len() + xs[2].len()) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"strarr-elem-reinit-loop", 0, "yes")

	// ELEMENT ALIAS BINDING excludes: `var t = xs[0]` is a lasting element alias
	// the walk can't see through — xs must stay on the shallow buffer-only dec,
	// so t reads valid bytes after xs's sweep point and nothing double-frees.
	// lens 3+2 = 5 over 2000 calls, underflow 0 → exit 0.
	run(t, `function pick(pre: string): i32 { var xs: string[] = [pre + "x", "qq"]; var t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-alias-excluded", 0, "no")

	// RETURNED ELEMENT excludes: `return xs[0]` hands an element box to the
	// caller — xs must not element-walk (the shallow dec frees only the buffer,
	// so the returned box stays valid). len 3 over 2000 calls, underflow 0.
	run(t, `function first(pre: string): string { var xs: string[] = [pre + "z"]; return xs[0]; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (first(pre).len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-return-excluded", 0, "no")

	// PRODUCER-CALL ELEMENT, BOUNDED HIGH-WATER: the stored elements are calls
	// to `w`, a whole-program-proven fresh-string producer (str_fresh_ret_fns),
	// rather than inline concats. The credit's element proof is
	// strarr_value_is_fresh, the same question the "STRARR:" producer admission
	// asks, so the registry arm admits them and the exit sweep element-walks.
	// Before that the credit asked a registry-blind sibling that refused any
	// call, and all three element boxes leaked per build → 98.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { var xs: string[] = [w(pre), "lit"]; xs = xs.append(w(pre)); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w0: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-elem-producer-store-flat", 0, "yes")

	// LOCAL BOUND FROM A PRODUCER, BOUNDED HIGH-WATER: `var xs = mk(pre)` where
	// `mk` is a "STRARR:" registry function — its admission already proved
	// whole-program that every element of the returned array is a box `mk`
	// allocated and handed out at rc=1, so the frame owns them and the exit
	// sweep may element-walk. Previously only an array LITERAL initialiser
	// earned the credit, so this shape took the shallow buffer-only dec and
	// leaked every element → 98. `mk`'s own `out` still escapes by return and
	// keeps the shallow dec, so the elements are freed exactly once.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { var xs: string[] = mk(pre); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w0: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-local-from-producer-flat", 0, "yes")

	// BORROWED CALL ARG stays admitted: `take` only reads its parameter, so it
	// is borrowable and the array does not escape — the credit survives the
	// call and both the callee's reads and the post-call `xs[2]` read see live
	// bytes. 3 + 43 + 43 = 89 over 2000 calls, underflow 0.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function take(xs: string[]): i32 { return xs.len() + xs[0].len(); }
function build(pre: string): i32 { var xs: string[] = mk(pre); var k: i32 = take(xs); return k + xs[2].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-local-borrowed-arg-flat", 0, "yes")

	// STORED BY THE CALLEE excludes: `keep` puts the array in the struct it
	// returns, so the parameter is not borrowable and the array outlives
	// build's sweep point inside `b`. The credit must not fire — element-walking
	// here would free boxes `b.rows` still owns. Both the struct read and the
	// direct `xs[2]` read stay valid. 89 over 2000 calls, underflow 0.
	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): i32 { var xs: string[] = mk(pre); var b: Box = keep(xs); return b.rows.len() + b.rows[0].len() + xs[2].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-local-stored-by-callee-excluded", 0, "no")

	// FORWARDED RETURN excludes: `fwd` binds the producer's result and hands it
	// straight back out, so the array escapes its frame and the credit must not
	// fire. The caller reads it after fwd returns. 3 + 43 = 46, underflow 0.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fwd(pre: string): string[] { var xs: string[] = mk(pre); return xs; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var r: string[] = fwd(pre); if (r.len() + r[1].len() != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-local-forwarded-return-excluded", 0, "no")

	// SELF-`.with` REBIND, BOUNDED HIGH-WATER (#6407): `a = a.with(i, v)` on an
	// owned string[] lowers to an in-place arr_set, which used to drop the
	// overwritten element pointer on the floor — and, because the rebind was a
	// hazard, cost the array its credit as well, so ALL eight element boxes
	// leaked per round (380 B/round measured). lower_strarr_with_store now
	// releases the superseded box and retains the stored value, which makes the
	// rebind admissible: the sweep element-walks and the loop is flat. `v` is
	// another ELEMENT here, so without the retain the walk would free one box
	// through two slots → 99.
	run(t, `function mks(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function build(pre: string): i32 { var a: string[] = mks(pre); a = a.with(3, a[5]); return a.len() + a[3].len() + a[5].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w0: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-with-rebind-flat", 0, "yes")

	// `.with` VALUE IS A LIVE LOCAL: the store retains it, so the array and the
	// local each hold a counted reference — the local reads valid bytes after
	// the store and the element walk decs rather than frees. A long string is
	// built between the store and the reads so a wrongly freed block is really
	// recycled first. 37 + 37 + 23 correct, underflow 0.
	run(t, `function mks(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function churn(n: i32): string { var s: string = ""; var i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { var xs: string[] = mks(pre); var nm: string = pre + "-a-distinct-live-local-string-value"; xs = xs.with(1, nm); var junk: string = churn(20); if (junk.len() < 0) { return 0; } return nm.len() + xs[1].len() + xs[0].len(); }
function run2(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 97) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = run2(3000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-with-live-local-value-safe", 0, "yes")

	// `.with` SELF-STORE `a.with(i, a[i])`: the release is cow-guarded on the
	// old element differing from the stored value, so the store never frees the
	// pointer it is about to write. 23 correct over 3000 rounds, underflow 0.
	run(t, `function mks(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function churn(n: i32): string { var s: string = ""; var i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { var xs: string[] = mks(pre); xs = xs.with(3, xs[3]); var junk: string = churn(20); if (junk.len() < 0) { return 0; } return xs[3].len(); }
function run2(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 23) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = run2(3000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-with-self-store-safe", 0, "yes")

	// (A borrowed-element STORE case — `var xs: string[] = [nm]` — cannot be
	// exercised here: a bare-ident element in a string[] literal/append bails
	// the whole module to the AST emitter today, so the IR-path collector's
	// element-freshness gate is defence-in-depth for when that subset widens,
	// not a reachable shape.)
}

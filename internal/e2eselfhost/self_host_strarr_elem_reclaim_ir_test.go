package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStrArrElemReclaimIRX86_64 pins the #4355 string[] ELEMENT reclaim
// slice: a fresh, non-escaping string[] local whose every stored element is
// provably fresh (a concat) or a static literal is credited "SARR:" by
// reclaimable_names_of, and the exit sweep frees it with __fern_str_arr_free —
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
// stays on the IR path — the AST-path spelling __fern_rc_underflow_count would
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
				inUser = !strings.HasPrefix(ln, "__fn___fern_")
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
function main(): i32 { var w: i32 = churn(5000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(5000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
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
function main(): i32 { var w: i32 = churn(5000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(5000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
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

	// (A borrowed-element STORE case — `var xs: string[] = [nm]` — cannot be
	// exercised here: a bare-ident element in a string[] literal/append bails
	// the whole module to the AST emitter today, so the IR-path collector's
	// strarr_elem_store_ok gate is defence-in-depth for when that subset
	// widens, not a reachable shape.)
}

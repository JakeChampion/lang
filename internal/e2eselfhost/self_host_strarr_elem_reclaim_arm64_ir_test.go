package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostStrArrElemReclaimIRArm64 is the arm64 port of the #4355 string[]
// ELEMENT reclaim (x86 sibling: TestSelfHostStrArrElemReclaimIRX86_64). Under
// qemu the reclaim is proven by CORRECTNESS (a wrong free of a live element box
// corrupts the read-back / ticks the underflow detector → 99) plus an asm-shape
// assertion on the `bl __fn___fern_str_arr_free` CALL site — present when the
// SARR gate admits the array, absent when the element-hazard walk excludes it.
// Heavy heap-exhaustion churn is left to the x86 path (too slow under qemu).
func TestSelfHostStrArrElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	// userCodeCalls: a `bl <helper>` counts as a reclaim-set admission only
	// inside a USER function (`__fn_<name>:` label that isn't a runtime
	// `__fn___fern_*` body) — the __fn___fern_strarrarr_free runtime body
	// (#4355 slice 9) itself `bl`s __fn___fern_str_arr_free per inner, so a
	// whole-asm substring match would misread the runtime as an admission.
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
			if inUser && strings.Contains(ln, "bl "+helper) {
				return true
			}
		}
		return false
	}

	run := func(t *testing.T, prog, name string, want int, wantCall string) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		hasCall := userCodeCalls(string(asm), "__fn___fern_str_arr_free")
		if wantCall == "yes" && !hasCall {
			t.Fatalf("%s: emitted arm64 asm has no __fn___fern_str_arr_free call — the string[] was not admitted to the SARR reclaim set", name)
		}
		if wantCall == "no" && hasCall {
			t.Fatalf("%s: emitted arm64 asm calls __fn___fern_str_arr_free — the element-hazard walk failed to exclude an aliased/escaping element", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (99 = over-release)", name, code, want)
		}
	}

	// RECLAIM SHAPE + VALUE: fresh-element string[] (literal + concats, sanctioned
	// self-append) over 5000 build/drop cycles — the element walk must free each
	// element exactly once (a double free ticks the underflow detector → 99) and
	// the transient reads stay correct. lens 3+3+4 = 10.
	run(t, `function build(pre: string): i32 { var xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(5000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-reclaim-arm64", 0, "yes")

	// LOOP-BODY REINIT (#4353 item 4): a string[] re-DECLARED each iteration is
	// freed at the loop REBIND (emit_strarr_reclaim_store), not at a helper exit.
	// Correctness + over-release proof under qemu (the x86 sibling carries the
	// heap-exhaustion / bounded-high-water leg). xs[1]="abx" (3) + xs[2]="abyy"
	// (4) = 7 each iteration; a UAF from an early element free would read garbage
	// (bad=1) or tick the underflow detector (99). underflow 0 + value 7 → 0.
	run(t, `function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var xs: string[] = ["lit", pre + "x", pre + "yy"]; if (xs[1].len() + xs[2].len() != 7) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-reinit-loop-arm64", 0, "yes")

	// ELEMENT ALIAS BINDING excludes: `var t = xs[0]` — xs stays on the shallow
	// buffer-only dec, so t reads valid bytes and nothing double-frees. 3+2 = 5.
	run(t, `function pick(pre: string): i32 { var xs: string[] = [pre + "x", "qq"]; var t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-alias-excluded-arm64", 0, "no")

	// PRODUCER-CALL ELEMENT: the stored elements are calls to a proven
	// fresh-string producer rather than inline concats. The credit's element
	// proof is strarr_value_is_fresh, so the registry arm admits them; the
	// registry-blind sibling it replaced refused any call. Correctness +
	// over-release under qemu (the x86 sibling carries the flatness leg).
	// 43 + 3 + 43 = 89 each build.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { var xs: string[] = [w(pre), "lit"]; xs = xs.append(w(pre)); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-producer-store-arm64", 0, "yes")

	// LOCAL BOUND FROM A PRODUCER: `var xs = mk(pre)` where `mk` is a "STRARR:"
	// registry function — the frame owns every element the callee handed it, so
	// the exit sweep may element-walk. `mk`'s own `out` escapes by return and
	// keeps the shallow dec, so each element is freed exactly once. 3 + 43 = 46.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { var xs: string[] = mk(pre); return xs.len() + xs[1].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-local-from-producer-arm64", 0, "yes")

	// STORED BY THE CALLEE is ADMITTED, and this case used to pin the opposite.
	// Its premise was that `keep`'s parameter is not borrowable, which is still
	// true and is no longer the whole question: param_counted_of proves every
	// appearance of that parameter is a COUNTED store, so the construction incs
	// the buffer and the caller's claim survives the call. The "CNT:" tier
	// carries that verdict to the escape walker.
	//
	// Granting the DEEP walk on a shallow-release justification is the part that
	// needs stating. Two rules close it from both ends: __fern_str_arr_free is
	// rc-gated, so only the owner that finds rc 1 walks the elements; and no
	// element can be out UNCOUNTED, because the tier refuses ExprIndex for array
	// params while the caller's own element-hazard rules still exclude
	// `var t = xs[0]`. Both reads stay valid. 3 + 43 + 43 = 89.
	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): i32 { var xs: string[] = mk(pre); var b: Box = keep(xs); return b.rows.len() + b.rows[0].len() + xs[2].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-local-stored-by-callee-counted-arm64", 0, "yes")

	// The same store where the holder ESCAPES the frame that owns the array —
	// the shape the case above was written to fear, and the one that can
	// actually fail. `build` returns the Box, so the retain is still live when
	// `xs` sweeps: the walk runs, finds rc 2, and decs without touching an
	// element. Every element is read back AFTER 20 churn frames have recycled
	// the freelist; a wrong walk returns 100, a double free 99.
	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): Box { var xs: string[] = mk(pre); var b: Box = keep(xs); return b; }
function churnjunk(i: i32): i32 { var a: string[] = ["zzzz", "yyyy", "xxxx"]; return a[0].len() + a[2].len(); }
function round(i: i32): i32 {
    var pre: string = "ab";
    var b: Box = build(pre);
    var j: i32 = 0; var t: i32 = 0;
    while (j < 20) { t = t + churnjunk(j); j = j + 1; }
    var s: i32 = 0; var k: i32 = 0;
    while (k < b.rows.len()) { s = s + b.rows[k].len(); k = k + 1; }
    if (s != 129) { return 0 - 1; }
    return (t + s) % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 500) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 83;
}`,
		"strarr-local-callee-holder-escapes-arm64", 8, "yes")

	// SELF-`.with` REBIND (#6407): the in-place element store releases the
	// superseded box and retains the stored value, which makes the rebind
	// admissible to the credit. `v` is another ELEMENT, so without the retain
	// the exit walk would free one box through two slots → 99. 8 + 23 + 23 = 54.
	run(t, `function mks(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function build(pre: string): i32 { var a: string[] = mks(pre); a = a.with(3, a[5]); return a.len() + a[3].len() + a[5].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 54) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-with-rebind-arm64", 0, "yes")
}

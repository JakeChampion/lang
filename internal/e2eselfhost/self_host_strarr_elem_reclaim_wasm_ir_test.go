package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStrArrElemReclaimWasmIR is the wasm port of the #4355 string[]
// ELEMENT reclaim (x86 sibling: TestSelfHostStrArrElemReclaimIRX86_64). On wasm
// __fern_str_arr_free maps to $__fern_arr_dec_ptr — at rc==1 it $__fern_arr_dec's
// every element (which IS the wasm string free: a wasm heap string is a single
// inline rc-headered block) then frees the buffer. The reclaim is proven by
// CORRECTNESS + the over-release detector ($__fern_rc_underflow → exit 99) over
// a bounded churn. Heap-exhaustion churn is left to the x86 path.
func TestSelfHostStrArrElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string[] element reclaim wasm IR e2e")
	}
	dir := t.TempDir()
	l := newWasmStdlibLoader(t)

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// RECLAIM: fresh-element string[] (literal + concats, sanctioned
		// self-append), 20000 build/drop cycles. Every element box is freed
		// exactly once per exit sweep — a double free ticks the underflow
		// detector → 99. lens 3+3+4 = 10 → bad stays 0 → exit 0.
		{"strarr-elem-reclaim-churn-wasm", `function build(pre: string): i32 { let xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(20000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		// BOUNDED HIGH-WATER: after a 3000-iteration warmup, a second churn(3000)
		// re-serves every allocation from the freelist — the bump high-water
		// stays flat (< 256 B slack). Element leaks would grow it per iteration
		// → 98; a double-free ticks the underflow detector → 99.
		{"strarr-elem-reclaim-flat-wasm", `function build(pre: string): i32 { let xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		// LOOP-BODY REINIT, BOUNDED HIGH-WATER (#4353 item 4): a string[]
		// re-DECLARED each iteration is freed at the loop REBIND
		// (emit_strarr_reclaim_store), not at a helper exit. After a 3000-iter
		// warmup the second churn re-serves from the freelist → flat bump.
		// Pre-fix the reinit store leaked all 3 element boxes per iteration → 98.
		{"strarr-elem-reinit-loop-wasm", `function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { let xs: string[] = ["lit", pre + "x", pre + "yy"]; acc = (acc + xs[0].len() + xs[2].len()) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		// ELEMENT ALIAS BINDING excludes: `let t = xs[0]` — xs keeps the shallow
		// buffer-only dec; t stays valid, nothing double-frees. 3+2 = 5.
		{"strarr-elem-alias-excluded-wasm", `function pick(pre: string): i32 { let xs: string[] = [pre + "x", "qq"]; let t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		// PRODUCER-CALL ELEMENT: the stored elements are calls to a proven
		// fresh-string producer rather than inline concats. The credit's element
		// proof is strarr_value_is_fresh, so the registry arm admits them; the
		// registry-blind sibling it replaced refused any call. 43 + 3 + 43 = 89.
		{"strarr-elem-producer-store-wasm", `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let xs: string[] = [w(pre), "lit"]; xs = xs.append(w(pre)); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(5000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		// LOCAL BOUND FROM A PRODUCER: `let xs = mk(pre)` where `mk` is a
		// "STRARR:" registry function — the frame owns every element the callee
		// handed it, so the exit sweep may element-walk. `mk`'s own `out` escapes
		// by return and keeps the shallow dec, so each element is freed exactly
		// once — a second free would tick the underflow detector. 3 + 43 = 46.
		{"strarr-local-from-producer-wasm", `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let xs: string[] = mk(pre); return xs.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(5000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		// STORED BY THE CALLEE is ADMITTED, and this case used to pin the
		// opposite. Its premise was that `keep`'s parameter is not borrowable,
		// which is still true and is no longer the whole question:
		// param_counted_of proves every appearance of that parameter is a
		// COUNTED store, so the construction incs the buffer and the caller's
		// claim survives the call. The "CNT:" tier carries that verdict to the
		// escape walker.
		//
		// Granting the DEEP walk on a shallow-release justification is the part
		// that needs stating. Two rules close it from both ends:
		// __fern_str_arr_free is rc-gated, so only the owner that finds rc 1
		// walks the elements; and no element can be out UNCOUNTED, because the
		// tier refuses ExprIndex for array params while the caller's own
		// element-hazard rules still exclude `let t = xs[0]` — the alias case
		// above. Both reads stay valid. 3 + 43 + 43 = 89.
		{"strarr-local-stored-by-callee-counted-wasm", `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): i32 { let xs: string[] = mk(pre); let b: Box = keep(xs); return b.rows.len() + b.rows[0].len() + xs[2].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		// The same store where the holder ESCAPES the frame that owns the array
		// — the shape the case above was written against, and the one that can
		// actually fail. `build` returns the Box, so the retain is still live
		// when `xs` sweeps: the walk runs, finds rc 2, and decs without touching
		// an element. Every element is read back AFTER 20 churn frames have
		// recycled the freelist; a wrong walk returns 100, a double free 99. One
		// junk element goes through ids so the churn's array is built on the heap
		// rather than placed as a constant.
		{"strarr-local-callee-holder-escapes-wasm", `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): Box { let xs: string[] = mk(pre); let b: Box = keep(xs); return b; }
function ids(s: string): string { return s; }
function churnjunk(i: i32): i32 { let a: string[] = ["zzzz", "yyyy", ids("xxxx")]; return a[0].len() + a[2].len(); }
function round(i: i32): i32 { let pre: string = "ab"; let b: Box = build(pre); let j: i32 = 0; let t: i32 = 0; while (j < 20) { t = t + churnjunk(j); j = j + 1; } let s: i32 = 0; let k: i32 = 0; while (k < b.rows.len()) { s = s + b.rows[k].len(); k = k + 1; } if (s != 129) { return 0 - 1; } return (t + s) % 101; }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0; while (i < 500) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; } if (bad > 0) { return 100; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`, 8},
		// SELF-`.with` REBIND (#6407): the in-place element store releases the
		// superseded box and retains the stored value, which makes the rebind
		// admissible to the credit. `v` is another ELEMENT, so without the
		// retain the exit walk would free one box through two slots → 99.
		// 8 + 23 + 23 = 54 each build.
		{"strarr-with-rebind-wasm", `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function build(pre: string): i32 { let a: string[] = mks(pre); a = a.with(3, a[5]); return a.len() + a[3].len() + a[5].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 54) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(5000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := l.emit(t, tc.src)
			if len(wat) == 0 {
				t.Fatalf("no WAT for %q", tc.src)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("string[] element reclaim wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}

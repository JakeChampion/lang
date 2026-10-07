package e2ecompiler

import (
	"strings"
	"testing"
)

// strSliceRecvBorrowCases pin a string SLICE as the receiver of a
// source-declared method: the slice borrows its source unless the method
// carries the receiver out.
//
// `base[4:base.len()].to_owned().len()` still releases `base`, because
// `to_owned` (`return s + ""`) only copies its receiver. `trim`
// (`return s[low:high]`) returns a view of its receiver, so a slice passed to
// it is carried out and `base` must stay alive. That is the distinction these
// cases pin.
//
// The cases use fixture-local methods rather than std/string's, so the suite
// pins the mechanism rather than one stdlib body: `own2` is `to_owned`'s shape
// and `view2` is `trim`'s.
//
// Measured on x86-64 over 400 rounds: 64000 bytes with the source stranded,
// 9600 with only the intermediate VIEW BOX left, flat once that box is
// released too — on all three legs.
const sliceRecvPrelude = `import "std/i32";
import "std/i64";
import "std/string";
` + strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function ww(pre: string): string { return pre + "-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment"; }
function (s: string) own2(): string { return s + ""; }
function (s: string) view2(): str { return slice_unchecked(s, 1, s.len()); }
`

// sliceRecvHeap wraps a `round` body in the churn/heap-delta harness. The gate
// sits between the two measured deltas rather than at flat: this change frees
// the SOURCE, and the view box it leaves behind is the next slice's business.
func sliceRecvHeap(round string) string {
	return sliceRecvPrelude + `function round(pre: string): i32 { let base: string = ww(pre); ` + round + ` }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= @LIMIT@) { return 98; }
    return 0;
}`
}

// The gate was per-leg while a RESIDUAL remained — a fixed 24-byte view box on
// the register backends, a payload-sized copy on wasm, since a slice is
// zero-copy on the asm-IR path (#4294) and a copy on wasm. The sibling change
// that releases that box at an ExprSlice receiver takes every leg to 0, so one
// tight gate now serves all three and catches a regression of EITHER half: lose
// the source's credit and the leak is ~48 B/round, lose the box release and it
// is 24 (register) or payload-sized (wasm).
const sliceRecvLimit = "4096"

// sliceRecvSrc fills in the leg's heap gate. Cases without a gate carry no
// placeholder and pass through unchanged.
func sliceRecvSrc(src string, limit string) string {
	return strings.ReplaceAll(src, "@LIMIT@", limit)
}

var strSliceRecvBorrowCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here. 64000 before, 9600 after; the gate at 32768 is
	// 2x under the leak and 3.4x over what remains.
	{"str-slice-recv-source-method-improved", sliceRecvHeap(`return slice_unchecked(base, 4, base.len()).own2().len();`), 0},
	// CONTROL: no slice, already a borrow through the bare-ident receiver arm.
	// Flat (0) on both sides — pins that widening the carve-out does not disturb
	// the path that never needed it.
	{"str-slice-recv-plain-control", sliceRecvHeap(`return base.own2().len();`), 0},
	// The same view-returning method with the result NOT escaping the frame:
	// both the view and the source are read afterwards and must survive.
	{"str-slice-recv-view-method-live", sliceRecvPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let v: str = slice_unchecked(base, 2, base.len()).view2();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (has_sub(v, "XXXX")) { return 0 - 1; }
    if (!has_prefix(v, "defgh")) { return 0 - 2; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + v.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 209) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// The ADMITTED method's result and the source both live: `own2` copies, so
	// releasing nothing and reclaiming `base` at scope end must leave both intact.
	{"str-slice-recv-owned-live", sliceRecvPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let c: string = slice_unchecked(base, 4, base.len()).own2();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(c, "XXXX")) { return 0 - 1; }
    if (!has_prefix(c, "efgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 208) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// A same-named method on another type: `Hold.own2` RETAINS (it hands back
	// a field holding `base`) while `string.own2` only copies, and the result
	// outlives the frame. `Hold.own2` must not be read as `string.own2`; the
	// result is re-read after churn.
	{"str-slice-recv-struct-name-collision", sliceRecvPrelude + `struct Hold { v: string }
function (h: Hold) own2(): string { return h.v; }
function mk(s: string): Hold { return Hold { v: s }; }
function leak2(pre: string): string {
    let base: string = w(pre);
    return mk(base).own2();
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 3000) {
        let c: string = leak2("abcdefgh");
        let p1: string = w("XXXXXXXX");
        let p2: string = w("YYYYYYYY");
        if (p1.len() + p2.len() < 0) { return 0; }
        if (has_sub(c, "XXXX")) { return 96; }
        if (c.len() != 106) { return 97; }
        if (!has_prefix(c, "abcdefgh-a-wide")) { return 95; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

const strSliceRecvFailFmt = "%s = %d, want %d (98 = the source lost its reclaim credit; 96 = a released source read back; 99 = over-release; 97/95 = value corrupted)"

// TestSelfHostStrSliceRecvBorrowIRX86_64 drives the cases through the
// self-hosted CLI for x86-64.
func TestSelfHostStrSliceRecvBorrowIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSliceRecvBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", sliceRecvSrc(tc.src, sliceRecvLimit))); code != tc.want {
				t.Errorf(strSliceRecvFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceRecvBorrowIRArm64 is the arm64 leg.
func TestSelfHostStrSliceRecvBorrowIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range strSliceRecvBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", sliceRecvSrc(tc.src, sliceRecvLimit))); code != tc.want {
				t.Errorf(strSliceRecvFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceRecvBorrowWasmIR is the wasm leg.
func TestSelfHostStrSliceRecvBorrowWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSliceRecvBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", sliceRecvSrc(tc.src, sliceRecvLimit))); code != tc.want {
				t.Errorf(strSliceRecvFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

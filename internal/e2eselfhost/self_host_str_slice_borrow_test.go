package e2eselfhost

import "testing"

// strSliceBorrowCases pin which positions read a string SLICE as a borrow of its
// source and which read it as an alias.
//
// A slice is a zero-copy view over the source's buffer, so the escape scan
// counted every one of them as an alias: a `base[a:b]` anywhere in the body
// struck `base`'s reclaim credit, and neither the loop-rebind release nor the
// scope-exit sweep ever freed it. `base[4:base.len()].len()` — no binding, no
// method chain, nothing carried anywhere — leaked 47 B/round on x86-64 and
// arm64, 71 on wasm.
//
// The lowerer already draws the line the scan was missing. lower_view_borrowed
// marks the positions where a view is consumed inside the expression that built
// it, and lower_str_slice_frame puts the box for one in reserved frame slots
// that no name can reach. expr_unsafe_for_view_pos is that same list asked as an
// escape question, and the source of a view in one of those positions is READ,
// not aliased out.
//
// The flat cases return 98 when the source is stranded: 400 rounds of a 170-byte
// producer is 68 KB leaked against a 32 KB ceiling, and the widest legitimate
// residual (wasm, where the view is a copy rather than a frame box, on the
// nested-slice case) is 19 KB. The trim case is value-exact under
// same-size-class allocation pressure, so a wrongly released buffer is recycled
// with different bytes rather than merely freed and left readable.
var strSliceBorrowCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here: a `.len()` read of an inline slice. The view box is
	// frame-allocated and costs nothing; what leaked was `base` itself, 47 B/round,
	// because the escape scan read the slice as an alias and struck the binding's
	// reclaim credit.
	{"str-slice-len-recv-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { var base: string = w(pre); return slice_unchecked(base, 4, 12).len(); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// A comparison operand: `==` / `!=` lower to str_eq, which reads bytes and moves
	// nothing.
	{"str-slice-compare-operand-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { var base: string = w(pre); if (slice_unchecked(base, 0, 8) == pre) { return 3; } return 5; }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// A concat operand: `+` copies both operands' bytes into a new box.
	{"str-slice-concat-operand-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { var base: string = w(pre); return (slice_unchecked(base, 0, 8) + "!").len(); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// A byte read off the view: `base[4:12][1]` indexes the view and copies out one
	// byte.
	{"str-slice-index-base-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { var base: string = w(pre); return (slice_unchecked(base, 4, 12)[1] as i32); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// A slice OF a slice, both in borrow position — the recursion has to carry the
	// verdict through the inner view as well as the outer.
	{"str-slice-nested-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { var base: string = w(pre); return slice_unchecked(slice_unchecked(base, 4, 20), 2, 6).len(); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// `.trim()` returns an owned string, so trimming a slice reads `base` rather
	// than aliasing it. The padding is the same length as `base`, so a wrongly
	// released buffer is recycled with different bytes and the value check sees it.
	{"str-slice-trim-receiver-owned", `import "std/string";
function w(pre: string): string { return "  " + pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789  "; }
function view(pre: string): str { var base: string = w(pre); return slice_unchecked(base, 0, 14).trim(); }
function round(pre: string): i32 {
    var v: str = view(pre);
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (v != "abcdefgh-a-w") { return 0 - 1; }
    return v.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 12) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

const strSliceBorrowFailFmt = "%s = %d, want %d (98 = the sliced source was stranded; 99 = over-release; 97 = value corrupted)"

func TestSelfHostStrSliceBorrowIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSliceBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(strSliceBorrowFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceBorrowIRArm64 is the arm64 leg. The scan is shared
// irlower; the frame-view form it licenses is a per-backend transcription.
func TestSelfHostStrSliceBorrowIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range strSliceBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(strSliceBorrowFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceBorrowWasmIR is the wasm leg, where a slice COPIES rather
// than viewing, so the residual per round is the view's own bytes and the source
// reclaim is all these cases move.
func TestSelfHostStrSliceBorrowWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSliceBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf(strSliceBorrowFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

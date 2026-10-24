package e2ecompiler

import (
	"strings"
	"testing"
)

const sliceRecvPrelude = `import "std/i32";
import "std/i64";
import "std/string";
` + strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function ww(pre: string): string { return pre + "-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment"; }
function (s: string) own2(): string { return s + ""; }
function (s: string) view2(): str { return slice_unchecked(s, 1, s.len()); }
`

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

const sliceRecvLimit = "4096"

func sliceRecvSrc(src string, limit string) string {
	return strings.ReplaceAll(src, "@LIMIT@", limit)
}

var strSliceRecvBorrowCases = []struct {
	name string
	src  string
	want int
}{
	{"str-slice-recv-source-method-improved", sliceRecvHeap(`return slice_unchecked(base, 4, base.len()).own2().len();`), 0},
	{"str-slice-recv-plain-control", sliceRecvHeap(`return base.own2().len();`), 0},
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

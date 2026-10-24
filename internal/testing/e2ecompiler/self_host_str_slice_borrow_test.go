package e2ecompiler

import "testing"

var strSliceBorrowCases = []struct {
	name string
	src  string
	want int
}{
	{"str-slice-len-recv-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { let base: string = w(pre); return slice_unchecked(base, 4, 12).len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-slice-compare-operand-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { let base: string = w(pre); if (slice_unchecked(base, 0, 8) == pre) { return 3; } return 5; }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-slice-concat-operand-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { let base: string = w(pre); return (slice_unchecked(base, 0, 8) + "!").len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-slice-index-base-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { let base: string = w(pre); return (slice_unchecked(base, 4, 12)[1] as i32); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-slice-nested-borrow-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789"; }
function round(pre: string): i32 { let base: string = w(pre); return slice_unchecked(slice_unchecked(base, 4, 20), 2, 6).len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-slice-trim-receiver-owned", `import "std/string";
function w(pre: string): string { return "  " + pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-view-box-so-the-source-string-dominates-the-measurement-when-it-is-stranded-by-a-slice-read-0123456789  "; }
function view(pre: string): str { let base: string = w(pre); return slice_unchecked(base, 0, 14).trim(); }
function round(pre: string): i32 {
    let v: str = view(pre);
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (v != "abcdefgh-a-w") { return 0 - 1; }
    return v.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { if (round(pre) != 12) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
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

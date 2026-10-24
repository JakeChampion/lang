package e2ecompiler

import "testing"

var strViewFrameCases = []struct {
	name string
	src  string
	want int
}{
	{"strview-loop-flat", `function main(): i32 {
    let s: string = "hello world";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let t: str = slice_unchecked(s, 0, 1); acc = (acc + t.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let t2: str = slice_unchecked(s, 0, 1); acc = (acc + t2.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"strview-value-exact", `function main(): i32 {
    let s: string = "abcdefgh";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let t: str = slice_unchecked(s, i, i + 2);
        if (t.len() != 2) { return 96; }
        if (t[0] != s[i]) { return 95; }
        if (t[1] != s[i + 1]) { return 94; }
        acc = acc + (t[0] as i32);
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 97 + 98 + 99 + 100 + 101 + 102) { return 97; }
    return 0;
}`, 0},
	{"strview-concat-compare", `function main(): i32 {
    let s: string = "hello world";
    let hits: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let t: str = slice_unchecked(s, 1, 4);
        if (t == "ell") { hits = hits + 1; }
        let wrapped: string = "<" + t + ">";
        if (wrapped.len() != 5) { return 96; }
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (hits != 200) { return 97; }
    if (b1 < 0) { return 95; }
    return 0;
}`, 0},
	{"strview-nested-slice", `function main(): i32 {
    let s: string = "abcdefgh";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let t: str = slice_unchecked(s, 2, 6);
        let u: str = slice_unchecked(t, 1, 3);
        if (u.len() != 2) { return 96; }
        if (u[0] != 100) { return 95; }
        acc = (acc + u.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != (400) % 251) { return 97; }
    return 0;
}`, 0},
	{"strview-escape-return-safe", `function head(s: string): str { let t: str = slice_unchecked(s, 0, 4); return t; }
function churn(n: i32): i32 {
    let a: i32 = n * 3;
    let b: i32 = a + 7;
    let c: i32 = b * 2;
    let d: i32 = c - a;
    let e: i32 = d + b;
    let f: i32 = e % 97;
    return a + b + c + d + e + f;
}
function main(): i32 {
    let s: string = "hello world";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let v: str = head(s);
        acc = (acc + churn(i)) % 251;
        if (v.len() != 4) { return 96; }
        if (v[0] != 104) { return 95; }
        if (v[3] != 108) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"strview-escape-alias-safe", `function main(): i32 {
    let s: string = "abcdefgh";
    let prev: str = slice_unchecked(s, 0, 2);
    let acc: i32 = 0;
    let i: i32 = 1;
    while (i < 6) {
        let cur: str = slice_unchecked(s, i, i + 2);
        if (prev[0] != s[i - 1]) { return 95; }
        acc = acc + (prev[0] as i32);
        prev = cur;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (prev[0] != 102) { return 96; }
    if (acc != 97 + 98 + 99 + 100 + 101) { return 97; }
    return 0;
}`, 0},
	{"strview-escape-arg-safe", `function keep(v: str, xs: str[]): str[] { return xs.append(v); }
function build(s: string, k: i32): str[] {
    let xs: str[] = [];
    let i: i32 = 0;
    while (i < k) { let t: str = slice_unchecked(s, i, i + 2); xs = keep(t, xs); i = i + 1; }
    return xs;
}
function churn(n: i32): i32 { let a: i32 = n * 3; let b: i32 = a + 7; let c: i32 = b * 2; return a + b + c; }
function main(): i32 {
    let s: string = "abcdefgh";
    let xs: str[] = build(s, 5);
    let acc: i32 = churn(5) % 251;
    let j: i32 = 0;
    while (j < xs.len()) {
        if (xs[j].len() != 2) { return 96; }
        if (xs[j][0] != s[j]) { return 95; }
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"strview-escape-store-safe", `function collect(s: string, k: i32): str[] {
    let xs: str[] = [];
    let i: i32 = 0;
    while (i < k) { let t: str = slice_unchecked(s, i, i + 2); xs = xs.append(t); i = i + 1; }
    return xs;
}
function churn(n: i32): i32 {
    let a: i32 = n * 3;
    let b: i32 = a + 7;
    let c: i32 = b * 2;
    let d: i32 = c - a;
    let e: i32 = d + b;
    return a + b + c + d + e;
}
function main(): i32 {
    let s: string = "abcdefgh";
    let xs: str[] = collect(s, 5);
    let acc: i32 = churn(3) % 251;
    let j: i32 = 0;
    while (j < xs.len()) {
        if (xs[j].len() != 2) { return 96; }
        if (xs[j][0] != s[j]) { return 95; }
        acc = acc + (xs[j][0] as i32);
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"strview-temp-loop-flat", `function main(): i32 {
    let s: string = "hello world";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        acc = (acc + slice_unchecked(s, 0, 4).len() + (slice_unchecked(s, 2, 6)[1] as i32) + ("<" + slice_unchecked(s, 1, 3)).len()) % 251;
        if (slice_unchecked(s, 0, 5) == "hello") { acc = (acc + 1) % 251; }
        acc = (acc + slice_unchecked(slice_unchecked(s, 1, 7), 2, 4).len()) % 251;
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) {
        acc = (acc + slice_unchecked(s, 0, 4).len() + (slice_unchecked(s, 2, 6)[1] as i32) + ("<" + slice_unchecked(s, 1, 3)).len()) % 251;
        if (slice_unchecked(s, 0, 5) == "hello") { acc = (acc + 1) % 251; }
        acc = (acc + slice_unchecked(slice_unchecked(s, 1, 7), 2, 4).len()) % 251;
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    // The concat still allocates its RESULT per iteration; only the five VIEW boxes
    // are gone, so the bound is the concat's share rather than zero.
    if (b2 - b1 >= 300000) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"strview-temp-value-exact", `function main(): i32 {
    let s: string = "abcdefgh";
    let u: string = "wxyz";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        if (("[" + slice_unchecked(s, i, i + 2) + "]").len() != 4) { return 96; }
        if (slice_unchecked(s, i, i + 2).len() != 2) { return 95; }
        if (slice_unchecked(s, i, i + 2)[0] != s[i]) { return 94; }
        acc = acc + (slice_unchecked(s, i, i + 2)[0] as i32);
        i = i + 1;
    }
    if (acc != 97 + 98 + 99 + 100 + 101 + 102) { return 93; }
    // Two views of DIFFERENT sources live at once as the operands of one concat:
    // one shared slot triple would make this "abxy" wrong.
    if ((slice_unchecked(s, 0, 2) + slice_unchecked(u, 1, 3)) != "abxy") { return 92; }
    if ((slice_unchecked(s, 0, 2) + slice_unchecked(s, 4, 6)) != "abef") { return 91; }
    if (slice_unchecked(slice_unchecked(s, 2, 6), 1, 3) != "de") { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"strview-temp-escape-arg-safe", `function collect(s: string, k: i32): str[] {
    let xs: str[] = [];
    let i: i32 = 0;
    while (i < k) { xs = xs.append(slice_unchecked(s, i, i + 2)); i = i + 1; }
    return xs;
}
function churn(n: i32): i32 {
    let a: i32 = n * 3;
    let b: i32 = a + 7;
    let c: i32 = b * 2;
    let d: i32 = c - a;
    let e: i32 = d + b;
    return a + b + c + d + e;
}
function main(): i32 {
    let s: string = "abcdefgh";
    let xs: str[] = collect(s, 5);
    let acc: i32 = churn(4) % 251;
    let j: i32 = 0;
    while (j < xs.len()) {
        if (xs[j].len() != 2) { return 96; }
        if (xs[j][0] != s[j]) { return 95; }
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"strview-temp-escape-return-safe", `function head(s: string): str { return slice_unchecked(s, 0, 4); }
function churn(n: i32): i32 {
    let a: i32 = n * 3;
    let b: i32 = a + 7;
    let c: i32 = b * 2;
    let d: i32 = c - a;
    return a + b + c + d;
}
function main(): i32 {
    let s: string = "hello world";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let v: str = head(s);
        acc = (acc + churn(i)) % 251;
        if (v.len() != 4) { return 96; }
        if (v[0] != 104) { return 95; }
        if (v[3] != 108) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

const strViewFrameFailFmt = "%s = %d, want %d (98 = view box still per-evaluation; 99 = over-release/underflow; 94-97 = value corrupted)"

func TestSelfHostStrViewFrameIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strViewFrameCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(strViewFrameFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrViewFrameIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range strViewFrameCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(strViewFrameFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

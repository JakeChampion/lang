package e2ecompiler

import "testing"

const mapBoxColumnExitHint = "a page count = the box column still leaks; 90/91 = wrong value read back; 99 = over-release"

var mapBoxColumnCases = []struct {
	name string
	src  string
}{
	{"arr-column-flat", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32[]] = Map { 1: [n, n + 1], 2: [n + 2, n + 3, n + 4] };
    let r: i32 = 0;
    if (m.contains(1)) { r = r + 1; }
    if (m.contains(2)) { r = r + 1; }
    return r;
}

function build_i32(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: n, 2: n + 1 };
    let r: i32 = 0;
    if (m.contains(1)) { r = r + 1; }
    if (m.contains(2)) { r = r + 1; }
    return r;
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build(i) + build_i32(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j) + build_i32(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 8800) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	{"struct-column-flat", `import "core/map";
struct Q { a: i32, b: i32 }

function build(n: i32): i32 {
    let m: Map[i32, Q] = Map { 1: Q { a: n, b: n + 1 }, 2: Q { a: n + 2, b: n + 3 } };
    let r: i32 = 0;
    if (m.contains(1)) { r = r + 1; }
    if (m.contains(2)) { r = r + 1; }
    return r;
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 4400) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	{"read-then-recycle", `import "core/map";
function inner(m: Map[i32, i32[]]): i32 {
    let v: i32[] = m.get_or(2, []);
    return v.len();
}

function build(n: i32): i32 {
    let m: Map[i32, i32[]] = Map { 2: [n + 2, n + 3, n + 4] };
    let a: i32 = inner(m);
    let junk: i32[] = [999, 998, 997];
    let v2: i32[] = m.get_or(2, []);
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < v2.len()) { s = s + v2[i]; i = i + 1; }
    if (a != 3) { return 0 - 1; }
    if (junk.len() != 3) { return 0 - 2; }
    return s;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 100) {
        if (build(i) != 3 * i + 9) { return 90; }
        i = i + 1;
    }
    return 0;
}
`},
	{"read-back-no-over-release", `import "core/map";
struct Q { a: i32, b: i32 }

function build_arr(n: i32): i32 {
    let m: Map[i32, i32[]] = Map { 1: [n, n + 1], 2: [n + 2, n + 3, n + 4] };
    let v: i32[] = m.get_or(2, []);
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < v.len()) { s = s + v[i]; i = i + 1; }
    return s;
}

function build_struct(n: i32): i32 {
    let m: Map[i32, Q] = Map { 1: Q { a: n, b: n + 1 }, 2: Q { a: n + 2, b: n + 3 } };
    let q: Q = m.get_or(2, Q { a: 0, b: 0 });
    return q.a + q.b;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 500) {
        if (build_arr(i) != 3 * i + 9) { return 90; }
        if (build_struct(i) != 2 * i + 5) { return 91; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`},
	{"strarr-column-uncredited-control", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, string[]] = Map { 1: ["a" + "b", "c" + "d"] };
    let v: string[] = m.get_or(1, []);
    if (v.len() != 2) { return 0 - 1; }
    if (v[0].len() != 2) { return 0 - 2; }
    return 1;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        if (build(i) != 1) { return 90; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`},
}

func TestSelfHostMapBoxColumnReclaimIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapBoxColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want 0 (%s)", tc.name, code, mapBoxColumnExitHint)
			}
		})
	}
}

func TestSelfHostMapBoxColumnReclaimIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapBoxColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want 0 (%s)", tc.name, code, mapBoxColumnExitHint)
			}
		})
	}
}

func TestSelfHostMapBoxColumnReclaimIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapBoxColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want 0 (%s)", tc.name, code, mapBoxColumnExitHint)
			}
		})
	}
}

package e2ecompiler

import "testing"

var mapScopedReclaimCases = []struct {
	name string
	src  string
}{
	{"block-scoped-if-flat", `import "core/map";
function build(n: i32): i32 {
    let r: i32 = 0;
    if (n >= 0) {
        let m: Map[i32, i32] = Map { 1: n, 2: n + 1 };
        if (m.contains(1)) { r = r + 1; }
        if (m.contains(2)) { r = r + 1; }
    }
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
	{"loop-declared-flat", `import "core/map";
function build(n: i32): i32 {
    let r: i32 = 0;
    let k: i32 = 0;
    while (k < 3) {
        let m: Map[i32, i32] = Map { 1: n + k, 2: n + k + 1 };
        if (m.contains(1)) { r = r + 1; }
        if (m.contains(2)) { r = r + 1; }
        k = k + 1;
    }
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
    if (acc != 13200) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	{"block-scoped-string-columns-flat", `import "core/map";
function build(n: i32): i32 {
    let r: i32 = 0;
    if (n >= 0) {
        let m: Map[string, string] = Map { "k" + "1": "v" + "1", "k" + "2": "v" + "2" };
        if (m.contains("k1")) { r = r + 1; }
        if (m.contains("k2")) { r = r + 1; }
    }
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
	{"function-scope-control-flat", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: n, 2: n + 1 };
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
	{"sibling-alias-no-over-release", `import "core/map";
function round(base: Map[i32, i32], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let m: Map[i32, i32] = Map { 1: i, 2: i + 1 }; if (m.contains(1)) { t = t + 1; } }
    if (i % 2 == 1) { let m: Map[i32, i32] = base; if (m.contains(1)) { t = t + 2; } }
    return t;
}

function main(): i32 {
    let b: Map[i32, i32] = Map { 1: 7, 2: 8 };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (!b.contains(2)) { return 91; }
    if (t != 300) { return 92; }
    return 0;
}
`},
}

const mapScopedReclaimWant = "want 0 (a page count = the scoped map still leaks; 90 = wrong value; 91/92 = the aliased map was freed under its owner; 99 = over-release)"

func TestSelfHostMapScopedReclaimIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapScopedReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, %s", tc.name, code, mapScopedReclaimWant)
			}
		})
	}
}

func TestSelfHostMapScopedReclaimIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapScopedReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, %s", tc.name, code, mapScopedReclaimWant)
			}
		})
	}
}

func TestSelfHostMapScopedReclaimIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapScopedReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("scoped-map wasm %q = %d, %s", tc.name, code, mapScopedReclaimWant)
			}
		})
	}
}

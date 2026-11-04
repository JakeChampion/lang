package e2ecompiler

import "testing"

// Each element type copies an array once with `for x in xs` and once with the
// same loop over an index. The exit code names the first type whose two forms
// allocate a different number of times (#14066): 1 for bytes, 2 for i32, 3
// for strings, 4 for records. 5 to 8 are a copy that lost an element.
const elementForCopyAllocsSrc = `struct P { name: string, n: i32 }

function bytes_for(xs: u8[]): u8[] {
    let out: u8[] = [];
    for x in xs { out = out.append(x); }
    return out;
}
function bytes_idx(xs: u8[]): u8[] {
    let out: u8[] = [];
    for i in 0..xs.len() { out = out.append(xs[i]); }
    return out;
}
function ints_for(xs: i32[]): i32[] {
    let out: i32[] = [];
    for x in xs { out = out.append(x); }
    return out;
}
function ints_idx(xs: i32[]): i32[] {
    let out: i32[] = [];
    for i in 0..xs.len() { out = out.append(xs[i]); }
    return out;
}
function strings_for(xs: string[]): string[] {
    let out: string[] = [];
    for x in xs { out = out.append(x); }
    return out;
}
function strings_idx(xs: string[]): string[] {
    let out: string[] = [];
    for i in 0..xs.len() { out = out.append(xs[i]); }
    return out;
}
function records_for(xs: P[]): P[] {
    let out: P[] = [];
    for x in xs { out = out.append(x); }
    return out;
}
function records_idx(xs: P[]): P[] {
    let out: P[] = [];
    for i in 0..xs.len() { out = out.append(xs[i]); }
    return out;
}

function round(n: i32): i32 {
    let bs: u8[] = [];
    let is: i32[] = [];
    let ss: string[] = [];
    let ps: P[] = [];
    for i in 0..n {
        bs = bs.append((i % 251) as u8);
        is = is.append(i * 3);
        ss = ss.append("element-" + i.to_string());
        ps = ps.append(P { name: "record-" + i.to_string(), n: i });
    }
    let a0 = __heap_alloc_count();
    let b1 = bytes_for(bs);
    let a1 = __heap_alloc_count();
    let b2 = bytes_idx(bs);
    let a2 = __heap_alloc_count();
    if (a1 - a0 != a2 - a1) { return 1; }
    let i1 = ints_for(is);
    let a3 = __heap_alloc_count();
    let i2 = ints_idx(is);
    let a4 = __heap_alloc_count();
    if (a3 - a2 != a4 - a3) { return 2; }
    let s1 = strings_for(ss);
    let a5 = __heap_alloc_count();
    let s2 = strings_idx(ss);
    let a6 = __heap_alloc_count();
    if (a5 - a4 != a6 - a5) { return 3; }
    let p1 = records_for(ps);
    let a7 = __heap_alloc_count();
    let p2 = records_idx(ps);
    let a8 = __heap_alloc_count();
    if (a7 - a6 != a8 - a7) { return 4; }
    let last = n - 1;
    if (b1.len() != n || b2.len() != n || b1[last] != bs[last] || b2[last] != bs[last]) { return 5; }
    if (i1.len() != n || i2.len() != n || i1[last] != last * 3 || i2[last] != last * 3) { return 6; }
    if (s1.len() != n || s2.len() != n || s1[last] != ss[last] || s2[last] != ss[last]) { return 7; }
    if (p1.len() != n || p2.len() != n || p1[last].n != last || p2[last].name != ps[last].name) { return 8; }
    return 0;
}

function main(): i32 {
    for n in [1, 9, 100, 1000] {
        let code = round(n);
        if (code != 0) { return code; }
    }
    return 42;
}`

func TestSelfHostElementForCopyAllocs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, elementForCopyAllocsSrc, target); code != 42 {
				t.Errorf("exited %d, want 42 (1 to 4 are an element loop that allocates more than its indexed form)\n%s", code, stderr)
			}
		})
	}
}

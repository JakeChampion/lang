package e2ecompiler

import "testing"

var paramAppendReclaimCases = []struct {
	name string
	main string
	want int
}{
	{"param-append-recycled-by-return", `
struct D { code: string, n: i32 }
function walk(acc: string[], n: i32): D[] {
    let out: D[] = [];
    let i: i32 = 0;
    while (i < n) {
        acc = acc.append("xx");
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    let seed: string[] = [];
    return walk(seed, n);
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 30) {
        let d: D[] = entry(4);
        t = (t + d.len()) % 100;
        let junk: string[] = ["aaaa", "bbbb"];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    let ds: D[] = entry(4);
    let hit: i32 = 0;
    let j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	{"param-append-i32-recycled", `
struct D { code: string, n: i32 }
function walk(acc: i32[], n: i32): D[] {
    let out: D[] = [];
    let i: i32 = 0;
    while (i < n) {
        acc = acc.append(i);
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    let seed: i32[] = [];
    return walk(seed, n);
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 30) {
        let d: D[] = entry(4);
        t = (t + d.len()) % 100;
        let junk: i32[] = [1, 2];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    let ds: D[] = entry(4);
    let hit: i32 = 0;
    let j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	{"caller-reads-seed-after-callee-append", `
function walk(acc: string[], n: i32): i32 {
    let i: i32 = 0;
    while (i < n) { acc = acc.append("xx"); i = i + 1; }
    return acc.len();
}
function entry(): i32 {
    let seed: string[] = [];
    seed = seed.append("keep");
    let r: i32 = walk(seed, 6);
    let hit: i32 = 0;
    if (seed[0] == "keep") { hit = 1; }
    return r + hit;
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 30) {
        t = (t + entry()) % 100;
        let junk: string[] = ["aaaa", "bbbb"];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    return entry();
}
`, 8},
}

func TestSelfHostParamAppendReclaimIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range paramAppendReclaimCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main, target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

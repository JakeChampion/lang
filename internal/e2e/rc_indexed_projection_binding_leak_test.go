package e2e

import "testing"

// A local bound to a projection THROUGH an array element — `ps[i].steps`,
// `grid[i][j]`, `fs[f].graph.blocks[0].insts[i]` — takes the binding's alias
// inc like any field or element read, but the free analysis only credited a
// chain of struct / tuple fields back to a slot, so an indexed step left the
// binding borrow-tainted. It then had no per-iteration release and a flat exit
// dec, and every element it ever held leaked with everything reachable from it.
// Each program checks its own answer and carries `__rc_underflow_count()` in
// its exit, so crediting a binding that does not own its count would show as
// an over-release rather than a clean census.
const indexedProjectionPrelude = `struct St { block: i32, drops: i32[] }
struct Pl { ok: boolean, steps: St[] }
function mk(n: i32): Pl[] {
    let out: Pl[] = [];
    let k: i32 = 0;
    while (k < n) {
        let steps: St[] = [];
        let j: i32 = 0;
        while (j < 20) { steps = steps.append(St { block: j, drops: [j] }); j = j + 1; }
        out = out.append(Pl { ok: true, steps: steps });
        k = k + 1;
    }
    return out;
}
`

var indexedProjectionBindingCases = []struct{ name, src string }{
	{"bound-field", indexedProjectionPrelude + `function walk(plans: Pl[]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < plans.len()) { let st: St[] = plans[i].steps; t = t + st.len(); i = i + 1; }
    return t;
}
function main(): i32 { let ps: Pl[] = mk(50); return walk(ps) - 1000 + __rc_underflow_count(); }
`},
	{"for-over-field", indexedProjectionPrelude + `function walk(plans: Pl[]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < plans.len()) {
        for s in plans[i].steps { t = t + s.block + s.drops.len(); }
        i = i + 1;
    }
    return t;
}
function main(): i32 { let ps: Pl[] = mk(50); return walk(ps) - 10500 + __rc_underflow_count(); }
`},
	{"element-of-element", `struct St { block: i32, drops: i32[] }
function mk(n: i32): St[][] {
    let out: St[][] = [];
    let k: i32 = 0;
    while (k < n) {
        let row: St[] = [];
        let j: i32 = 0;
        while (j < 20) { row = row.append(St { block: j, drops: [j] }); j = j + 1; }
        out = out.append(row);
        k = k + 1;
    }
    return out;
}
function walk(grid: St[][]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < grid.len()) {
        let j: i32 = 0;
        while (j < 20) { let s: St = grid[i][j]; t = t + s.drops.len(); j = j + 1; }
        i = i + 1;
    }
    return t;
}
function main(): i32 { let g: St[][] = mk(50); return walk(g) - 1000 + __rc_underflow_count(); }
`},
	{"deep-chain-bound", `import "std/i32";
struct I { kind: i32, args: i32[], str: string }
struct B { insts: I[] }
struct G { blocks: B[] }
struct F { graph: G }
function mk(n: i32): F[] {
    let fs: F[] = [];
    let k: i32 = 0;
    while (k < n) {
        let insts: I[] = [];
        let j: i32 = 0;
        while (j < 20) { insts = insts.append(I { kind: j, args: [j, k], str: "s" + j.to_string() }); j = j + 1; }
        fs = fs.append(F { graph: G { blocks: [B { insts: insts }] } });
        k = k + 1;
    }
    return fs;
}
function walk(fs: F[], at: i32[]): i32 {
    let t: i32 = 0;
    let f: i32 = 0;
    while (f < fs.len()) {
        let i: i32 = 0;
        while (i < 20) {
            let ins: I = fs[f].graph.blocks[at[0]].insts[i];
            t = t + ins.kind + ins.args.len() + ins.str.len();
            i = i + 1;
        }
        f = f + 1;
    }
    return t;
}
function main(): i32 { let fs: F[] = mk(10); return walk(fs, [0]) - 2800 + __rc_underflow_count(); }
`},
	// The binding owns its count: replacing the element it was read from must
	// not take the array it holds down with it.
	{"outlives-replaced-element", indexedProjectionPrelude + `function main(): i32 {
    let ps: Pl[] = mk(3);
    let st: St[] = ps[0].steps;
    ps = ps.with(0, Pl { ok: false, steps: [] });
    let fresh: St[] = [St { block: 7, drops: [7, 7, 7] }];
    return st.len() + st[19].drops[0] + ps[0].steps.len() + fresh.len() - 40 + __rc_underflow_count();
}
`},
}

func TestLeakCheckIndexedProjectionBindingX86_64(t *testing.T) {
	for _, tc := range indexedProjectionBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckX86_64(t, tc.src)
			allocs, frees, live := leakSummaryIn(t, stderr)
			if code != 0 || allocs != frees || live != 0 {
				t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: the local bound through an indexed element is not released", code, allocs, frees, live)
			}
		})
	}
}

func TestLeakCheckIndexedProjectionBindingArm64(t *testing.T) {
	for _, tc := range indexedProjectionBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckArm64(t, tc.src)
			allocs, frees, live := leakSummaryIn(t, stderr)
			if code != 0 || allocs != frees || live != 0 {
				t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: the local bound through an indexed element is not released", code, allocs, frees, live)
			}
		})
	}
}

func TestLeakCheckIndexedProjectionBindingWasm(t *testing.T) {
	for _, tc := range indexedProjectionBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runLeakCheckWasm(t, tc.src, false)
			allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
			if code != 0 || allocs != frees || live != 0 {
				t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: the local bound through an indexed element is not released", code, allocs, frees, live)
			}
		})
	}
}

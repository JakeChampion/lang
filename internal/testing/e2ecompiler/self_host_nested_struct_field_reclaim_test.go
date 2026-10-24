package e2ecompiler

import (
	"strings"
	"testing"
)

const nestedStructFieldSrc = `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }

function round(): i32 {
    let o: Outer = Outer { f: Inner { v: 0 }, n: 0 };
    let i: i32 = 0;
    while (i < 4) { o = Outer { f: Inner { v: i }, n: i }; i = i + 1; }
    return o.n;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

func TestSelfHostNestedStructFieldReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, nestedStructFieldSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "nested_struct_field", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 6 {
		t.Fatalf("exited %d, want 6", exit)
	}
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatalf("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatalf("allocated nothing — the probe is not exercising the path")
	}
	if allocs != frees {
		t.Errorf("allocs=%d frees=%d — each rebind must release the nested-struct field "+
			"box it orphans; frees > allocs means the field was released twice", allocs, frees)
	}
	if live != 0 {
		t.Errorf("live_bytes=%d, want 0 — one Inner box per rebind, so this scales with "+
			"the loop count", live)
	}
}

func TestSelfHostNestedStructFieldHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "fresh_field_aliased_into_container",
			src: `struct Node { kind: i32, items: i32[] }
struct PS { node: Node, pos: i32 }
function parse_one(pos: i32): PS { return PS { node: Node { kind: pos, items: [pos, pos + 1] }, pos: pos + 1 }; }
function round(n: i32): i32 {
    let items: Node[] = [];
    let pos: i32 = 0;
    while (pos < n) { let p: PS = parse_one(pos); items = items.append(p.node); pos = p.pos; }
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < items.len()) { t = t + items[k].kind + items[k].items[1]; k = k + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(3); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			name: "field_rehomed_into_struct_literal",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
struct Holder { g: Inner, m: i32 }
function round(i: i32): i32 {
    let keep: Holder[] = [];
    let o: Outer = Outer { f: Inner { v: i }, n: i };
    let j: i32 = 0;
    while (j < 3) { o = Outer { f: Inner { v: i + j }, n: j }; keep = keep.append(Holder { g: o.f, m: j }); j = j + 1; }
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < keep.len()) { t = t + keep[k].g.v + keep[k].m; k = k + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			name: "functional_update_base_copies_field",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
function round(i: i32): i32 {
    let keep: Outer[] = [];
    let o: Outer = Outer { f: Inner { v: i }, n: i };
    let j: i32 = 0;
    while (j < 3) { keep = keep.append(Outer { ...o, n: j }); o = Outer { f: Inner { v: i + j }, n: j }; j = j + 1; }
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < keep.len()) { t = t + keep[k].f.v + keep[k].n; k = k + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 21,
		},
		{
			name: "borrow_chain_still_granted",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
function round(i: i32): i32 {
    let o: Outer = Outer { f: Inner { v: i }, n: i };
    let j: i32 = 0;
    while (j < 4) { o = Outer { f: Inner { v: i + j }, n: j }; j = j + 1; }
    return o.f.v + o.n;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 21,
		},
		{
			name: "field_passed_to_call",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
function sink(x: Inner): i32 { return x.v; }
function round(i: i32): i32 {
    let o: Outer = Outer { f: Inner { v: i }, n: i };
    return sink(o.f) + o.n;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 6,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "nested_struct_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash means the nested-field "+
					"release was granted to a shape that still holds a live reference to the "+
					"field box (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}

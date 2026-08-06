package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Nested-struct / nested-enum field reclaim (#6127 nested_struct) ---------
//
// A struct local whose field is itself a struct leaked that field's box on every
// REBIND. The asymmetry is the whole diagnosis: __struct_drop_<T> already has
// k_struct / k_enum arms, so a SINGLE bind reclaims cleanly at scope exit
// (measured 200/200/0), while __field_reclaim_<T>'s field loop had no such arm —
// so only rebinds leaked (900/600/12000 over 100 rounds against 0 on native).
// Routing was never the issue either: struct_has_reclaim_array_field already
// admits a direct nested-struct field.
//
// #6148 added the arms without an admission scan and it was a use-after-free. A
// struct-literal field value is sole-owned at rc=1, so `items.append(p.node)`
// takes an UNCOUNTED alias and the next rebind's release freed a box the
// container still pointed at. The self-host has no read-side alias-inc for these
// reads, so structfld_reclaim_ok_types_of IS the safety argument, not a
// convenience: a type is admitted only when every read of its nested-field names
// is a provable transient borrow.
//
// It could not reuse strfld_collect_unsafe. That walker recurses into a marked
// access's RECEIVER as unsafe, so `o.f.v` would mark `f` and exclude every type
// whose nested field is merely read through; here the receiver of a borrow chain
// is a borrow, judged under the inner field's own name. Two positions the string
// walker never visits also had to be added — a struct-literal field value
// (including a functional-update base, which copies every field pointer into the
// new box) and a tuple element. Both are pinned as hazards below.

const nestedStructFieldSrc = `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }

function round(): i32 {
    var o: Outer = Outer { f: Inner { v: 0 }, n: 0 };
    var i: i32 = 0;
    while (i < 4) { o = Outer { f: Inner { v: i }, n: i }; i = i + 1; }
    return o.n;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

// TestSelfHostNestedStructFieldReclaimX86_64 — a rebound struct local releases
// the nested-struct field box each rebind orphans. allocs == frees is
// load-bearing: frees short of allocs is the leak this closes; frees ABOVE
// allocs is #6148's use-after-free reappearing as a double free.
func TestSelfHostNestedStructFieldReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, nestedStructFieldSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "nested_struct_field", asm)
	stderr, exit := hevRun(t, runner, progBin)
	// 300 % 7 = 6, confirmed against both oracles (bin/fern -interp and native
	// -target x86-64), not read off the self-host run this test exists to check.
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

// TestSelfHostNestedStructFieldHazardsX86_64 — the shapes the scan must REFUSE,
// plus the borrow shapes it must keep admitting. A wrongly-granted release frees
// a nested box something else still points at, so the failure is a wrong answer
// or a crash, not a leak. Every `want` came from the interpreter and native
// x86-64 agreeing.
//
// The refused cases were each confirmed genuinely denied by checking they still
// leak, not by assuming a passing exit means the gate fired.
func TestSelfHostNestedStructFieldHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// #6148's use-after-free, reconstructed. `parse_one` returns a fresh
			// PS whose `node` field is a sole-owned literal, so the construction
			// retain correctly does not inc it; `items.append(p.node)` then takes
			// an uncounted alias. Adding the field arms WITHOUT the scan made the
			// next iteration's rebind free a Node the tree still pointed at —
			// exit 134 on wasm and 139 on x86-64 in that attempt. The scan
			// excludes PS twice over: a struct-literal field value and a call
			// argument.
			name: "fresh_field_aliased_into_container",
			src: `struct Node { kind: i32, items: i32[] }
struct PS { node: Node, pos: i32 }
function parse_one(pos: i32): PS { return PS { node: Node { kind: pos, items: [pos, pos + 1] }, pos: pos + 1 }; }
function round(n: i32): i32 {
    var items: Node[] = [];
    var pos: i32 = 0;
    while (pos < n) { var p: PS = parse_one(pos); items = items.append(p.node); pos = p.pos; }
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < items.len()) { t = t + items[k].kind + items[k].items[1]; k = k + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(3); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			// The field is re-homed into ANOTHER struct literal, which the new
			// owner outlives. strfld's walker never visits ExprStructLit at all,
			// so this position had to be added for this scan.
			name: "field_rehomed_into_struct_literal",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
struct Holder { g: Inner, m: i32 }
function round(i: i32): i32 {
    var keep: Holder[] = [];
    var o: Outer = Outer { f: Inner { v: i }, n: i };
    var j: i32 = 0;
    while (j < 3) { o = Outer { f: Inner { v: i + j }, n: j }; keep = keep.append(Holder { g: o.f, m: j }); j = j + 1; }
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < keep.len()) { t = t + keep[k].g.v + keep[k].m; k = k + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			// A functional-update BASE copies every field pointer into the new
			// box, so `Outer { ...o, n: j }` gives o's Inner a second owner. The
			// has_base arm of the struct-literal walk is what catches this.
			name: "functional_update_base_copies_field",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
function round(i: i32): i32 {
    var keep: Outer[] = [];
    var o: Outer = Outer { f: Inner { v: i }, n: i };
    var j: i32 = 0;
    while (j < 3) { keep = keep.append(Outer { ...o, n: j }); o = Outer { f: Inner { v: i + j }, n: j }; j = j + 1; }
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < keep.len()) { t = t + keep[k].f.v + keep[k].n; k = k + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 21,
		},
		{
			// A borrow CHAIN (`o.f.v`) must stay admitted — this is the case that
			// forced a struct-specific walker, since strfld_mark would have marked
			// `f` here and excluded Outer. Granted, and the answer must be right.
			name: "borrow_chain_still_granted",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
function round(i: i32): i32 {
    var o: Outer = Outer { f: Inner { v: i }, n: i };
    var j: i32 = 0;
    while (j < 4) { o = Outer { f: Inner { v: i + j }, n: j }; j = j + 1; }
    return o.f.v + o.n;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 21,
		},
		{
			// The field passed as a call argument — the callee may retain it, so
			// the type is excluded. Single-bind here, which already reclaimed
			// before this change; pinned so the exclusion cannot silently flip.
			name: "field_passed_to_call",
			src: `struct Inner { v: i32 }
struct Outer { f: Inner, n: i32 }
function sink(x: Inner): i32 { return x.v; }
function round(i: i32): i32 {
    var o: Outer = Outer { f: Inner { v: i }, n: i };
    return sink(o.f) + o.n;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
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

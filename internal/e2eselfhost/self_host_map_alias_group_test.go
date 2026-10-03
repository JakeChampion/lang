package e2eselfhost

import (
	"strings"
	"testing"
)

// --- map alias groups: who owns the box a clone superseded (#7235) ----------
//
// A mapbox carries no rc word, so `let q: Map[..] = m` is an UNCOUNTED share and
// the release of every box the pair reaches has to be decided statically: m, its
// plain aliases and the local tuples holding it at an element form a group, and
// each box the group reaches is freed exactly once. An insert clones only when an
// alias can be live at it.
//
// Every want was confirmed against bin/fern -interp. Counts are the x86-64
// leakcheck census at the row's round count, and every row balances.

type mapAliasGroupCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func mapAliasGroupMain(rounds string) string {
	return "\nfunction main(): i32 { let x: i32 = 0; let r: i32 = 0; " +
		"while (r < " + rounds + ") { x = x + round(r); r = r + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"
}

func mapAliasGroupCases() []mapAliasGroupCase {
	const repro = `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    let q: Map[string, i32] = m;
    m = m.insert("k", i);
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}`
	return []mapAliasGroupCase{
		{
			// #7235's repro: the alias holds the old box, m the clone.
			name: "alias_before_insert",
			src:  repro + mapAliasGroupMain("100"),
			want: 64, allocs: 400, frees: 400,
		},
		{
			// The same at 200 rounds — flat, not merely smaller.
			name: "alias_before_insert_200",
			src:  repro + mapAliasGroupMain("200"),
			want: 43, allocs: 800, frees: 800,
		},
		{
			// No alias is live at the insert, so it mutates in place and no clone
			// exists: half the allocations of alias_before_insert.
			name: "alias_after_insert",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    let q: Map[string, i32] = m;
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 17, allocs: 200, frees: 200,
		},
		{
			// The clone happens on half the rounds: q holds the old box on those
			// and m's box on the rest.
			name: "alias_and_conditional_insert",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    let q: Map[string, i32] = m;
    if (i % 2 == 0) { m = m.insert("k", i); }
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 11, allocs: 300, frees: 300,
		},
		{
			// Three inserts per round after the alias.
			name: "alias_before_a_loop_of_inserts",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    let q: Map[string, i32] = m;
    let k: i32 = 0;
    while (k < 3) { m = m.insert("k", i + k); k = k + 1; }
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 82, allocs: 400, frees: 400,
		},
		{
			// Two aliases bound around two inserts: q keeps the first box, r the
			// first clone, m the second.
			name: "two_aliases_two_inserts",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    let q: Map[string, i32] = m;
    m = m.insert("k", i);
    let r: Map[string, i32] = m;
    m = m.insert("k", i + 1);
    return (q.get_or("k", 0) + r.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 26, allocs: 600, frees: 600,
		},
		{
			// THE WRONG-ANSWER CASE for a positional alias scan. The alias is bound
			// textually AFTER the insert, but the loop's back edge makes it live at
			// the next iteration's insert, so every insert must clone and each
			// snapshot keeps its own box: 0+1+2+3 = 6. In-place would hand all four
			// the same box: 12.
			name: "alias_in_enclosing_loop_snapshot",
			src: `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    let seen: Map[string, i32][] = [];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        m = m.insert("k", i);
        let alias: Map[string, i32] = m;
        seen = seen.append(alias);
        i = i + 1;
    }
    let j: i32 = 0;
    while (j < seen.len()) { acc = acc + seen[j].get_or("k", 0); j = j + 1; }
    return acc + __rc_underflow_count() * 100;
}`,
			want: 6, allocs: 9, frees: 9,
		},
		{
			// A string VALUE column shared between the clone and the alias.
			name: "string_values",
			src: `import "core/map";
import "std/i32";
function round(i: i32): i32 {
    let m: Map[string, string] = map_new(4);
    let q: Map[string, string] = m;
    m = m.insert("k", i.to_string());
    return (q.get_or("k", "").len() + m.get_or("k", "").len() + i) % 91;
}` + mapAliasGroupMain("100"),
			want: 72, allocs: 600, frees: 600,
		},
		{
			// Fresh string KEYS as well, one insert before the alias and one after.
			name: "string_keys",
			src: `import "core/map";
import "std/i32";
function round(i: i32): i32 {
    let m: Map[string, string] = map_new(4);
    m = m.insert(i.to_string(), i.to_string());
    let q: Map[string, string] = m;
    m = m.insert((i + 1).to_string(), (i + 1).to_string());
    return (q.len() + m.len() + m.get_or(i.to_string(), "").len() + i) % 91;
}` + mapAliasGroupMain("100"),
			want: 16, allocs: 1400, frees: 1400,
		},
		{
			// The #7212 shape: a tuple holds the old box at an element.
			name: "tuple_element",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    let t: (i32, Map[string, i32]) = (i, m);
    m = m.insert("k", i);
    return (t.0 + t.1.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 17, allocs: 500, frees: 500,
		},
		{
			// CONTROL: never aliased; the in-place insert and the plain release.
			name: "never_aliased",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    return m.get_or("k", 0) % 91;
}` + mapAliasGroupMain("100"),
			want: 64, allocs: 200, frees: 200,
		},
		{
			// An alias declared inside a loop body, re-bound every iteration.
			name: "alias_declared_in_loop_refused",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 2) { let q: Map[string, i32] = m; m = m.insert("k", i + k); acc = acc + q.get_or("k", 0); k = k + 1; }
    return (acc + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 26, allocs: 600, frees: 600,
		},
		{
			// A chain: `let r = q` aliases the alias.
			name: "alias_chain_refused",
			src: `import "core/map";
function round(i: i32): i32 {
    let m: Map[string, i32] = map_new(4);
    let q: Map[string, i32] = m;
    let r: Map[string, i32] = q;
    m = m.insert("k", i);
    return (q.get_or("k", 0) + r.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 64, allocs: 400, frees: 400,
		},
	}
}

// TestSelfHostMapAliasGroupX86_64 — the census leg, plus a second build of each
// row under FERN_SANITIZE=1: an identity guard that frees a box another holder
// still reads is an over-release into a freelist, which the census cannot see.
func TestSelfHostMapAliasGroupX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapAliasGroupCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
			stderr, exit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "mapgrp_"+tc.name, asm))
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; a wrong sum means an "+
					"insert mutated a box an alias still read)", tc.name, exit, tc.want)
			}
			allocs, frees, _ := parseLeakcheck(t, tc.name, stderr)
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: allocs=%d, want %d (MORE on an in-place row means a clone "+
					"came back; FEWER on a clone row means one stopped)", tc.name, allocs, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: frees=%d, want %d (FEWER means a holder lost its release)", tc.name, frees, tc.frees)
			}

			sanAsm := cli.emit(t, "x86-64-linux", tc.src, "FERN_SANITIZE=1")
			sanErr, sanExit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "mapgrp_san_"+tc.name, sanAsm))
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

// TestSelfHostMapAliasGroupWasmIR — the wasm sibling. Exit codes only: the
// answer is what proves each insert cloned exactly when an alias was live.
func TestSelfHostMapAliasGroupWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapAliasGroupCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("map alias-group wasm %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostMapAliasGroupIRArm64 — the arm64 sibling under qemu.
func TestSelfHostMapAliasGroupIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapAliasGroupCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

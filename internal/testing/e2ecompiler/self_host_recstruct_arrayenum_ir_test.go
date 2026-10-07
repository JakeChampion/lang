package e2ecompiler

import "testing"

// recStructArrayEnumPrelude is the #3720 repro frame: a pair of mutually-
// recursive functions that build and return a struct (`PS`) whose field is an
// enum (`Tok`) with an array payload (`Many(Tok[])`). Returning the nested
// array-payload enum through the struct from the re-entrant recursive call
// needs the enum constructor to retain the `items` array buffer it stores;
// without that retain the exit dec-sweep frees it out from under the returned
// box (a UAF that `count` then walks into unbounded recursion). Each case below
// varies only main()'s input; `count` returns the number of leaf `One` tokens,
// oracle-checked against the interpreter.
const recStructArrayEnumPrelude = `enum Tok { One(i32), Many(Tok[]) }
struct PS { node: Tok, pos: i32 }

function parse_one(s: string, i: i32): PS {
    if (s[i] == 40) {
        let inner: PS = parse_many(s, i + 1);
        let pos: i32 = inner.pos;
        if (pos < s.len() && s[pos] == 41) { pos = pos + 1; }
        return PS { node: inner.node, pos: pos };
    }
    return PS { node: One(s[i] as i32), pos: i + 1 };
}
function parse_many(s: string, i: i32): PS {
    let items: Tok[] = [];
    let pos: i32 = i;
    while (pos < s.len() && s[pos] != 41) {
        let p: PS = parse_one(s, pos);
        items = items.append(p.node);
        pos = p.pos;
    }
    if (items.len() == 1) { return PS { node: items[0], pos: pos }; }
    return PS { node: Many(items), pos: pos };
}
function count(t: Tok): i32 {
    match (t) {
        One(_) => { return 1; },
        Many(xs) => { let c: i32 = 0; let k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; }
    }
}
`

var recStructArrayEnumCases = []struct {
	name string
	main string
}{
	// Group first, then a trailing atom: leaves a,b,c -> 3 (the canonical crash).
	{"group-first", `function main(): i32 { let r: PS = parse_many("(ab)c", 0); return count(r.node); }`},
	// Group only, inner is a 2-element Many: a,b -> 2.
	{"group-only", `function main(): i32 { let r: PS = parse_many("(ab)", 0); return count(r.node); }`},
	// Single-element group (inner is a bare One, no Many wrapper): a -> 1.
	{"single-group", `function main(): i32 { let r: PS = parse_many("(a)", 0); return count(r.node); }`},
	// Group not first: z,a,b,c -> 4 (was fine before the fix; regression guard).
	{"group-not-first", `function main(): i32 { let r: PS = parse_many("z(ab)c", 0); return count(r.node); }`},
	// No group at all: a,b,c -> 3.
	{"no-group", `function main(): i32 { let r: PS = parse_many("abc", 0); return count(r.node); }`},
	// Nested groups: a,b,c -> 3 (deeper recursive struct returns).
	{"nested-group", `function main(): i32 { let r: PS = parse_many("((ab)c)", 0); return count(r.node); }`},
}

// TestSelfHostRecStructArrayEnumIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostRecStructArrayEnumIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range recStructArrayEnumCases {
		src := recStructArrayEnumPrelude + tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}

package e2ecompiler

import "testing"

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
	{"group-first", `function main(): i32 { let r: PS = parse_many("(ab)c", 0); return count(r.node); }`},
	{"group-only", `function main(): i32 { let r: PS = parse_many("(ab)", 0); return count(r.node); }`},
	{"single-group", `function main(): i32 { let r: PS = parse_many("(a)", 0); return count(r.node); }`},
	{"group-not-first", `function main(): i32 { let r: PS = parse_many("z(ab)c", 0); return count(r.node); }`},
	{"no-group", `function main(): i32 { let r: PS = parse_many("abc", 0); return count(r.node); }`},
	{"nested-group", `function main(): i32 { let r: PS = parse_many("((ab)c)", 0); return count(r.node); }`},
}

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

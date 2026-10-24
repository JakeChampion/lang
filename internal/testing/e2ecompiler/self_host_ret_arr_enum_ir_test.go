package e2ecompiler

import "testing"

var retArrEnumIRCases = []struct {
	name string
	src  string
	want int
}{
	{"mk_append_grow", `enum Tok { One(i32), Many(Tok[]) }
function mk(): Tok { let a: Tok[] = [One(97), One(98)]; return Many(a); }
function count(t: Tok): i32 { match (t) { One(_) => { return 1; }, Many(xs) => { let c: i32 = 0; let k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; } } }
function main(): i32 { let items: Tok[] = []; items = items.append(mk()); items = items.append(One(99)); return count(items[0]) + count(items[1]); }`, 3},
	{"mk_append_wrap", `enum Tok { One(i32), Many(Tok[]) }
function mk(): Tok { let a: Tok[] = [One(97), One(98)]; return Many(a); }
function count(t: Tok): i32 { match (t) { One(_) => { return 1; }, Many(xs) => { let c: i32 = 0; let k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; } } }
function main(): i32 { let items: Tok[] = []; items = items.append(mk()); items = items.append(One(99)); return count(Many(items)); }`, 3},
	{"recursive_parser", `enum Tok { One(i32), Many(Tok[]) }
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
function main(): i32 { let r: PS = parse_many("(ab)c", 0); return count(r.node); }`, 3},
	{"struct_field_enum", `enum Tok { One(i32), Many(Tok[]) }
struct W { node: Tok }
function mk(): W { let a: Tok[] = [One(1), One(2), One(3)]; return W { node: Many(a) }; }
function count(t: Tok): i32 { match (t) { One(_) => { return 1; }, Many(xs) => { let c: i32 = 0; let k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; } } }
function main(): i32 { let w: W = mk(); let pad: Tok[] = []; pad = pad.append(One(9)); pad = pad.append(One(9)); return count(w.node) + pad.len(); }`, 5},
}

func TestSelfHostRetArrEnumIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range retArrEnumIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

package e2e

import (
	"strconv"
	"testing"
)

const variantPayloadPrelude = `import "std/i32";
enum T { Leaf, Node(T, string, T) }
enum A { ALeaf, ANode(A, i32[], A) }
@noinline
function mk(i: i32): T { return Node(Leaf, "k", Leaf); }
@noinline
function wrap(l: T, k: string, r: T): T { return Node(l, k, r); }
@noinline
function mkarr(i: i32): i32[] { return [i, i, i]; }
@noinline
function wrapa(l: A, xs: i32[], r: A): A { return ANode(l, xs, r); }
@noinline
function keylen(t: T): i32 {
    match (t) { Node(l, k, r) => { return k.len(); }, Leaf => { return 0; } }
}
@noinline
function arrlen(t: A): i32 {
    match (t) { ANode(l, xs, r) => { return xs.len(); }, ALeaf => { return 0; } }
}
@noinline
function ins(t: T, k: string): T {
    match (t) {
        Leaf => { return Node(Leaf, k, Leaf); },
        Node(l, nk, r) => { let nl: T = ins(l, k); return wrap(nl, nk, r); },
    }
}
@noinline
function depth(t: T): i32 {
    match (t) { Node(l, k, r) => { return 1 + depth(l); }, Leaf => { return 0; } }
}
`

const variantPayloadEnumTempBody = variantPayloadPrelude + `
function round(i: i32): i32 { return keylen(wrap(mk(i), "k", Leaf)); }
`

const variantPayloadStrTempBody = variantPayloadPrelude + `
function digits(i: i32): i32 { if (i >= 100) { return 3; } if (i >= 10) { return 2; } return 1; }
function round(i: i32): i32 { return keylen(wrap(Leaf, "key-value-" + i.to_string(), Leaf)) - 9 - digits(i); }
`

const variantPayloadArrTempBody = variantPayloadPrelude + `
function round(i: i32): i32 { return arrlen(wrapa(ALeaf, mkarr(i), ALeaf)) - 2; }
`

const variantPayloadLiveBody = variantPayloadPrelude + `
function round(i: i32): i32 {
    let l: T = mk(i);
    let t: T = wrap(l, "k", Leaf);
    return keylen(t) + keylen(l) - 1;
}
`

const variantPayloadInsertBody = variantPayloadPrelude + `
function round(i: i32): i32 {
    let t: T = Leaf;
    t = ins(t, "a");
    t = ins(t, "b");
    t = ins(t, "c");
    t = ins(t, "d");
    return depth(t) - 3;
}
`

func variantPayloadMain(rounds, want int) string {
	return `function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { acc = acc + round(r); r = r + 1; }
    if (acc != ` + strconv.Itoa(want) + `) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 0;
}`
}

var variantPayloadCases = []struct {
	name string
	body string
}{
	{"enum_temp_into_payload", variantPayloadEnumTempBody},
	{"heap_string_temp_into_payload", variantPayloadStrTempBody},
	{"array_temp_into_payload", variantPayloadArrTempBody},
	{"live_payload_source", variantPayloadLiveBody},
	{"insert_chain", variantPayloadInsertBody},
}

func TestX86_64VariantPayloadArgTempReclaim(t *testing.T) {
	for _, tc := range variantPayloadCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, rounds := range []int{100, 200, 400} {
				name := tc.name + "/" + strconv.Itoa(rounds)
				allocs, frees, live := leakCounts(t, name, tc.body+variantPayloadMain(rounds, rounds), 0)
				if live != 0 || allocs != frees {
					t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census — "+
						"a fresh temp stored into a variant payload must be released once the callee has retained it",
						name, allocs, frees, live)
				}
			}
		})
	}
}

package e2e

import (
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A FRESH call-result temp handed to a BORROWED parameter that the callee
// stores into a variant-constructor payload was never released. emitEnumNew
// inc's the payload, so the temp sat at rc 2, and the caller emitted no
// post-call release because paramCountedRetain had no rule crediting a
// variant-constructor store. `wrap(ins(l, k), k, r)` stranded the `ins(l, k)`
// node on every ordered-map insert whose node holds a string key; a heap
// string temp and an array temp into a payload leaked the same way.
//
// The credit now mirrors emitEnumNew's inc gate exactly
// (internal/ir/variant_payload_counted_retain_test.go), and a variant
// construction counts as a fresh box in findReturnsFreshBox, so the local an
// insert binds to the recursive result stays reclaimable.
//
// Each case is loop-resident and measured at three counts, and the exit code
// folds in `__rc_underflow_count()` because a census is blind to an
// over-release — the real hazard of a new credit. `@noinline` keeps the
// argument temp in existence (docs/TEST-GATES.md).
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
        Node(l, nk, r) => { var nl: T = ins(l, k); return wrap(nl, nk, r); },
    }
}
@noinline
function depth(t: T): i32 {
    match (t) { Node(l, k, r) => { return 1 + depth(l); }, Leaf => { return 0; } }
}
`

// The reported shape: an enum temp into a payload of a string-keyed node.
const variantPayloadEnumTempBody = variantPayloadPrelude + `
function round(i: i32): i32 { return keylen(wrap(mk(i), "k", Leaf)); }
`

// A heap string temp (past the 7-byte inline threshold) into the payload.
// The key is 10 bytes plus the digits of i; digits(i) makes the round's
// value 1 regardless.
const variantPayloadStrTempBody = variantPayloadPrelude + `
function digits(i: i32): i32 { if (i >= 100) { return 3; } if (i >= 10) { return 2; } return 1; }
function round(i: i32): i32 { return keylen(wrap(Leaf, "key-value-" + i.to_string(), Leaf)) - 9 - digits(i); }
`

// An array temp into the payload.
const variantPayloadArrTempBody = variantPayloadPrelude + `
function round(i: i32): i32 { return arrlen(wrapa(ALeaf, mkarr(i), ALeaf)) - 2; }
`

// The payload source is a local READ AFTER the call, so no move applies and
// the construction's inc is what keeps it alive: an over-release here reads
// freed memory, caught by the value check and the underflow counter.
const variantPayloadLiveBody = variantPayloadPrelude + `
function round(i: i32): i32 {
    var l: T = mk(i);
    var t: T = wrap(l, "k", Leaf);
    return keylen(t) + keylen(l) - 1;
}
`

// The ordered-map insert chain: a recursive insert binds its result to a local
// and hands it to a node-building helper — the `var nl = __om_insert(l, k, v);
// return __om_balance(nl, ..)` shape. Four inserts build a left spine of depth 4.
const variantPayloadInsertBody = variantPayloadPrelude + `
function round(i: i32): i32 {
    var t: T = Leaf;
    t = ins(t, "a");
    t = ins(t, "b");
    t = ins(t, "c");
    t = ins(t, "d");
    return depth(t) - 3;
}
`

func variantPayloadMain(rounds, want int) string {
	return `function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
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

// The wasm leg has no alloc census; the bump high-water mark is the same
// signal — one round's working set, equal for 20 and 200 rounds.
func variantPayloadBumpSrc(body string, rounds int) string {
	return body + `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var r: i32 = 0;
    var sum: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { sum = sum + round(r); r = r + 1; }
    if (sum != ` + strconv.Itoa(rounds) + `) { return 201; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMVariantPayloadArgTempBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, tc := range variantPayloadCases {
		t.Run(tc.name, func(t *testing.T) {
			small := runWasm(t, variantPayloadBumpSrc(tc.body, 20))
			large := runWasm(t, variantPayloadBumpSrc(tc.body, 200))
			if small == 201 || large == 201 {
				t.Fatalf("value-incorrect run: small=%d large=%d", small, large)
			}
			if small != large {
				t.Errorf("a variant-payload arg temp must be O(1) heap, got rounds=20 -> %d, rounds=200 -> %d (leak)", small, large)
			}
			if small == 0 {
				t.Errorf("expected a non-zero working set, got 0 (probe not exercising the heap)")
			}
		})
	}
}

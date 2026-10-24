package e2e

import (
	"strconv"
	"testing"
)

const variantReclaimPrelude = `enum E { A(string), B }
@noinline
function sink(e: E): i32 {
    match (e) {
        E.A(s) => { return s.len(); },
        E.B => { return 0; },
    }
}
@noinline
function mkpayload(base: string): string { return base + "-payloadpayload"; }
`

func variantCtorArgSrc(rounds int) string {
	return variantReclaimPrelude + `
function round(i: i32): i32 {
    let base: string = "shared-left-operand";
    return sink(E.A(mkpayload(base)));
}
` + variantChurnMain(rounds, rounds*34)
}

func variantCtorArgLivePayloadSrc(rounds int) string {
	return variantReclaimPrelude + `
function round(i: i32): i32 {
    let base: string = "shared-left-operand";
    let live: string = mkpayload(base);
    let n: i32 = sink(E.A(live));
    return n + live.len() - 34;
}
` + variantChurnMain(rounds, rounds*34)
}

func variantCtorReturnedSrc(rounds int) string {
	return variantReclaimPrelude + `
@noinline
function keepf(e: E): E { return e; }
function round(i: i32): i32 {
    let base: string = "shared-left-operand";
    let r: E = keepf(E.A(mkpayload(base)));
    match (r) {
        E.A(s) => { return s.len(); },
        E.B => { return 0; },
    }
}
` + variantChurnMain(rounds, rounds*34)
}

func variantPayloadlessArgSrc(rounds int) string {
	return variantReclaimPrelude + `
function round(i: i32): i32 {
    let base: string = "shared-left-operand";
    return sink(E.B) + sink(E.A(mkpayload(base)));
}
` + variantChurnMain(rounds, rounds*34)
}

func variantCtorNestedSrc(rounds int) string {
	return variantReclaimPrelude + `
@noinline
function sink2(a: i32, e: E, b: i32): i32 {
    match (e) {
        E.A(s) => { return s.len() + a + b; },
        E.B => { return a + b; },
    }
}
function round(i: i32): i32 {
    let base: string = "shared-left-operand";
    return sink2(1, E.A(mkpayload(base)), 2) - 3;
}
` + variantChurnMain(rounds, rounds*34)
}

func variantChurnMain(rounds, want int) string {
	return `function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { acc = acc + round(r); r = r + 1; }
    if (acc != ` + strconv.Itoa(want) + `) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 0;
}`
}

func TestX86_64VariantCtorArgReclaim(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(int) string
	}{
		{"variant_ctor_at_arg", variantCtorArgSrc},
		{"variant_ctor_live_payload", variantCtorArgLivePayloadSrc},
		{"variant_ctor_returned_by_callee", variantCtorReturnedSrc},
		{"variant_payloadless_at_arg", variantPayloadlessArgSrc},
		{"variant_ctor_nested_args", variantCtorNestedSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rounds := range []int{100, 200, 400} {
				name := tc.name + "/" + strconv.Itoa(rounds)
				allocs, frees, live := leakCounts(t, name, tc.src(rounds), 0)
				if live != 0 || allocs != frees {
					t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census — "+
						"a fresh variant-constructor temp at an argument position must be reclaimed "+
						"once the callee has borrowed it",
						name, allocs, frees, live)
				}
			}
		})
	}
}

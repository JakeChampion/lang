package e2e

import (
	"strconv"
	"testing"
)

func enumUnionReplaceSrc(rounds int) string {
	return `struct A { s: i32[], n: i32 }
struct B { x: i32[], y: i32[] }
enum E { VA(A), VB(B) }
function step(k: i32): i32 {
    let e: E = VB(B { x: [k, k + 1], y: [k + 2, k + 3] });
    e = VA(A { s: [k + 4, k + 5], n: k });
    match (e) { VA(a) => { return a.s[0] + a.n; }, VB(b) => { return b.x[0]; } }
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < ` + strconv.Itoa(rounds) + `) { acc = acc + step(i); i = i + 1; }
    if (acc != ` + strconv.Itoa(rounds*(rounds-1)+4*rounds) + `) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 0;
}`
}

func TestX86_64EnumUnionReplaceReclaimsDisplacedPayload(t *testing.T) {
	for _, rounds := range []int{100, 200, 400} {
		name := "rounds=" + strconv.Itoa(rounds)
		allocs, frees, live := leakCounts(t, name, enumUnionReplaceSrc(rounds), 0)
		if live != 0 || allocs != frees {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census — a replaced union "+
				"payload must be released through ITS OWN type's drop, not the first variant's",
				name, allocs, frees, live)
		}
	}
}

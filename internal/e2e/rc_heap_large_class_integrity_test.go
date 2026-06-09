package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Large-block size-class integrity. The large tier rounds a >2048 B request up
// to 3 significant bits and bins it by that rounded capacity; alloc and free
// must compute the SAME class, and a popped block must be at least as large as
// the request. A wrong class (e.g. an off-by-one in the bsr/clz mantissa math)
// would hand back a too-small block and corrupt the next array's contents — so
// this builds arrays of many sizes spanning the large tier and its class
// boundaries, in a reuse-heavy loop, and verifies every element. It returns 0
// iff every read matches what was written.
//
// Guards both the x86-64 (bsr) and arm64 (clz) large-tier class arithmetic.
const largeClassIntegritySrc = `
function build(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i * 3 + 1); i = i + 1; }
    return a;
}
function check(n: i32): i32 {
    var a: i32[] = build(n);
    var i: i32 = 0;
    while (i < n) { if (a[i] != i * 3 + 1) { return 1; } i = i + 1; }
    return 0;
}
function main(): i32 {
    var k: i32 = 0;
    var bad: i32 = 0;
    while (k < 500) {
        bad = bad + check(520) + check(540) + check(600) + check(700) +
              check(1000) + check(1030) + check(1300) + check(2050) +
              check(3000) + check(5000) + check(8200);
        k = k + 1;
    }
    return bad;
}`

func TestX86_64LargeClassIntegrity(t *testing.T) {
	ast.RcFreeEnabled = true
	if got := mustRunX86_64FreeOn(t, largeClassIntegritySrc); got != 0 {
		t.Errorf("large-tier size-class integrity violated: got %d (expected 0; a non-zero result means a reused block was too small / mis-binned)", got)
	}
}

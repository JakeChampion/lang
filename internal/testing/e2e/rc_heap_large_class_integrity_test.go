package e2e

import (
	"testing"
)

const largeClassIntegritySrc = `
function build(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i * 3 + 1); i = i + 1; }
    return a;
}
function check(n: i32): i32 {
    let a: i32[] = build(n);
    let i: i32 = 0;
    while (i < n) { if (a[i] != i * 3 + 1) { return 1; } i = i + 1; }
    return 0;
}
function main(): i32 {
    let k: i32 = 0;
    let bad: i32 = 0;
    while (k < 500) {
        bad = bad + check(520) + check(540) + check(600) + check(700) +
              check(1000) + check(1030) + check(1300) + check(2050) +
              check(3000) + check(5000) + check(8200);
        k = k + 1;
    }
    return bad;
}`

func TestX86_64LargeClassIntegrity(t *testing.T) {
	if got := mustRunX86_64FreeOn(t, largeClassIntegritySrc); got != 0 {
		t.Errorf("large-tier size-class integrity violated: got %d (expected 0; a non-zero result means a reused block was too small / mis-binned)", got)
	}
}

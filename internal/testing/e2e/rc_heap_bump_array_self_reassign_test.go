package e2e

import (
	"testing"
)

func arrSelfReassignSrc(iters string) string {
	return `
function build(k: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(i); i = i + 1; }
    return a;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    let sum: i32 = 0;
    while (j < ` + iters + `) {
        let a: i32[] = build(600);
        sum = sum + a[0];
        j = j + 1;
    }
    if (sum != 0) { return 201; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64ArraySelfReassignReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, arrSelfReassignSrc("20"))
	large := mustRunX86_64FreeOn(t, arrSelfReassignSrc("400"))
	if small == 201 || large == 201 {
		t.Fatalf("value-incorrect run (a[0] != 0): small=%d large=%d", small, large)
	}
	if small != large {
		t.Errorf("array self-reassign push-grow must be O(1) heap, got iters=20 -> %d, iters=400 -> %d (leak)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero working set for build(600); got 0 (probe not exercising the heap)")
	}
}

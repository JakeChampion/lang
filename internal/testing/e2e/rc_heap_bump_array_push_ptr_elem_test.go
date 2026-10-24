package e2e

import (
	"testing"
)

func strArrPushSrc(iters string) string {
	return `
function build(k: i32): string[] {
    let a: string[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append("item"); i = i + 1; }
    return a;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    let n: i32 = 0;
    while (j < ` + iters + `) {
        let a: string[] = build(300);
        n = n + a.len();
        j = j + 1;
    }
    if (n != 300 * ` + iters + `) { return 201; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func structArrPushSrc(iters string) string {
	return `
struct Item { tag: i32 }
function build(k: i32): Item[] {
    let a: Item[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(Item { tag: i }); i = i + 1; }
    return a;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    let sum: i32 = 0;
    while (j < ` + iters + `) {
        let a: Item[] = build(300);
        sum = sum + a[0].tag;
        j = j + 1;
    }
    if (sum != 0) { return 202; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func runArrayPushPtrElemChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"string", strArrPushSrc},
		{"struct", structArrPushSrc},
	} {
		t.Run(c.name, func(t *testing.T) {
			small := run(t, c.src("20"))
			large := run(t, c.src("400"))
			if small == 201 || small == 202 || large == 201 || large == 202 {
				t.Fatalf("value-incorrect run: small=%d large=%d", small, large)
			}
			if small != large {
				t.Errorf("%s[] self-append must be O(1) heap: iters=20 -> %d, iters=400 -> %d (leak)", c.name, small, large)
			}
			if small == 0 {
				t.Errorf("expected a non-zero working set for build(300); got 0")
			}
		})
	}
}

func TestX86_64ArrayPushPtrElemReclaim(t *testing.T) {
	runArrayPushPtrElemChecks(t, mustRunX86_64FreeOn)
}

func TestArm64ArrayPushPtrElemReclaim(t *testing.T) {
	runArrayPushPtrElemChecks(t, mustRunArm64FreeOn)
}

func TestWASMArrayPushPtrElemReclaim(t *testing.T) {
	runArrayPushPtrElemChecks(t, func(t *testing.T, src string) int {
		return runWasm(t, src)
	})
}

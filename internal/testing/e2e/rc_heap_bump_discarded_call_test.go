package e2e

import (
	"strconv"
	"strings"
	"testing"
)

func discardedCallStructBump(n string) string {
	return `import "std/i32";
struct P { x: i32, y: i32, values: i32[] }
@noinline function mk(v: i32): P { return P { x: v, y: v, values: [v, v + 1] }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    let bump = (__heap_bump_bytes() as i32) - before;
    print("bump=" + bump.to_string());
    return 0;
}`
}

func discardedCallScalarStructBump(n string) string {
	return `import "std/i32";
struct P { x: i32, y: i32 }
@noinline function mk(v: i32): P { return P { x: v, y: v }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    let bump = (__heap_bump_bytes() as i32) - before;
    print("bump=" + bump.to_string());
    return 0;
}`
}

func discardedCallArrBump(n string) string {
	return `import "std/i32";
@noinline function mk(v: i32): i32[] { return [v, v + 1, v + 2]; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    let bump = (__heap_bump_bytes() as i32) - before;
    print("bump=" + bump.to_string());
    return 0;
}`
}

const discardedCallAliasedSafe = `@noinline function pass(p: i32[]): i32[] { return p; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    let arr: i32[] = [10, 20, 30];
    while (i < 200) {
        pass(arr);
        acc = acc + arr[0] + arr[1] + arr[2];
        i = i + 1;
    }
    if (acc != 12000) { return 999; }
    return __rc_underflow_count();
}`

const discardedCallFreshSafe = `struct P { x: i32, y: i32 }
function mk(n: i32): P { return P { x: n, y: n + 1 }; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        mk(i);
        let p: P = mk(i);
        acc = acc + p.x + p.y;
        i = i + 1;
    }
    // sum over i=0..199 of (i + (i+1)) = 2*19900 + 200 = 40000
    if (acc != 40000) { return 999; }
    return __rc_underflow_count();
}`

func checkDiscardedCallSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, discardedCallAliasedSafe); code != 0 {
		t.Errorf("aliased-return safety: code=%d (999=value mismatch, >0=over-release/UAF)", code)
	}
	if _, code := run(t, discardedCallFreshSafe); code != 0 {
		t.Errorf("fresh-result reclaim: code=%d", code)
	}
}

func TestX86_64DiscardedCallReclaim(t *testing.T) {
	checkDiscardedCallSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64DiscardedCallReclaim(t *testing.T) {
	checkDiscardedCallSafe(t, compileAndRunArm64FreeOn)
}

func TestWASMDiscardedCallReclaim(t *testing.T) {
	bump := func(src string) int {
		t.Helper()
		output := strings.TrimSpace(runWasmCapturingStdout(t, src))
		if !strings.HasPrefix(output, "bump=") {
			t.Fatalf("missing high-water measurement: %q", output)
		}
		value, err := strconv.Atoi(strings.TrimPrefix(output, "bump="))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"struct", discardedCallStructBump},
		{"array", discardedCallArrBump},
	} {
		small := bump(c.src("50"))
		large := bump(c.src("5000"))
		if small != large {
			t.Errorf("%s discarded-call bump should be bounded: N=50 -> %d, N=5000 -> %d", c.name, small, large)
		}
		if small <= 0 {
			t.Errorf("%s: expected a positive bounded high-water, got %d", c.name, small)
		}
	}
	for _, n := range []string{"50", "5000"} {
		if got := bump(discardedCallScalarStructBump(n)); got != 0 {
			t.Errorf("scalar-only discarded result: N=%s allocates %d bytes, want zero", n, got)
		}
	}
	checkDiscardedCallSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}

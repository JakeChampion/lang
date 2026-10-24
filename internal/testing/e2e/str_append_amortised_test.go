package e2e

import "testing"

const strAppendAmortisedSrc = `function churn(n: i32): i32 {
    let piece: string = "abcdefghijklmnopqrstuvwxyz0123456789abcdefgh";
    let out: string = "";
    let i: i32 = 0;
    while (i < n) { out = out + piece; i = i + 1; }
    return out.len();
}
function main(): i32 {
    if (churn(4096) != 180224) { return 2; }
    return 0;
}`

const strAppendAmortisedPieces = 4096

func checkStrAppendAmortised(t *testing.T, stderr string, exit int) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("exit %d, want 0; stderr: %s", exit, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	t.Logf("%d appends: allocs=%d frees=%d live_bytes=%d", strAppendAmortisedPieces, allocs, frees, live)
	if allocs == 0 {
		t.Fatal("no allocations: the probe is not building its string")
	}
	if allocs > strAppendAmortisedPieces/8 {
		t.Errorf("allocs=%d for %d appends — the accumulator is copied per append rather than grown in place", allocs, strAppendAmortisedPieces)
	}
	if allocs != frees || live != 0 {
		t.Errorf("allocs=%d frees=%d live_bytes=%d: the in-place grows must leave the census balanced", allocs, frees, live)
	}
}

func TestX86_64StrAppendAmortised(t *testing.T) {
	_, stderr, exit := runLeakCheckX86_64(t, strAppendAmortisedSrc)
	checkStrAppendAmortised(t, stderr, exit)
}

func TestWASMStrAppendAmortised(t *testing.T) {
	_, stderr, exit := runLeakCheckWasm(t, strAppendAmortisedSrc, false)
	checkStrAppendAmortised(t, stderr, exit)
}

func TestArm64StrAppendAmortised(t *testing.T) {
	_, stderr, exit := runLeakCheckArm64(t, strAppendAmortisedSrc)
	checkStrAppendAmortised(t, stderr, exit)
}

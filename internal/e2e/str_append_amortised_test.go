package e2e

import "testing"

// `out = out + piece` over a uniquely held accumulator allocates once per
// allocator class step, not once per append (#8404): __fern_str_append
// grows in place while the grown length keeps the block's class capacity
// — 16-byte exact fit below 2 KiB, three significant bits above — so 4096
// pieces of 44 bytes (180 KiB) cost ~75 allocations, where the old 16-byte
// in-place test cost one `__fern_strcat` per append, 4096 of them, and a
// whole re-copy of the prefix each time.
//
// The COUNT is the observable, not bump bytes: the large tier recycles each
// superseded copy into the next same-class request, so __heap_bump_bytes
// read linear under the old runtime too. The bound is n/8, sixty-odd times
// the fixed figure and eight times under the old one.
const strAppendAmortisedSrc = `function churn(n: i32): i32 {
    var piece: string = "abcdefghijklmnopqrstuvwxyz0123456789abcdefgh";
    var out: string = "";
    var i: i32 = 0;
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

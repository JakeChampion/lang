package e2eselfhost

import (
	"strings"
	"testing"
)

// `s = s + piece` on a local the frame owns grows its buffer in place while
// the block's size class has room, so 20,000 eight-byte appends allocate only
// at class boundaries: about 290 times, where a fresh concatenation per append
// allocated 20,000 times and copied the whole prefix each time (#9077). The
// program exits 42 when the result is whole and the loop allocated fewer than
// 1,000 times.
const stringAppendInPlaceSrc = `function main(): i32 {
    let s: string = "";
    let a1: i64 = __heap_alloc_count();
    let i: i32 = 0;
    while (i < 20000) { s = s + "abcdefgh"; i = i + 1; }
    let a2: i64 = __heap_alloc_count();
    if (s.len() != 160000) { return 1; }
    if (s[159999] as i32 != 104 || s[80000] as i32 != 97) { return 2; }
    if (a2 - a1 >= 1000) { return 3; }
    return 42;
}`

func TestSelfHostStringAppendInPlace(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, stringAppendInPlaceSrc, target); code != 42 {
				t.Errorf("exited %d, want 42 (3 is an allocation per append)\n%s", code, stderr)
			}
		})
	}
}

// An in-place growth is still one block, so the leak census moves its live
// bytes and leaves its counts alone, as native's does; counting it as a free
// plus an alloc reported one allocation per append. The FERN_RC_TRACE leg
// covers the path where the trace's event hooks have already counted the pair.
const stringAppendCensusSrc = `function churn(n: i32): i32 {
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

func TestSelfHostStringAppendInPlaceCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	legs := []struct {
		target string
		env    []string
	}{
		{"x86-64-linux", []string{"FERN_LEAKCHECK=1"}},
		{"x86-64-linux", []string{"FERN_LEAKCHECK=1", "FERN_RC_TRACE=1"}},
		{"arm64-linux", []string{"FERN_LEAKCHECK=1"}},
	}
	for _, leg := range legs {
		name := leg.target + "/" + strings.Join(leg.env, ",")
		t.Run(name, func(t *testing.T) {
			stderr, code := cli.exitOf(t, stringAppendCensusSrc, leg.target, leg.env...)
			if code != 0 {
				t.Fatalf("exited %d, want 0\n%s", code, stderr)
			}
			allocs, frees, live := parseLeakcheck(t, name, stderr)
			if allocs == 0 || allocs > 4096/8 {
				t.Errorf("allocs=%d for 4096 appends, want 1..512: the census counts each in-place growth as an allocation", allocs)
			}
			if allocs != frees || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d: an in-place growth must leave the census balanced", allocs, frees, live)
			}
		})
	}
}

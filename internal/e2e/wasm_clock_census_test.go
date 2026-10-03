package e2e

import "testing"

// TestWasmClockCensus runs each clock builtin, and the date formatter
// over the wall clock, a hundred times under the wasm leak census: the
// preview-2 wall-clock helpers read the host's datetime record through
// a scratch block they allocate, and one that is not given back is a
// block per read (#9854's `Date` header, formatted once per second,
// read the clock once per wait and leaked one for each).
func TestWasmClockCensus(t *testing.T) {
	loop := func(call string) string {
		return `import "std/time";
function main(): i32 {
    let last: i64 = 1 as i64;
    let s: string = "";
    let i: i32 = 0;
    while (i < 100) {
        ` + call + `
        i = i + 1;
    }
    if (last == (0 as i64)) { return 1; }
    print(s);
    return 0;
}
`
	}
	for _, tc := range []struct{ name, call string }{
		{"now_unix_ms", "last = now_unix_ms();"},
		{"now_ns", "last = now_ns();"},
		{"monotonic_ns", "last = monotonic_ns();"},
		{"instant_now_formatted", "s = time.instant_now().format_http_date();"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			component := buildLeakCheckComponentPrinting(t, loop(tc.call), false, false)
			_, stderr, code := runComponent(t, component, runOpts{})
			allocs, frees, live := leakSummaryIn(t, stderr)
			if code != 0 || allocs != frees || live != 0 {
				t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d", code, allocs, frees, live)
			}
		})
	}
}

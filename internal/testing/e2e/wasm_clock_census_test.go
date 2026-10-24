package e2e

import "testing"

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
			component := buildLeakCheckCLIComponent(t, loop(tc.call), false)
			_, stderr, code := runWasmArtifact(t, component, runOpts{})
			allocs, frees, live := leakSummaryIn(t, stderr)
			if code != 0 || allocs != frees || live != 0 {
				t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d", code, allocs, frees, live)
			}
		})
	}
}

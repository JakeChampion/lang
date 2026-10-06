package e2ecompiler

import "testing"

// TestSelfHostStrSliceRcBoxIRX86_64 pins the slice-view box (#2649): a view
// (`s[a:b]`, `.trim()`, `slice_unchecked`) shares its source's bytes, so
// releasing the view must never free them and nothing may double-free the
// view's own box. A long trim/slice churn keeps the census balanced with zero
// over-releases, and the value is right -> exit 0.
func TestSelfHostStrSliceRcBoxIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)

	// "hello"[1:4] = "ell", copied into an owned string: len 3.
	if _, exit := cli.exitOf(t, `function main(): i32 { let s: string = "hello"; let t: string = slice_unchecked(s, 1, 4) + ""; return t.len(); }
`, "x86-64-linux"); exit != 3 {
		t.Errorf("slice copy exited %d, want 3", exit)
	}

	// 2,000,000 trim + slice iterations. r = "mid" len 3, so bad stays 0, and
	// an over-release ticks __rc_underflow_count() -> 99.
	const churn = `import "std/string";
function churn(n: i32): i32 {
    let s: string = "  mid  ";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let r: str = s.trim();
        if (r.len() != 3) { bad = 1; }
        let sl: str = slice_unchecked(s, 2, 5);
        if (sl.len() != 3) { bad = 1; }
        i = i + 1;
    }
    return bad;
}
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }
`
	stderr, exit := cli.exitOf(t, churn, "x86-64-linux", "FERN_LEAKCHECK=1")
	if exit != 0 {
		t.Fatalf("churn exited %d, want 0 (99 = over-release)\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

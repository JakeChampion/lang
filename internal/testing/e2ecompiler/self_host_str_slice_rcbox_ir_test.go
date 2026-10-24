package e2ecompiler

import "testing"

func TestSelfHostStrSliceRcBoxIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)

	if _, exit := cli.exitOf(t, `function main(): i32 { let s: string = "hello"; let t: string = slice_unchecked(s, 1, 4) + ""; return t.len(); }
`, "x86-64-linux"); exit != 3 {
		t.Errorf("slice copy exited %d, want 3", exit)
	}

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

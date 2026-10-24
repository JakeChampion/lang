package e2e

import "testing"

func TestX86_64StringLocalRecycles(t *testing.T) {
	const localSrc = `function churn(base: string, n: i32): i32 {
    let i: i32 = 0;
    while (i < n) {
        let s: string = base + "_payload_data_here";
        if (s.len() == 0) { return 99; }
        i = i + 1;
    }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn("prefix", 100000); }`
	if _, code := compileAndRunX86_64FreeOn(t, localSrc); code != 0 {
		t.Errorf("string-local churn: got exit %d, want 0 (heap bump < 1 MiB — freed strcat boxes must recycle)", code)
	}

	const nestedSrc = `function churn(a: string, b: string, n: i32): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < n) { acc = acc + (a + b + a + b).len(); i = i + 1; }
    if (acc == 0) { return 99; }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn("longer_string_one_here", "longer_string_two_here", 100000); }`
	if _, code := compileAndRunX86_64FreeOn(t, nestedSrc); code != 0 {
		t.Errorf("nested-concat churn: got exit %d, want 0 (intermediate temps must recycle)", code)
	}
}

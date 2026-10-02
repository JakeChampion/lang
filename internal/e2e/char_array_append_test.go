package e2e

import "testing"

// A `char[]` grown by append (#10953). ElemSizeBytesFor had no case for
// `char`, so append strode its elements at pointer width while the drop and
// every index read used four bytes: on the natives each grown buffer went
// back short of what it took, and the elements the walk reads back are
// checked too. `s.chars()` is that loop.
const charArrayAppendSrc = `import "std/string";
function mk(n: i32): char[] {
    let out: char[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append((65 + i) as char); i = i + 1; }
    return out;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 10) {
        let cs: char[] = mk(3 + i);
        if (cs[2] as i32 != 67 || cs[cs.len() - 1] as i32 != 65 + cs.len() - 1) { return 1; }
        let ds: char[] = "héllo".chars();
        if (ds.len() != 5 || ds[1] as i32 != 233) { return 2; }
        t = t + cs.len() + ds.len();
        i = i + 1;
    }
    return t - 83;
}
`

func TestCharArrayAppendX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, charArrayAppendSrc, 42, runSanitizeX86_64)
}

func TestCharArrayAppendArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, charArrayAppendSrc, 42, runSanitizeArm64)
}

func TestCharArrayAppendWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, charArrayAppendSrc); got != 42 {
		t.Fatalf("wasm exited %d, want 42", got)
	}
}

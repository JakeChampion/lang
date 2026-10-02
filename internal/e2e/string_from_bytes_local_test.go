package e2e

import "testing"

// A local bound from `string_from_bytes_unchecked` (#10954). The builtin always
// copies into a fresh string, but the taint rule took it for one that might
// alias its argument, so the local was never free-eligible and its release
// only decremented: every heap string it built (above the inline limit on
// x86-64, any length on the two-word ABIs) leaked on the path that dropped
// it. Both paths run here: the string dropped in the callee, and the string
// handed back in an Option.
const stringFromBytesLocalSrc = `function conv(b: u8[]): Option[string] {
    let s: string = string_from_bytes_unchecked(b);
    if (s.len() > 10) { return Some(s); }
    return None;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 10) {
        let b: u8[] = __alloc_u8(3 + i);
        match (conv(b)) { Some(x) => { t = t + 100 + x.len() - x.len(); }, None => { t = t + 3; } }
        i = i + 1;
    }
    return t - 182;
}
`

func TestStringFromBytesLocalX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, stringFromBytesLocalSrc, 42, runSanitizeX86_64)
}

func TestStringFromBytesLocalArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, stringFromBytesLocalSrc, 42, runSanitizeArm64)
}

func TestStringFromBytesLocalWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringFromBytesLocalSrc); got != 42 {
		t.Fatalf("wasm exited %d, want 42", got)
	}
}

package e2eselfhost

import "testing"

// prefixIRCases exercise the Eq-driven `starts_with` / `ends_with`
// array prefix/suffix checks through the self-host IR path on x86-64 +
// wasm. The surface is inlined rather than imported: an `Eq` trait +
// i32 / string primitive impls and the `[T: Eq]` bodies. Both compare
// with the `==` operator (scalar compare / `str_eq`, not a method
// call), so they monomorphise and lower cleanly per element type on
// both backends. Each case returns a small deterministic int (<= 125,
// wasm exit-code safe), oracle-checked against the interpreter. #2689.
const prefixIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function starts_with[T: Eq](xs: T[], prefix: T[]): boolean {
    if (prefix.len() > xs.len()) { return false; }
    var i: i32 = 0;
    while (i < prefix.len()) {
        if (xs[i] != prefix[i]) { return false; }
        i = i + 1;
    }
    return true;
}
pub function ends_with[T: Eq](xs: T[], suffix: T[]): boolean {
    var n: i32 = xs.len();
    var m: i32 = suffix.len();
    if (m > n) { return false; }
    var i: i32 = 0;
    while (i < m) {
        if (xs[n - m + i] != suffix[i]) { return false; }
        i = i + 1;
    }
    return true;
}
`

var prefixIRCases = []struct {
	name string
	main string
	want int
}{
	// starts_with i32: match + non-match + too-long -> 1 + 2 + 4 = 7.
	{"starts-i32", `var a: i32[] = [1, 2, 3, 4, 5]; var r: i32 = 0; if (starts_with(a, [1, 2, 3])) { r = r + 1; } if (!starts_with(a, [1, 3])) { r = r + 2; } if (!starts_with([1, 2], [1, 2, 3])) { r = r + 4; } return r;`, 7},
	// ends_with i32: match + non-match -> 1 + 2 = 3.
	{"ends-i32", `var a: i32[] = [1, 2, 3, 4, 5]; var r: i32 = 0; if (ends_with(a, [4, 5])) { r = r + 1; } if (!ends_with(a, [3, 5])) { r = r + 2; } return r;`, 3},
	// string element prefix via str_eq -> 8 + 4 = 12.
	{"starts-string", `var ss: string[] = ["a", "b", "c", "d"]; var r: i32 = 0; if (starts_with(ss, ["a", "b"])) { r = r + 8; } if (!starts_with(ss, ["a", "c"])) { r = r + 4; } return r;`, 12},
	// string element suffix via str_eq -> 9.
	{"ends-string", `var ss: string[] = ["a", "b", "c", "d"]; var r: i32 = 0; if (ends_with(ss, ["c", "d"])) { r = r + 8; } if (!ends_with(ss, ["b", "d"])) { r = r + 1; } return r;`, 9},
}

func prefixIRSrc(mainBody string) string {
	return prefixIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostPrefixIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostPrefixIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range prefixIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, prefixIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

package e2ecompiler

import "testing"

const prefixIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function starts_with[T: Eq](xs: T[], prefix: T[]): boolean {
    if (prefix.len() > xs.len()) { return false; }
    let i: i32 = 0;
    while (i < prefix.len()) {
        if (xs[i] != prefix[i]) { return false; }
        i = i + 1;
    }
    return true;
}
pub function ends_with[T: Eq](xs: T[], suffix: T[]): boolean {
    let n: i32 = xs.len();
    let m: i32 = suffix.len();
    if (m > n) { return false; }
    let i: i32 = 0;
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
	{"starts-i32", `let a: i32[] = [1, 2, 3, 4, 5]; let r: i32 = 0; if (starts_with(a, [1, 2, 3])) { r = r + 1; } if (!starts_with(a, [1, 3])) { r = r + 2; } if (!starts_with([1, 2], [1, 2, 3])) { r = r + 4; } return r;`, 7},
	{"ends-i32", `let a: i32[] = [1, 2, 3, 4, 5]; let r: i32 = 0; if (ends_with(a, [4, 5])) { r = r + 1; } if (!ends_with(a, [3, 5])) { r = r + 2; } return r;`, 3},
	{"starts-string", `let ss: string[] = ["a", "b", "c", "d"]; let r: i32 = 0; if (starts_with(ss, ["a", "b"])) { r = r + 8; } if (!starts_with(ss, ["a", "c"])) { r = r + 4; } return r;`, 12},
	{"ends-string", `let ss: string[] = ["a", "b", "c", "d"]; let r: i32 = 0; if (ends_with(ss, ["c", "d"])) { r = r + 8; } if (!ends_with(ss, ["b", "d"])) { r = r + 1; } return r;`, 9},
}

func prefixIRSrc(mainBody string) string {
	return prefixIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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

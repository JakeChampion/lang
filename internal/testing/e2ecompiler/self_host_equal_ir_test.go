package e2ecompiler

import "testing"

const equalIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function equal[T: Eq](a: T[], b: T[]): boolean {
    if (a.len() != b.len()) { return false; }
    let i: i32 = 0;
    while (i < a.len()) {
        if (a[i] != b[i]) { return false; }
        i = i + 1;
    }
    return true;
}
pub function index_of_last[T: Eq](xs: T[], target: T): Option[i32] {
    let i: i32 = xs.len() - 1;
    while (i >= 0) {
        if (xs[i] == target) { return Some(i); }
        i = i - 1;
    }
    return None;
}
function uw(o: Option[i32], d: i32): i32 {
    match (o) {
        Some(v) => { return v; },
        None => { return d; },
    }
    return d;
}
`

var equalIRCases = []struct {
	name string
	main string
	want int
}{
	{"equal-i32", `let a: i32[] = [1, 2, 3]; let b: i32[] = [1, 2, 3]; let c: i32[] = [1, 2, 4]; let r: i32 = 0; if (equal(a, b)) { r = r + 1; } if (!equal(a, c)) { r = r + 2; } if (!equal(a, [1, 2])) { r = r + 4; } return r;`, 7},
	{"equal-string", `let ss: string[] = ["x", "y", "x"]; let r: i32 = 0; if (equal(ss, ["x", "y", "x"])) { r = r + 8; } if (!equal(ss, ["x", "y", "z"])) { r = r + 4; } return r;`, 12},
	{"last-i32", `let a: i32[] = [5, 1, 5, 2, 5]; let r: i32 = uw(index_of_last(a, 5), 0 - 1) * 10; if (uw(index_of_last(a, 9), 0 - 1) == 0 - 1) { r = r + 1; } return r;`, 41},
	{"last-string", `let ss: string[] = ["a", "b", "a", "c"]; return uw(index_of_last(ss, "a"), 0 - 1);`, 2},
}

func equalIRSrc(mainBody string) string {
	return equalIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostEqualIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range equalIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, equalIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

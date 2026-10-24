package e2ecompiler

import "testing"

const eqVerbsIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function contains[T: Eq](xs: T[], target: T): boolean {
    for x in xs {
        if (x == target) { return true; }
    }
    return false;
}
pub function index_of[T: Eq](xs: T[], target: T): Option[i32] {
    let i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i] == target) { return Some(i); }
        i = i + 1;
    }
    return None;
}
pub function distinct[T: Eq](xs: T[]): T[] {
    let out: T[] = [];
    for x in xs {
        if (!contains(out, x)) { out = out.append(x); }
    }
    return out;
}
function idx_or(o: Option[i32], d: i32): i32 {
    match (o) {
        Some(v) => { return v; },
        None => { return d; },
    }
    return d;
}
`

var eqVerbsIRCases = []struct {
	name string
	main string
	want int
}{
	{"contains-i32", `let a: i32[] = [10, 20, 30, 20]; let r: i32 = 0; if (contains(a, 20)) { r = r + 1; } if (!contains(a, 99)) { r = r + 2; } return r;`, 3},
	{"contains-string", `let ss: string[] = ["x", "y", "z"]; let r: i32 = 0; if (contains(ss, "z")) { r = r + 4; } if (!contains(ss, "q")) { r = r + 8; } return r;`, 12},
	{"index-of-hit", `let a: i32[] = [5, 10, 15, 20]; return idx_or(index_of(a, 15), 0 - 1);`, 2},
	{"index-of-miss", `let a: i32[] = [5, 10, 15, 20]; return idx_or(index_of(a, 99), 7);`, 7},
	{"distinct-i32", `let a: i32[] = [3, 1, 3, 2, 1, 3]; let d: i32[] = distinct(a); return d.len() * 30 + d[0] * 5 + d[1] * 3 + d[2];`, 110},
	{"distinct-string", `let ss: string[] = ["a", "b", "a", "c", "b"]; return distinct(ss).len();`, 3},
}

func eqVerbsIRSrc(mainBody string) string {
	return eqVerbsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostArrayEqVerbsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range eqVerbsIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, eqVerbsIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

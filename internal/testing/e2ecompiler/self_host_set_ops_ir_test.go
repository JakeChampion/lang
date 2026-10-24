package e2ecompiler

import "testing"

const setOpsIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function contains[T: Eq](xs: T[], target: T): boolean {
    for x in xs {
        if (x == target) { return true; }
    }
    return false;
}
pub function count[T: Eq](xs: T[], target: T): i32 {
    let c: i32 = 0;
    for x in xs {
        if (x == target) { c = c + 1; }
    }
    return c;
}
pub function union[T: Eq](a: T[], b: T[]): T[] {
    let out: T[] = [];
    for x in a {
        if (!contains(out, x)) { out = out.append(x); }
    }
    for y in b {
        if (!contains(out, y)) { out = out.append(y); }
    }
    return out;
}
pub function intersection[T: Eq](a: T[], b: T[]): T[] {
    let out: T[] = [];
    for x in a {
        if (contains(b, x) && !contains(out, x)) { out = out.append(x); }
    }
    return out;
}
pub function difference[T: Eq](a: T[], b: T[]): T[] {
    let out: T[] = [];
    for x in a {
        if (!contains(b, x) && !contains(out, x)) { out = out.append(x); }
    }
    return out;
}
`

var setOpsIRCases = []struct {
	name string
	main string
	want int
}{
	{"count-mixed", `let a: i32[] = [1, 2, 2, 3, 2]; let ss: string[] = ["x", "y", "x"]; return count(a, 2) * 10 + count(ss, "x");`, 32},
	{"union-i32", `let a: i32[] = [1, 2, 2, 3, 4]; let b: i32[] = [3, 4, 4, 5]; let u: i32[] = union(a, b); return u.len() * 10 + u[0] + u[4];`, 56},
	{"intersection-i32", `let a: i32[] = [4, 3, 2, 1]; let b: i32[] = [1, 3, 5]; let x: i32[] = intersection(a, b); return x.len() * 10 + x[0] * 2 + x[1];`, 27},
	{"difference-string", `let ss: string[] = ["a", "b", "c", "b"]; let tt: string[] = ["b"]; let d: string[] = difference(ss, tt); let r: i32 = d.len() * 10; if (d[0] == "a") { r = r + 1; } if (d[1] == "c") { r = r + 2; } return r;`, 23},
}

func setOpsIRSrc(mainBody string) string {
	return setOpsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostSetOpsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range setOpsIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, setOpsIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

package e2eselfhost

import "testing"

// setOpsIRCases exercise the Eq-driven set-algebra verbs (`count` /
// `union` / `intersection` / `difference`) through the self-host IR
// path on x86-64 + wasm. As with the eq-verbs cases, the surface is
// inlined from `internal/stdlib/std/array.fern` rather than imported:
// the `[T: Eq]` bodies (plus the `contains` they build on), an inline
// `Eq` trait + the i32 / string primitive impls, all stand-ins for
// core/cmp.
//
// The `Eq` bound is what makes the verbs clone per element type on the
// self-host path — an unbounded `[T]` would erase to one scalar-`==`
// clone that miscompares strings (pointer identity). This pins, in one
// program: per-type monomorphisation of the set ops, the nested
// bounded-generic calls (`union`/`intersection`/`difference` ->
// `contains`), and the string-vs-scalar `==` dispatch. Each case returns
// a small deterministic int (<= 125 so wasmtime accepts the process exit
// code), oracle-checked against the interpreter. FEATURE-AUDIT std/array
// row (#2689).
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
    var c: i32 = 0;
    for x in xs {
        if (x == target) { c = c + 1; }
    }
    return c;
}
pub function union[T: Eq](a: T[], b: T[]): T[] {
    var out: T[] = [];
    for x in a {
        if (!contains(out, x)) { out = out.append(x); }
    }
    for y in b {
        if (!contains(out, y)) { out = out.append(y); }
    }
    return out;
}
pub function intersection[T: Eq](a: T[], b: T[]): T[] {
    var out: T[] = [];
    for x in a {
        if (contains(b, x) && !contains(out, x)) { out = out.append(x); }
    }
    return out;
}
pub function difference[T: Eq](a: T[], b: T[]): T[] {
    var out: T[] = [];
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
	// count over i32[] (3 twos) and string[] (2 x's): 3*10 + 2 = 32.
	{"count-mixed", `var a: i32[] = [1, 2, 2, 3, 2]; var ss: string[] = ["x", "y", "x"]; return count(a, 2) * 10 + count(ss, "x");`, 32},
	// union dedups across both: {1,2,3,4,5}; len*10 + first + last.
	{"union-i32", `var a: i32[] = [1, 2, 2, 3, 4]; var b: i32[] = [3, 4, 4, 5]; var u: i32[] = union(a, b); return u.len() * 10 + u[0] + u[4];`, 56},
	// intersection in a-order: {3,1} -> [3,1]; len*10 + x0*2 + x1.
	{"intersection-i32", `var a: i32[] = [4, 3, 2, 1]; var b: i32[] = [1, 3, 5]; var x: i32[] = intersection(a, b); return x.len() * 10 + x[0] * 2 + x[1];`, 27},
	// difference a\b over strings: ["a","b","c","b"] \ ["b"] = {a,c}; len*10 + (a==first?).
	{"difference-string", `var ss: string[] = ["a", "b", "c", "b"]; var tt: string[] = ["b"]; var d: string[] = difference(ss, tt); var r: i32 = d.len() * 10; if (d[0] == "a") { r = r + 1; } if (d[1] == "c") { r = r + 2; } return r;`, 23},
}

func setOpsIRSrc(mainBody string) string {
	return setOpsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostSetOpsIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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

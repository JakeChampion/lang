package e2eselfhost

import "testing"

// eqVerbsIRCases exercise the Eq-driven generic array verbs
// (`contains` / `index_of` / `distinct`) through the self-host IR path
// on x86-64 + wasm. The relevant surface is inlined from
// `internal/stdlib/std/array.fern` rather than imported: the three
// `[T: Eq]` bodies, plus an inline `Eq` trait + the `i32` / `string`
// primitive impls (standing in for core/cmp — the same shape the derive-Eq
// IR cases use) and a `match`-based Option unwrap.
//
// The `Eq` BOUND is essential here, not decoration: the self-host
// monomorphiser only clones BOUNDED generics. An unbounded `[T]` erases
// to a single pointer-compare clone, so `contains(["x","y","z"], "z")`
// would compare string-box identities and miss the match. With `[T: Eq]`
// the verbs clone per element type, so a `string` instance lowers its
// `==` to `str_eq` (byte equality) while an `i32` instance keeps the
// scalar compare. This pins, in one program: per-type monomorphisation
// of a `T[]`-consuming body, a nested bounded-generic call
// (`distinct` -> `contains`), an `Option[i32]` return through
// `Some`/`None`, and the string-vs-scalar `==` dispatch inside a
// monomorphised body. Each case returns a small deterministic int
// (<= 255); expectations are oracle-checked against the native
// interpreter. FEATURE-AUDIT std/array row (#2689).
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
    var i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i] == target) { return Some(i); }
        i = i + 1;
    }
    return None;
}
pub function distinct[T: Eq](xs: T[]): T[] {
    var out: T[] = [];
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
	// contains over i32[]: present (20) and absent (99).
	{"contains-i32", `var a: i32[] = [10, 20, 30, 20]; var r: i32 = 0; if (contains(a, 20)) { r = r + 1; } if (!contains(a, 99)) { r = r + 2; } return r;`, 3},
	// contains over string[]: primitive string `==`.
	{"contains-string", `var ss: string[] = ["x", "y", "z"]; var r: i32 = 0; if (contains(ss, "z")) { r = r + 4; } if (!contains(ss, "q")) { r = r + 8; } return r;`, 12},
	// index_of hit: 15 is at index 2.
	{"index-of-hit", `var a: i32[] = [5, 10, 15, 20]; return idx_or(index_of(a, 15), 0 - 1);`, 2},
	// index_of miss: returns the None default.
	{"index-of-miss", `var a: i32[] = [5, 10, 15, 20]; return idx_or(index_of(a, 99), 7);`, 7},
	// distinct i32[]: [3,1,3,2,1,3] -> [3,1,2]; len*30 + d0*5 + d1*3 + d2 (kept
	// < 126: wasmtime rejects a process exit code >= 126).
	{"distinct-i32", `var a: i32[] = [3, 1, 3, 2, 1, 3]; var d: i32[] = distinct(a); return d.len() * 30 + d[0] * 5 + d[1] * 3 + d[2];`, 110},
	// distinct string[]: [a,b,a,c,b] -> [a,b,c]; len = 3.
	{"distinct-string", `var ss: string[] = ["a", "b", "a", "c", "b"]; return distinct(ss).len();`, 3},
}

func eqVerbsIRSrc(mainBody string) string {
	return eqVerbsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostArrayEqVerbsIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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

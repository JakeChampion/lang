package e2ecompiler

import "testing"

const u64IRPrelude = `function u64_min(a: u64, b: u64): u64 { if (a < b) { return a; } return b; }
function u64_max(a: u64, b: u64): u64 { if (a > b) { return a; } return b; }
function u64_clamp(n: u64, lo: u64, hi: u64): u64 { if (n < lo) { return lo; } if (n > hi) { return hi; } return n; }
function u64_id(x: u64): u64 { return x; }
struct UPair { n: u64 }
`

var u64IRCases = []struct {
	name string
	main string
}{
	{"min", `let a: u64 = 7 as u64; let b: u64 = 3 as u64; return u64_min(a, b) as i32;`},
	{"max", `let a: u64 = 7 as u64; let b: u64 = 3 as u64; return u64_max(a, b) as i32;`},
	{"clamp-lo", `return u64_clamp(5 as u64, 10 as u64, 40 as u64) as i32;`},
	{"clamp-mid", `return u64_clamp(25 as u64, 10 as u64, 40 as u64) as i32;`},
	{"clamp-hi", `return u64_clamp(50 as u64, 10 as u64, 40 as u64) as i32;`},
	{"umax-highbit", `let a: u64 = 18000000000000000007 as u64; let b: u64 = 9 as u64; return (u64_max(a, b) % (100 as u64)) as i32;`},
	{"uclamp-highbit-hi", `let hi: u64 = 18000000000000000000 as u64; return u64_clamp(50 as u64, 10 as u64, hi) as i32;`},
	{"concrete-u64-ret-shift", `let a: u64 = 18000000000000000000 as u64; return (u64_id(a) >> 57) as i32;`},
	{"u64-iife-shift", `let c: boolean = true; let a: u64 = 18000000000000000000 as u64; return ((if (c) { a } else { 0 as u64 }) >> 57) as i32;`},
	{"struct-u64-field-shift", `let p: UPair = UPair { n: 18000000000000000000 as u64 }; return (p.n >> 57) as i32;`},
	{"u64-literal-index-shift", `return ([18000000000000000000 as u64, 1 as u64][0] >> 57) as i32;`},
	{"u64-slice-index-shift", `let a: u64[] = [18000000000000000000 as u64, 1 as u64, 2 as u64]; return (a[0:2][0] >> 57) as i32;`},
}

func u64IRSrc(mainBody string) string {
	return u64IRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostU64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64IRCases {
		src := u64IRSrc(tc.main)
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}

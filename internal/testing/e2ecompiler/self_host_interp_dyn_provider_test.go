package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostInterpDynProvider(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	interpDriver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")

	const twoProviders = `trait A { function m(self: Self): i32; }
trait B { function m(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
impl B for S { function m(self: Self): i32 { return 7; } }
`

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"dyn-second-trait", twoProviders + `function main(): i32 { let d: dyn B = S { v: 3 }; return d.m(); }`},
		{"dyn-first-trait", twoProviders + `function main(): i32 { let d: dyn A = S { v: 3 }; return d.m(); }`},
		{"dyn-param", twoProviders + `function via(d: dyn B): i32 { return d.m(); } function main(): i32 { return via(S { v: 3 }); }`},
		{"dyn-array-for-loop", twoProviders + `function main(): i32 { let xs: dyn B[] = [S { v: 3 }, S { v: 4 }]; let t: i32 = 0; for x in xs { t = t + x.m(); } return t; }`},
		{"dyn-array-index", twoProviders + `function main(): i32 { let xs: dyn B[] = [S { v: 3 }]; return xs[0].m(); }`},
		{"dyn-captured-by-closure", twoProviders + `function main(): i32 { let d: dyn B = S { v: 3 }; let f: () => i32 = (() => d.m()); return f(); }`},
		{"dyn-struct-field", twoProviders + `struct Holder { d: dyn B }
function main(): i32 { let h: Holder = Holder { d: S { v: 3 } }; return h.d.m(); }`},
		{"dyn-nested-struct-field", twoProviders + `struct Holder { d: dyn B }
struct Outer { h: Holder }
function main(): i32 { let o: Outer = Outer { h: Holder { d: S { v: 3 } } }; return o.h.d.m(); }`},
		{"dyn-struct-field-array", twoProviders + `struct Holder { ds: dyn B[] }
function main(): i32 { let h: Holder = Holder { ds: [S { v: 3 }] }; return h.ds[0].m(); }`},
		{"dyn-call-result", twoProviders + `function mk(): dyn B { return S { v: 3 }; }
function main(): i32 { return mk().m(); }`},
		{"dyn-method-call-result", twoProviders + `struct Mk { z: i32 }
function (k: Mk) make(): dyn B { return S { v: 3 }; }
function main(): i32 { let k: Mk = Mk { z: 0 }; return k.make().m(); }`},
		{"dyn-field-of-call-result", twoProviders + `struct Holder { d: dyn B }
function mk(): Holder { return Holder { d: S { v: 3 } }; }
function main(): i32 { return mk().d.m(); }`},
		{"dyn-call-result-binding", twoProviders + `function mk(): dyn B { return S { v: 3 }; }
function main(): i32 { let d = mk(); return d.m(); }`},

		{"only-trait-a", `trait A { function m(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
function main(): i32 { let d: dyn A = S { v: 3 }; return d.m(); }`},
		{"only-trait-b", `trait B { function m(self: Self): i32; }
struct S { v: i32 }
impl B for S { function m(self: Self): i32 { return 7; } }
function main(): i32 { let d: dyn B = S { v: 3 }; return d.m(); }`},

		{"concrete-receiver-alongside-collision", twoProviders + `struct T { w: i32 }
impl B for T { function m(self: Self): i32 { return self.w + 1; } }
function main(): i32 { let d: dyn B = T { w: 40 }; let t: T = T { w: 5 }; return d.m() + t.m(); }`},
		{"inherent-method", `struct P { v: i32 } function (p: P) get(x: i32): i32 { return p.v + x; } function main(): i32 { let p: P = P { v: 10 }; return p.get(5); }`},
		{"concrete-single-trait", `trait A { function m(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
function main(): i32 { let s: S = S { v: 3 }; return s.m(); }`},

		{"i64-width-binding", `function main(): i32 { let x: i64 = 100000; let y: i64 = x * 100000; if (y > 4294967296) { return 42; } return 1; }`},
		{"f32-precision-binding", `function main(): i32 { let f: f32 = 16777217.0; let g: f64 = 16777216.0; if (f as f64 == g) { return 42; } return 1; }`},
		{"param-width-binding", `function wide(x: i64): i32 { let y: i64 = x * 100000; if (y > 4294967296) { return 42; } return 1; } function main(): i32 { return wide(100000); }`},
		{"inferred-i64-from-call", `function big(): i64 { return 100000; }
function main(): i32 { let x = big(); let y: i64 = x * 100000; if (y > 4294967296) { return 42; } return 1; }`},
		{"inferred-f32-from-call", `function narrow(): f32 { return 16777217.0; }
function main(): i32 { let f = narrow(); let g: f64 = 16777216.0; if (f as f64 == g) { return 42; } return 1; }`},
		{"call-shadowed-by-local", `function pick(): i64 { return 1; }
function main(): i32 { let pick: () => i32 = (() => 100000); let x = pick(); let y = x * 100000; if (y == 1410065408) { return 1; } return 42; }`},
		{"closure-in-field-call", `struct H { f: () => i32 }
function main(): i32 { let h: H = H { f: (() => 42) }; let x = h.f(); return x; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			progPath := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(progPath, src, 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}

			want := interpExit(t, interpBin, tc.src)

			got := runDriverExit(t, runner, interpDriver, src)
			if got != want {
				t.Errorf("%s: self-host interp exited %d, want %d (native oracle) — a `dyn` "+
					"receiver must call the provider its own trait names, and a concrete "+
					"receiver must keep dispatching on its runtime type alone",
					tc.name, got, want)
			}
		})
	}
}

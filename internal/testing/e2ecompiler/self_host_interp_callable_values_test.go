package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostInterpCallableValues(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	interpDriver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"field-closure-on-local", `struct H { f: (i32) => i32, n: i32 } function main(): i32 { let h: H = H { f: ((x: i32) => x * 3), n: 5 }; return h.f(7) + h.n; }`},
		{"field-closure-through-array", `struct C { f: (i32) => i32, n: i32 } function main(): i32 { let cs: C[] = []; let i: i32 = 0; while (i < 2) { cs = cs.append(C { f: ((x: i32) => x + i), n: i }); i = i + 1; } return (cs[0].f)(0); }`},
		{"field-closure-captures", `struct H { f: () => i32 } function main(): i32 { let k: i32 = 11; let h: H = H { f: (() => k + 1) }; return h.f(); }`},

		{"method-wins-over-field", `struct S { go: (i32) => i32 } function (s: S) go(x: i32): i32 { return x + 100; } function main(): i32 { let s: S = S { go: ((x: i32) => x + 1) }; return s.go(5); }`},
		{"ordinary-method", `struct S { v: i32 } function (s: S) go(x: i32): i32 { return s.v + x; } function main(): i32 { let s: S = S { v: 10 }; return s.go(5); }`},
		{"tuple-element-closure", `function main(): i32 { let t: ((i32) => i32, i32) = (((x: i32) => x + 4), 2); return (t.0)(5); }`},
		{"fn-pointer-field", `function inc(x: i32): i32 { return x + 1; } function dbl(x: i32): i32 { return x * 2; } struct T { hs: ((i32) => i32)[] } function main(): i32 { let t: T = T { hs: [inc, dbl] }; return (t.hs[0])(10) + (t.hs[1])(10); }`},
		{"fn-name-as-argument", `function inc(x: i32): i32 { return x + 1; } function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(inc, 10); }`},
		{"method-returned-closure", `struct M { v: i32 } function (m: M) mk(): (i32) => i32 { return ((x: i32) => x + m.v); } function main(): i32 { let m: M = M { v: 7 }; return (m.mk())(3); }`},

		{"local-shadows-fn-name", `function inc(x: i32): i32 { return x + 1; } function main(): i32 { let inc: i32 = 99; return inc; }`},
		{"plain-tuple-read", `function main(): i32 { let t: (i32, i32) = (3, 4); return t.0 + t.1; }`},
		{"const-ref", `const LIMIT: i32 = 100; function main(): i32 { return LIMIT + 1; }`},
		{"const-two", `const A: i32 = 7; const B: i32 = 5; function main(): i32 { return A + B; }`},
		{"const-in-callee", `const K: i32 = 3; function add(x: i32): i32 { return x + K; } function main(): i32 { return add(4); }`},
		{"const-receiver", `const S: string = "abcd"; function main(): i32 { return S.len(); }`},
		{"const-as-argument", `const K: i32 = 3; function main(): i32 { let x: i32 = 4; return x + K; }`},

		{"enum-variant-nullary", `enum E { Red, Blue } function main(): i32 { let e: E = E.Red; return match (e) { Red => 1i32, Blue => 2i32 }; }`},
		{"enum-variant-payload", `enum Opt { Some(i32), Nothing } function main(): i32 { let o: Opt = Opt.Some(9); return match (o) { Some(v) => v, Nothing => 0i32 }; }`},
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
				t.Errorf("%s: self-host interp exited %d, want %d (native oracle) — a callable "+
					"reached through a field, a tuple element, or its own name must be "+
					"callable, and a real method must still win",
					tc.name, got, want)
			}
		})
	}
}

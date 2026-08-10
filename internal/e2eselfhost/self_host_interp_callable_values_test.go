package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostInterpCallableValues pins the ways a callable can be reached on
// the self-host interpreter other than by naming a method: out of a struct
// FIELD (#6596), out of a TUPLE element, and by naming a top-level function as
// a VALUE (both #6611).
//
// All three presented as the same symptom — an undefined-method or
// undefined-identifier error where native returns a number — and all three were
// divergences INSIDE the self-host rather than against native. Its compiled
// path handles every one of these (the struct-field case is what #6461 is
// about, a struct carrying a handler built into a dispatch table), so `-interp`
// could not run programs the same compiler compiles correctly. That matters
// because `fern -interp` is the oracle much of the corpus tooling leans on.
//
// The causes are distinct, which is why one fix did not cover them:
//
//   - struct field — `h.f(3)` reached the method walk, found no method `f` on
//     `H`, and gave up. Fixed by a FALLBACK after that walk.
//   - tuple element — `(t.0)(5)` arrives as method dispatch too, but a tuple
//     has no type name to dispatch ON, so it died deriving the receiver type
//     before the walk ever ran.
//   - function as a value — nothing to do with dispatch. Functions live in the
//     env's `funcs`, not its name/value arrays, so `inc` in `[inc, dbl]` read
//     as an undefined identifier.
//
// Each fix is a fallback on a path that previously errored, so none can change
// an answer that was already right — and the three controls are what hold that.
// `method-wins-over-field` pins the dispatch ORDER (a struct carrying both a
// method `go` and a field `go` must still take the method);
// `local-shadows-fn-name` pins that the funcs scan runs only on a lookup MISS;
// `plain-tuple-read` pins that ordinary element access is undisturbed.
//
// Every case is oracled against the native interpreter, so these assert the
// language's behaviour rather than values chosen to match the implementation.
func TestSelfHostInterpCallableValues(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")
	interpDriver := buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run")

	for _, tc := range []struct {
		name string
		src  string
	}{
		// The issue's repro: a field closure called straight off a local.
		{"field-closure-on-local", `struct H { f: (i32) => i32, n: i32 } function main(): i32 { var h: H = H { f: ((x: i32) => x * 3), n: 5 }; return h.f(7) + h.n; }`},
		// Through an array element — the provider-table shape from #6461, and
		// the one that kept the fourth capture case out of the interpreter
		// suite.
		{"field-closure-through-array", `struct C { f: (i32) => i32, n: i32 } function main(): i32 { var cs: C[] = []; var i: i32 = 0; while (i < 2) { cs = cs.append(C { f: ((x: i32) => x + i), n: i }); i = i + 1; } return (cs[0].f)(0); }`},
		// A capturing field closure: the captured value has to survive being
		// stored in the field and fetched back out.
		{"field-closure-captures", `struct H { f: () => i32 } function main(): i32 { var k: i32 = 11; var h: H = H { f: (() => k + 1) }; return h.f(); }`},

		// CONTROLS.
		//
		// A real method must still win. This is the one that would break if the
		// field lookup ran BEFORE the method walk instead of after it.
		{"method-wins-over-field", `struct S { go: (i32) => i32 } function (s: S) go(x: i32): i32 { return x + 100; } function main(): i32 { var s: S = S { go: ((x: i32) => x + 1) }; return s.go(5); }`},
		// An ordinary method on a struct with no fn fields at all — the plain
		// dispatch path, untouched.
		{"ordinary-method", `struct S { v: i32 } function (s: S) go(x: i32): i32 { return s.v + x; } function main(): i32 { var s: S = S { v: 10 }; return s.go(5); }`},
		// The two shapes #6611's sibling sweep turned up, which the struct-field
		// fallback above does NOT reach — same symptom, different causes.
		//
		// A fn-valued TUPLE element. `(t.0)(5)` also arrives as method
		// dispatch, but a tuple has no type name to dispatch ON, so it died
		// deriving the receiver type rather than in the FuncDecl walk.
		{"tuple-element-closure", `function main(): i32 { var t: ((i32) => i32, i32) = (((x: i32) => x + 4), 2); return (t.0)(5); }`},
		// A top-level function used as a VALUE. Nothing to do with dispatch:
		// functions live in the env's `funcs`, not its name/value arrays, so
		// `inc` read as an undefined identifier the moment it appeared outside
		// call position.
		{"fn-pointer-field", `function inc(x: i32): i32 { return x + 1; } function dbl(x: i32): i32 { return x * 2; } struct T { hs: ((i32) => i32)[] } function main(): i32 { var t: T = T { hs: [inc, dbl] }; return (t.hs[0])(10) + (t.hs[1])(10); }`},
		{"fn-name-as-argument", `function inc(x: i32): i32 { return x + 1; } function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(inc, 10); }`},
		// A closure returned by a method and called immediately — the one
		// sibling that already worked, kept so it stays working.
		{"method-returned-closure", `struct M { v: i32 } function (m: M) mk(): (i32) => i32 { return ((x: i32) => x + m.v); } function main(): i32 { var m: M = M { v: 7 }; return (m.mk())(3); }`},

		// CONTROLS for those two.
		//
		// A local must still shadow a function of the same name: the funcs
		// scan runs only on a MISS, so `var inc: i32 = 99` wins.
		{"local-shadows-fn-name", `function inc(x: i32): i32 { return x + 1; } function main(): i32 { var inc: i32 = 99; return inc; }`},
		// A plain tuple read with no closure in sight — the tuple arm must not
		// disturb ordinary element access.
		{"plain-tuple-read", `function main(): i32 { var t: (i32, i32) = (3, 4); return t.0 + t.1; }`},
		// CONSTS, which is where naming-a-function-as-a-value collides. The
		// parser desugars `const N = expr` into a ZERO-ARG FUNCTION, and a bare
		// `N` is evaluated by calling it — a path reached only because the env
		// lookup MISSED. Resolving function names to values one level lower, in
		// the lookup itself, made that lookup succeed with a VFunc and every
		// const reference silently became a function value. These are the
		// shapes that caught it.
		{"const-ref", `const LIMIT: i32 = 100; function main(): i32 { return LIMIT + 1; }`},
		{"const-two", `const A: i32 = 7; const B: i32 = 5; function main(): i32 { return A + B; }`},
		{"const-in-callee", `const K: i32 = 3; function add(x: i32): i32 { return x + K; } function main(): i32 { return add(4); }`},

		// NOT covered here: calling a NON-fn field (`s.v(1)` for `v: i32`). The
		// fallback is gated on the field holding a VFunc, so that shape still
		// errors — but the two engines report it through different channels
		// (native rejects it at CHECK time, exit 1; this interpreter reaches it
		// at runtime and exits 254), and an exit-code oracle cannot compare
		// those. The gating is asserted by `method-wins-over-field` instead,
		// which is the case that actually discriminates.
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			progPath := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(progPath, src, 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}

			// Oracle: the native interpreter defines the behaviour.
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

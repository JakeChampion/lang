package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostInterpDynProvider pins which definition the self-host AST
// interpreter calls when two traits provide one method name for one type and
// the receiver is reached through `dyn`.
//
// The symbol namespace the parser hands every engine is injective by renaming:
// the first trait to claim `<Type>.<m>` keeps the bare `m` and a later distinct
// trait's provider is interposed as `<Trait>.<m>` (`parser.claim_method_name`).
// A dispatcher keyed on the runtime type and the written method name therefore
// always reaches the FIRST claimant, whatever trait the `dyn` names — which is a
// wrong answer rather than a failure: `var d: dyn B = S { v: 3 }; d.m()` ran A's
// method and returned 3 where every other engine returns 7.
//
// The interpreter now reads the receiver's DECLARED type out of the scope it is
// bound in and resolves the written name through `parser.dyn_provider_name`, the
// same reading of the claim table the compiled backends make through
// `irlower.dyn_arm_matches`. So the cases split three ways:
//
//   - both traits implemented, reached through `dyn` — the shapes a declaration
//     is in reach of: a local binding, a parameter, an array element (indexed
//     and iterated), and a binding captured by a closure.
//   - each trait ALONE — controls on the resolution, since with one provider
//     nothing is interposed and the bare name must still answer. `only-trait-b`
//     is the one that discriminates a resolver that reaches for `B.m`
//     unconditionally: B is the sole claimant there, so its symbol IS `m`.
//   - the non-`dyn` path — a concrete receiver must dispatch on its runtime
//     type alone, undisturbed, including for a type that shares the colliding
//     method name with a type that does have two providers.
//
// Every case is oracled against the native interpreter, so these assert the
// language's behaviour rather than values chosen to match the implementation.
//
// NOT covered, because an exit-code oracle cannot compare them: a concrete
// `s.m()` on a type with two providers, which native REJECTS at check time
// (E074, exit 1) while the self-host checker does not yet enforce ambiguity and
// this engine runs the program. That gap is the self-host checker's, not the
// interpreter's.
func TestSelfHostInterpDynProvider(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")
	interpDriver := buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run")

	// Both traits provide `m` for S: A claims the bare name, B is interposed.
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
		// BOTH TRAITS IMPLEMENTED, reached through `dyn`.
		//
		// The issue's repro: the interposed provider, through a local binding.
		{"dyn-second-trait", twoProviders + `function main(): i32 { var d: dyn B = S { v: 3 }; return d.m(); }`},
		// The bare-name provider through the same shape — the half that was
		// already right, and which a fix that always interposes would break.
		{"dyn-first-trait", twoProviders + `function main(): i32 { var d: dyn A = S { v: 3 }; return d.m(); }`},
		// A `dyn` PARAMETER, whose declared type is on the FuncDecl rather than
		// on a var statement.
		{"dyn-param", twoProviders + `function via(d: dyn B): i32 { return d.m(); } function main(): i32 { return via(S { v: 3 }); }`},
		// The heterogeneous-collection shape: the loop var's declared type is
		// the element type of the iterable's annotation.
		{"dyn-array-for-loop", twoProviders + `function main(): i32 { var xs: dyn B[] = [S { v: 3 }, S { v: 4 }]; var t: i32 = 0; for x in xs { t = t + x.m(); } return t; }`},
		// The same array, indexed rather than iterated.
		{"dyn-array-index", twoProviders + `function main(): i32 { var xs: dyn B[] = [S { v: 3 }]; return xs[0].m(); }`},
		// Captured by a closure: the lambda snapshots the enclosing scope, so
		// the declared type has to travel with the captured value.
		{"dyn-captured-by-closure", twoProviders + `function main(): i32 { var d: dyn B = S { v: 3 }; var f: () => i32 = (() => d.m()); return f(); }`},

		// CONTROLS — each trait alone. Nothing is interposed, so the bare name
		// must answer for whichever trait is the sole provider.
		{"only-trait-a", `trait A { function m(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
function main(): i32 { var d: dyn A = S { v: 3 }; return d.m(); }`},
		{"only-trait-b", `trait B { function m(self: Self): i32; }
struct S { v: i32 }
impl B for S { function m(self: Self): i32 { return 7; } }
function main(): i32 { var d: dyn B = S { v: 3 }; return d.m(); }`},

		// THE NON-`dyn` PATH, which must not move.
		//
		// T provides `m` for B only, so T's own symbol is the bare name even
		// though S's `B.m` was interposed: a concrete `t.m()` and a `dyn B`
		// receiver holding a T must both reach it.
		{"concrete-receiver-alongside-collision", twoProviders + `struct T { w: i32 }
impl B for T { function m(self: Self): i32 { return self.w + 1; } }
function main(): i32 { var d: dyn B = T { w: 40 }; var t: T = T { w: 5 }; return d.m() + t.m(); }`},
		// A plain inherent method on a struct with no traits in sight.
		{"inherent-method", `struct P { v: i32 } function (p: P) get(x: i32): i32 { return p.v + x; } function main(): i32 { var p: P = P { v: 10 }; return p.get(5); }`},
		// A single-trait method called on its CONCRETE receiver — static
		// dispatch, no trait object involved.
		{"concrete-single-trait", `trait A { function m(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
function main(): i32 { var s: S = S { v: 3 }; return s.m(); }`},

		// CONTROLS on the binding path itself. A declared type now enters the
		// scope alongside the value, and the width / precision coercion it has
		// always driven rides the same call.
		{"i64-width-binding", `function main(): i32 { var x: i64 = 100000; var y: i64 = x * 100000; if (y > 4294967296) { return 42; } return 1; }`},
		{"f32-precision-binding", `function main(): i32 { var f: f32 = 16777217.0; var g: f64 = 16777216.0; if (f as f64 == g) { return 42; } return 1; }`},
		{"param-width-binding", `function wide(x: i64): i32 { var y: i64 = x * 100000; if (y > 4294967296) { return 42; } return 1; } function main(): i32 { return wide(100000); }`},
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
				t.Errorf("%s: self-host interp exited %d, want %d (native oracle) — a `dyn` "+
					"receiver must call the provider its own trait names, and a concrete "+
					"receiver must keep dispatching on its runtime type alone",
					tc.name, got, want)
			}
		})
	}
}

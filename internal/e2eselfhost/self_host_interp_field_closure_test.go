package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostInterpFieldClosure pins calling a closure held in a struct FIELD
// on the self-host interpreter (#6596): `h.f(3)` where `f: (i32) => i32` is a
// field rather than a method.
//
// The engine read every `recv.name(...)` as method dispatch, so it walked the
// user FuncDecls, found no method `f` on `H`, and gave up with an
// undefined-method error. The field itself resolved fine — a plain `h.f` read
// was never the problem, only the call position.
//
// This was a divergence INSIDE the self-host rather than against native: its
// compiled path has always handled the shape (it is what #6461 is about — a
// struct carrying a handler, built into a dispatch table), so `-interp` could
// not run a program the same compiler compiled correctly.
//
// The fix is a FALLBACK after the method walk, not a check before it, which is
// what makes `method-wins-over-field` below a real assertion rather than
// decoration: a struct with both a method `go` and a field `go` must keep
// dispatching to the method, so the change can only ever turn an error into an
// answer.
//
// Every case is oracled against the native interpreter, so these assert the
// language's behaviour rather than values chosen to match the implementation.
func TestSelfHostInterpFieldClosure(t *testing.T) {
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
				t.Errorf("%s: self-host interp exited %d, want %d (native oracle) — a closure "+
					"held in a struct field must be callable, and a real method must still win",
					tc.name, got, want)
			}
		})
	}
}

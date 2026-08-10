package e2e

import "testing"

// TestStructUpdateParamSpreadReuse guards a miscompile in the shared IR
// lowering (internal/ir): a self-overwrite struct-update spread of a value
// flowing through a function parameter — `p = T { ...p, field: v }` inside a
// function the caller threads a struct through, so p's rc is > 1 at the
// overwrite.
//
// The FBIP self-overwrite reuse fast path (tryStructReuseOverwrite) only
// placed the *explicitly listed* fields (sl.Fields). For the spread form the
// un-overridden fields live in sl.Base, so on the fresh-alloc (rc>1) branch
// the new box's un-listed fields were never copied and read back as 0 — a
// nondeterministic miscompile (correct when p happened to be unique → the
// reuse branch keeps the field; wrong when aliased → fresh-alloc). All three
// compiled backends (x86-64, arm64, wasm) share this lowering and were
// affected; the AST interpreter (separate path) was not. Fixed by deferring
// the spread form to the general StructLit lowering.
//
// Programs are written to return a value combining BOTH an un-overridden
// (carried) field and the overridden one, so a dropped carried field changes
// the result.
func TestStructUpdateParamSpreadReuse(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// `a` is never overridden — it must survive the spread through inc().
		// a stays 5, n increments per call → 3*10 + 5 = 35.
		{"carry", `struct T { a: i32, n: i32 }
function inc(p: T): T { p = T { ...p, n: p.n + 1 }; return p; }
function main(): i32 { var x: T = T { a: 5, n: 0 }; x = inc(x); x = inc(x); x = inc(x); return x.n * 10 + x.a; }`, 35},
		// Two sequential self-overwrite spreads of the parameter; the second
		// reads a field the first did not touch. After two calls a=20, n=2.
		{"two-spreads", `struct T { a: i32, n: i32 }
function emit(p: T): T { p = T { ...p, a: p.a + 10 }; p = T { ...p, n: p.n + 1 }; return p; }
function main(): i32 { var x: T = T { a: 0, n: 0 }; x = emit(x); x = emit(x); return x.a + x.n; }`, 22},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Run("x86-64-linux", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86-64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != c.want {
					t.Errorf("arm64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				if got := runWasm(t, c.src); got != c.want {
					t.Errorf("wasm main() = %d, want %d", got, c.want)
				}
			})
		})
	}
}

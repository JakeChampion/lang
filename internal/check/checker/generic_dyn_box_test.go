package checker

import (
	"strings"
	"testing"
)

func TestGenericErrorBoxingUsesBounds(t *testing.T) {
	const traits = `trait A { function a(self: Self): i32; }
trait B: A { function b(self: Self): i32; }
trait C { function c(self: Self): i32; }
trait Kind[T] { function tag(self: Self): T; }
`
	for _, tc := range []struct {
		name, bound, target string
		ok                  bool
	}{
		{"single", ": A", "A", true},
		{"multiple", ": A + C", "A + C", true},
		{"supertrait", ": B", "A", true},
		{"missing-trait", ": A", "A + C", false},
		{"unbounded", "", "A", false},
		{"generic-trait", ": Kind[i32]", "Kind[i32]", true},
		{"wrong-trait-argument", ": Kind[i32]", "Kind[string]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := traits + "function f[E" + tc.bound + "](r: Result[i32, E]): Result[i32, dyn " + tc.target + "] { let n: i32 = r?; return Ok(n); }"
			err := checkSource(t, src)
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "cannot box")) {
				t.Fatalf("want a boxing error, got %v", err)
			}
			if tc.name == "missing-trait" && !strings.Contains(err.Error(), "does not implement C") {
				t.Errorf("want the missing C bound, got %v", err)
			}
		})
	}
	for _, tc := range []struct{ name, src string }{
		{"implicit-return", "function f[E: A](e: E): dyn A { return e; }"},
		{"captured-bound", "function f[E: A](e: E): dyn A { let g: () => dyn A = (): dyn A => e; return g(); }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, traits+tc.src); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("parameter-shadows-concrete-implementation", func(t *testing.T) {
		src := traits + `struct E { n: i32 }
impl A for E { function a(self: Self): i32 { return self.n; } }
function f[E](r: Result[i32, E]): Result[i32, dyn A] { let n: i32 = r?; return Ok(n); }`
		if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "cannot box") {
			t.Fatalf("unbounded E must not use the concrete E's implementation: %v", err)
		}
	})
}

// Regression tests for #4787: dyn Trait[] element views vs Perceus drops on
// the native backends. A dyn cell carries no rc header, so an element read
// (`var x = xs[0]`, the for-in loop var) is an UNCOUNTED borrow of the
// array's cell. Two drop paths violated that:
//   - the precise (dead-local) drop freed the array — cells included — at
//     its last syntactic use, while an element view was still live
//     (segfault / garbage dispatch on both natives);
//   - the exit sweep dropped the borrowed view itself unconditionally,
//     double-freeing against the owning array's own drop walk.
//
// A bare pre-coerced dyn LOCAL as a literal element (`[d]`) is the same
// hazard from the other side: the cell MOVES into the array uncounted, so
// the source local must not also be swept.
// Fixed by excluding dyn arrays from precise drops and borrowed dyn views /
// moved-in sources from the sweep (rc_analysis.go computeDynBorrowedViews).
// Exit codes are the oracle; every case's expected value is the interpreter's.
package e2e

import "testing"

var dynArrViewCases = []struct {
	name     string
	src      string
	expected int
}{
	// The #4787 headline: an enum LOCAL as a dyn-array literal element,
	// iterated via `for x in xs` (whose desugar binds each element into a
	// loop-var view). Add(4).show() = 5.
	{"enum-local-elem-for",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function sum(xs: dyn Show[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { var e: Op = Add(4); var xs: dyn Show[] = [e]; return sum(xs); }`, 5},
	// Heterogeneous struct + enum locals in one literal. 3*3 + (4+1) = 14.
	{"mixed-struct-enum-elem-for",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } struct Circle { r: i32 } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function sum(xs: dyn Show[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { var e: Op = Add(4); var xs: dyn Show[] = [Circle { r: 3 }, e]; return sum(xs); }`, 14},
	// A struct local element with the for-loop in the SAME function as the
	// literal — the precise drop of xs fired between the binding and the
	// dispatch. 3*3 = 9.
	{"struct-local-elem-for-same-fn",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { var c: Circle = Circle { r: 3 }; var xs: dyn Show[] = [c]; var t: i32 = 0; for x in xs { t = t + x.show(); } return t; }`, 9},
	// An element bound into a named local, then dispatched — the minimal
	// view shape (segfaulted pre-fix). 9.
	{"elem-into-local",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { var c: Circle = Circle { r: 3 }; var xs: dyn Show[] = [c]; var x = xs[0]; return x.show(); }`, 9},
	// Same but with a CONSTRUCTION element — returned 0 (wrong value, not a
	// crash) pre-fix: the precise drop freed the fresh cell before dispatch. 9.
	{"construction-elem-into-local",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { var xs: dyn Show[] = [Circle { r: 3 }]; var x = xs[0]; return x.show(); }`, 9},
	// A PRE-COERCED dyn local as the literal element: the cell moves into
	// the array uncounted, so sweeping the source too double-freed at exit. 9.
	{"precoerced-dyn-local-elem",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { var c: Circle = Circle { r: 3 }; var d: dyn Show = c; var xs: dyn Show[] = [d]; var x = xs[0]; return x.show(); }`, 9},
	// Direct-index dispatch (no view binding) — worked pre-fix; pinned so
	// the exclusions never regress it. 5.
	{"direct-index-regression",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function main(): i32 { var e: Op = Add(4); var xs: dyn Show[] = [e]; return xs[0].show(); }`, 5},
}

func TestX86_64DynArrElementViews(t *testing.T) {
	for _, tc := range dynArrViewCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86Native(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestArm64DynArrElementViews(t *testing.T) {
	for _, tc := range dynArrViewCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, tc.src); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

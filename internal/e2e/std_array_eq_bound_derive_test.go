package e2e

import "testing"

// A `@derive(Eq)` element type satisfies the `T: cmp.Eq` bound the Eq-driven
// std/array and std/set verbs are written against, so every one of them must
// accept it. They compare through the bound's `eq` method for that reason: `==`
// desugars through a `Type.eq` lookup that is not visible from the defining
// module, so a body using the operator rejected the caller's own type with
// "cannot compare values of type Point" — reported as a monomorph "compiler
// bug" at a line inside the stdlib (#6846). `i32` / `string` elements resolve
// the operator fine, which is why the whole family looked healthy.
//
// One `derive(Eq)` program per verb; `i32` / `string` coverage for the same
// verbs lives in their own suites.
const eqBoundPointPreamble = `import "std/array" as array;
import "core/cmp" as cmp;
@derive(cmp.Eq)
struct Point { x: i32, y: i32 }
`

var eqBoundDeriveCases = []struct {
	name string
	body string
}{
	{
		// Each `return` is a distinct failure code so a red run names the
		// assertion; 42 is the all-pass value.
		name: "all_equal",
		body: `    var same: Point[] = [Point { x: 1, y: 2 }, Point { x: 1, y: 2 }];
    if (!same.all_equal()) { return 1; }
    if (!array.all_equal(same)) { return 2; }
    var mixed: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }];
    if (mixed.all_equal()) { return 3; }
    var one: Point[] = [Point { x: 1, y: 2 }];
    if (!one.all_equal()) { return 4; }
    var none: Point[] = [];
    if (!none.all_equal()) { return 5; }`,
	},
	{
		name: "dedup",
		body: `    var runs: Point[] = [Point { x: 1, y: 2 }, Point { x: 1, y: 2 }, Point { x: 3, y: 4 }, Point { x: 1, y: 2 }];
    var d: Point[] = runs.dedup();
    if (d.len() != 3) { return 1; }
    if (!d[0].eq(Point { x: 1, y: 2 })) { return 2; }
    if (!d[1].eq(Point { x: 3, y: 4 })) { return 3; }
    if (!d[2].eq(Point { x: 1, y: 2 })) { return 4; }
    var none: Point[] = [];
    if (array.dedup(none).len() != 0) { return 5; }`,
	},
	{
		// Both spellings: core/cmp's free function and std/array's `.distinct()`
		// receiver method, which delegates to it.
		name: "distinct",
		body: `    var runs: Point[] = [Point { x: 3, y: 4 }, Point { x: 1, y: 2 }, Point { x: 3, y: 4 }];
    var d: Point[] = cmp.distinct(runs);
    if (d.len() != 2) { return 1; }
    if (!d[0].eq(Point { x: 3, y: 4 })) { return 2; }
    if (!d[1].eq(Point { x: 1, y: 2 })) { return 3; }
    var m: Point[] = runs.distinct();
    if (m.len() != 2) { return 4; }
    if (!m[0].eq(Point { x: 3, y: 4 })) { return 5; }
    if (!m[1].eq(Point { x: 1, y: 2 })) { return 6; }
    var none: Point[] = [];
    if (none.distinct().len() != 0) { return 7; }`,
	},
	{
		name: "count",
		body: `    var runs: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }, Point { x: 1, y: 2 }];
    if (array.count(runs, Point { x: 1, y: 2 }) != 2) { return 1; }
    if (array.count(runs, Point { x: 3, y: 4 }) != 1) { return 2; }
    if (array.count(runs, Point { x: 9, y: 9 }) != 0) { return 3; }`,
	},
	{
		name: "index_of_last",
		body: `    var runs: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }, Point { x: 1, y: 2 }];
    match (runs.index_of_last(Point { x: 1, y: 2 })) {
        Some(i) => { if (i != 2) { return 1; } },
        None => { return 2; }
    }
    match (runs.index_of_last(Point { x: 9, y: 9 })) {
        Some(_) => { return 3; },
        None => {}
    }`,
	},
	{
		name: "starts_with",
		body: `    var xs: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }, Point { x: 5, y: 6 }];
    if (!xs.starts_with([Point { x: 1, y: 2 }, Point { x: 3, y: 4 }])) { return 1; }
    if (xs.starts_with([Point { x: 3, y: 4 }])) { return 2; }
    var none: Point[] = [];
    if (!xs.starts_with(none)) { return 3; }
    if (xs.starts_with([Point { x: 1, y: 2 }, Point { x: 3, y: 4 }, Point { x: 5, y: 6 }, Point { x: 7, y: 8 }])) { return 4; }`,
	},
	{
		name: "ends_with",
		body: `    var xs: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }, Point { x: 5, y: 6 }];
    if (!xs.ends_with([Point { x: 3, y: 4 }, Point { x: 5, y: 6 }])) { return 1; }
    if (xs.ends_with([Point { x: 3, y: 4 }])) { return 2; }
    var none: Point[] = [];
    if (!xs.ends_with(none)) { return 3; }
    if (xs.ends_with([Point { x: 9, y: 9 }, Point { x: 5, y: 6 }])) { return 4; }`,
	},
	{
		// The set-algebra verbs route their membership tests through
		// cmp.contains rather than comparing elements themselves, so they were
		// already generic — pinned here because the issue's fix list names
		// them as the verbs to check.
		name: "set algebra",
		body: `    var a: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }];
    var b: Point[] = [Point { x: 3, y: 4 }, Point { x: 5, y: 6 }];
    if (array.union(a, b).len() != 3) { return 1; }
    var i: Point[] = array.intersection(a, b);
    if (i.len() != 1) { return 2; }
    if (!i[0].eq(Point { x: 3, y: 4 })) { return 3; }
    var d: Point[] = array.difference(a, b);
    if (d.len() != 1) { return 4; }
    if (!d[0].eq(Point { x: 1, y: 2 })) { return 5; }`,
	},
	{
		// `.equal` / `.index_of` / `.contains` were converted in #6801; kept
		// here so one program covers the whole Eq-driven surface on one
		// element type.
		name: "equal and index_of",
		body: `    var a: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }];
    if (!a.equal([Point { x: 1, y: 2 }, Point { x: 3, y: 4 }])) { return 1; }
    if (a.equal([Point { x: 1, y: 2 }])) { return 2; }
    if (a.index_of(Point { x: 3, y: 4 }) != 1) { return 3; }
    if (!a.contains(Point { x: 1, y: 2 })) { return 4; }`,
	},
}

func eqBoundDeriveSrc(body string) string {
	return eqBoundPointPreamble + "function main(): i32 {\n" + body + "\n    return 42;\n}\n"
}

func TestStdArrayEqBoundDeriveElement(t *testing.T) {
	for _, c := range eqBoundDeriveCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			src := eqBoundDeriveSrc(c.body)
			// Each compiled backend gets its own sub-test so a missing
			// toolchain skips that leg alone: a bare skip here would take
			// the interp assertion down with it.
			if got := runInterpByte(t, src); got != 42 {
				t.Errorf("interp: got exit %d, want 42", got)
			}
			t.Run("wasm", func(t *testing.T) {
				if got := compileAndRunWasmbinMain(t, src); got != 42 {
					t.Errorf("wasm: got exit %d, want 42", got)
				}
			})
			t.Run("x86-64 native", func(t *testing.T) {
				if _, got := compileAndRunX86Native(t, src); got != 42 {
					t.Errorf("x86-64 native: got exit %d, want 42", got)
				}
			})
		})
	}
}

// The Eq-bounded std/set surface has the same shape: `contains` and `remove`
// decided membership with `==`, and every other combinator routes through one
// of them, so a `@derive(Eq)` element type was rejected for the whole module.
const setDeriveEqProg = `import "std/set";
import "core/cmp" as cmp;
@derive(cmp.Eq)
struct Point { x: i32, y: i32 }
function main(): i32 {
    var s: set.Set[Point] = set.set_of([Point { x: 1, y: 2 }, Point { x: 3, y: 4 }, Point { x: 1, y: 2 }]);
    if (s.len() != 2) { return 1; }
    if (!s.contains(Point { x: 1, y: 2 })) { return 2; }
    if (s.contains(Point { x: 9, y: 9 })) { return 3; }
    var r: set.Set[Point] = s.remove(Point { x: 1, y: 2 });
    if (r.len() != 1) { return 4; }
    if (r.contains(Point { x: 1, y: 2 })) { return 5; }
    if (s.len() != 2) { return 6; }
    var other: set.Set[Point] = set.set_of([Point { x: 3, y: 4 }, Point { x: 5, y: 6 }]);
    if (s.union(other).len() != 3) { return 7; }
    if (s.intersect(other).len() != 1) { return 8; }
    if (s.difference(other).len() != 1) { return 9; }
    if (s.is_disjoint(other)) { return 10; }
    if (!r.is_subset(s)) { return 11; }
    if (s.equals(other)) { return 12; }
    if (s.symmetric_difference(other).len() != 2) { return 13; }
    return 42;
}
`

func TestStdSetEqBoundDeriveElement(t *testing.T) {
	if got := runInterpByte(t, setDeriveEqProg); got != 42 {
		t.Errorf("interp: got exit %d, want 42", got)
	}
	t.Run("wasm", func(t *testing.T) {
		if got := compileAndRunWasmbinMain(t, setDeriveEqProg); got != 42 {
			t.Errorf("wasm: got exit %d, want 42", got)
		}
	})
	t.Run("x86-64 native", func(t *testing.T) {
		if _, got := compileAndRunX86Native(t, setDeriveEqProg); got != 42 {
			t.Errorf("x86-64 native: got exit %d, want 42", got)
		}
	})
}

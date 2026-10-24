package e2e

import "testing"

var dynArrViewCases = []struct {
	name     string
	src      string
	expected int
}{
	{"enum-local-elem-for",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function sum(xs: dyn Show[]): i32 { let t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { let e: Op = Add(4); let xs: dyn Show[] = [e]; return sum(xs); }`, 5},
	{"mixed-struct-enum-elem-for",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } struct Circle { r: i32 } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function sum(xs: dyn Show[]): i32 { let t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { let e: Op = Add(4); let xs: dyn Show[] = [Circle { r: 3 }, e]; return sum(xs); }`, 14},
	{"struct-local-elem-for-same-fn",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { let c: Circle = Circle { r: 3 }; let xs: dyn Show[] = [c]; let t: i32 = 0; for x in xs { t = t + x.show(); } return t; }`, 9},
	{"elem-into-local",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { let c: Circle = Circle { r: 3 }; let xs: dyn Show[] = [c]; let x = xs[0]; return x.show(); }`, 9},
	{"construction-elem-into-local",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { let xs: dyn Show[] = [Circle { r: 3 }]; let x = xs[0]; return x.show(); }`, 9},
	{"precoerced-dyn-local-elem",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { let c: Circle = Circle { r: 3 }; let d: dyn Show = c; let xs: dyn Show[] = [d]; let x = xs[0]; return x.show(); }`, 9},
	{"direct-index-regression",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function main(): i32 { let e: Op = Add(4); let xs: dyn Show[] = [e]; return xs[0].show(); }`, 5},
}

func TestX86_64DynArrElementViews(t *testing.T) {
	for _, tc := range dynArrViewCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.expected {
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

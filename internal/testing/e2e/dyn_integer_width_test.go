// An integer behind `dyn Trait` dispatches to the impl for its own type
// (#10194). The interpreter's integers carry no width, so before the coercion
// site recorded the type it called the i32 impl for every integer: silently,
// where an i32 impl existed, and with "no impl" where none did.
package e2e

import "testing"

var dynIntegerWidthCases = []struct {
	name     string
	src      string
	expected int
}{
	// Both impls exist: the i64 one must win, with no diagnostic to say
	// otherwise.
	{"i64-beside-i32", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return 100; } }
impl Show for i64 { function show(self: Self): i32 { return 200; } }
function main(): i32 {
    let n: i64 = 1;
    let a: dyn Show = n;
    return a.show() - 150;
}`, 50},
	// Each width in one array, through a parameter, with the receiver's own
	// value reaching the i64 impl.
	{"every-width", `trait Show { function show(self: Self): i32; }
impl Show for i32 { function show(self: Self): i32 { return 1; } }
impl Show for i64 { function show(self: Self): i32 { return (self as i32) * 10; } }
impl Show for u64 { function show(self: Self): i32 { return 3; } }
impl Show for u32 { function show(self: Self): i32 { return 4; } }
impl Show for u8 { function show(self: Self): i32 { return 5; } }
function total(xs: dyn Show[]): i32 {
    let t: i32 = 0;
    for x in xs { t = t + x.show(); }
    return t;
}
function main(): i32 {
    let a: i64 = 2;
    let xs: dyn Show[] = [7, a, 9 as u64, 1 as u32, 2 as u8];
    return total(xs);
}`, 33},
}

func TestInterpDynIntegerWidth(t *testing.T) {
	for _, tc := range dynIntegerWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			if out, code := runInterpExitCode(t, tc.src); code != tc.expected {
				t.Errorf("exit %d, want %d\n%s", code, tc.expected, out)
			}
		})
	}
}

func TestX86_64DynIntegerWidth(t *testing.T) {
	for _, tc := range dynIntegerWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			if out, code := compileAndRunX86_64(t, tc.src); code != tc.expected {
				t.Errorf("exit %d, want %d\n%s", code, tc.expected, out)
			}
		})
	}
}

func TestArm64DynIntegerWidth(t *testing.T) {
	for _, tc := range dynIntegerWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			if out, code := compileAndRunArm64(t, tc.src); code != tc.expected {
				t.Errorf("exit %d, want %d\n%s", code, tc.expected, out)
			}
		})
	}
}

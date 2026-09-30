package e2eselfhost

import "testing"

// genericStructLitDestCases bind a generic struct literal to a destination
// that names its instantiation: a `var` annotation, a function's return type,
// an array annotation, a parameter reached through an array-literal argument,
// a struct field, a method parameter, a value-position if's arms, or a
// lambda's declared result. The destination fixes the type argument even where
// an untyped field literal (`a: 3`) would default it to i32 (#10452, #10481,
// #10537).
var genericStructLitDestCases = []struct {
	name string
	src  string
	want int
}{
	{"var-annotation-i64-local", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var y: i64 = 4;
    var p: Same[i64] = Same { a: 3, b: y };
    return (p.a + p.b) as i32;
}
`, 7},
	{"var-annotation-wide-literal", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var p: Same[i64] = Same { a: 3, b: 4294967296 };
    return (p.b / 1073741824 + p.a) as i32;
}
`, 7},
	{"return-type", `struct Same[T] { a: T, b: T }
function mk(y: i64): Same[i64] { return Same { a: 3, b: y }; }
function main(): i32 {
    var p = mk(4294967296);
    return (p.b / 1073741824 + p.a) as i32;
}
`, 7},
	{"array-annotation", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var y: i64 = 4294967296;
    var xs: Same[i64][] = [Same { a: 3, b: y }, Same { a: 1, b: 2 }];
    return ((xs[0].a + xs[0].b + xs[1].a + xs[1].b) / 1073741824) as i32;
}
`, 4},
	{"array-argument-i64-local", `struct Same[T] { a: T, b: T }
function take(xs: Same[i64][]): i32 {
    var s: i64 = xs[0].a + xs[0].b + xs[1].a + xs[1].b;
    return (s / 1073741824 + xs[0].a) as i32;
}
function main(): i32 {
    var y: i64 = 4294967296;
    return take([Same { a: 3, b: y }, Same { a: 1, b: 2 }]);
}
`, 7},
	{"array-argument-literals-only", `struct Same[T] { a: T, b: T }
function take(xs: Same[i64][]): i32 {
    var s: i64 = xs[0].a + xs[0].b + xs[1].a + xs[1].b;
    return s as i32;
}
function main(): i32 {
    return take([Same { a: 3, b: 4 }, Same { a: 1, b: 2 }]);
}
`, 10},
	{"struct-field", `struct Same[T] { a: T, b: T }
struct Holder { s: Same[i64] }
function main(): i32 {
    var y: i64 = 4294967296;
    var h: Holder = Holder { s: Same { a: 3, b: y } };
    return (h.s.b / 1073741824 + h.s.a) as i32;
}
`, 7},
	{"method-parameter", `struct Same[T] { a: T, b: T }
struct S { xs: Same[i64][] }
impl S {
    function take(self: S, xs: Same[i64][]): i32 {
        return (xs[0].b / 1073741824 + xs[0].a) as i32;
    }
}
function main(): i32 {
    var y: i64 = 4294967296;
    var s: S = S { xs: [] };
    return s.take([Same { a: 3, b: y }]);
}
`, 7},
	{"value-block-arms", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var y: i64 = 4294967296;
    var p: Same[i64] = if (true) { Same { a: 3, b: y } } else { Same { a: 1, b: 2 } };
    return (p.b / 1073741824 + p.a) as i32;
}
`, 7},
	{"lambda-result", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var y: i64 = 4294967296;
    var f = (): Same[i64] => { return Same { a: 3, b: y }; };
    var p: Same[i64] = f();
    return (p.b / 1073741824 + p.a) as i32;
}
`, 7},
}

// TestSelfHostGenericStructLitDest compiles each case with the self-host CLI
// for every target, under both the default lowering and `FERN_SEM_IR=`, and
// checks the exit code against the interpreter.
func TestSelfHostGenericStructLitDest(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	lowerings := []struct {
		name string
		env  []string
	}{{"default", nil}, {"ast", []string{"FERN_SEM_IR="}}}
	for _, tc := range genericStructLitDestCases {
		if got := interpExit(t, interpBin, tc.src); got != tc.want {
			t.Fatalf("%s: interpreter exited %d, want %d", tc.name, got, tc.want)
		}
		for _, target := range []string{"x86-64-linux", "wasm32-wasi", "arm64-linux"} {
			for _, lw := range lowerings {
				t.Run(target+"/"+lw.name+"/"+tc.name, func(t *testing.T) {
					if stderr, code := cli.exitOf(t, tc.src, target, lw.env...); code != tc.want {
						t.Errorf("exited %d, want %d (interp oracle)\n%s", code, tc.want, stderr)
					}
				})
			}
		}
	}
}

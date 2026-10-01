package e2eselfhost

import "testing"

// genericCaptureCases read a captured generic struct or enum value inside a
// lambda. The checker records each capture's type before monomorphisation, as
// `Same[i32]`; the monomorphisers rename it to the clone, or the lifted lambda's
// parameter names a struct that no longer exists and the typed lowering refuses
// the projection (#10911). Each exit code is the native interpreter's.
var genericCaptureCases = []struct {
	name     string
	src      string
	expected int
}{
	{"struct_field", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var o = Same { a: 3, b: 4 };
    var g = (): i32 => o.a;
    return g();
}`, 3},
	{"literal_bound_wide", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var q = Same { a: 1, b: 2 };
    var f = (): i64 => q.a;
    return f() as i32;
}`, 1},
	{"array_element", `struct Box[T] { v: T }
function main(): i32 {
    var xs = [Box { v: 5 }, Box { v: 6 }];
    var g = (): i32 => xs[1].v;
    return g();
}`, 6},
	{"nested_instantiation", `struct Box[T] { v: T }
function main(): i32 {
    var nb = Box { v: Box { v: 7 } };
    var h = (): i32 => nb.v.v;
    return h();
}`, 7},
	{"escaping_closure", `struct Same[T] { a: T, b: T }
function make(): () => i32 {
    var o = Same { a: 3, b: 4 };
    return (): i32 => o.b;
}
function main(): i32 {
    var m = make();
    return m();
}`, 4},
	{"generic_enum_of_struct", `struct Box[T] { v: T }
enum Opt[T] { Has(T), Nope }
function main(): i32 {
    var op: Opt[Box[i32]] = Has(Box { v: 8 });
    var k = (): i32 => {
        match (op) { Has(b) => { return b.v; }, Nope => { return 0; } }
        return 0;
    };
    return k();
}`, 8},
}

func TestSelfHostGenericCaptureIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range genericCaptureCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target, "FERN_STRICT_IR=1"); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}

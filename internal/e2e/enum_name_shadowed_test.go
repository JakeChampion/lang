package e2e

import "testing"

// A local named like an enum shadows it, so `R.len()` and `R.X` on the local
// are a method call and a field read, not the enum's variants (#10159).
// A local captured into a lambda shadows it there too (#10211). Outside the
// shadow the qualified variants still construct.
const enumNameShadowedProg = `
enum R { X, Y, Z(i32) }
struct S { X: i32 }

function string_local(): i32 { var R: string = "abc"; return R.len(); }
function struct_local(): i32 { var R: S = S { X: 7 }; return R.X; }
function param(R: S): i32 { return R.X; }
function captured_field(): i32 { var R: S = S { X: 6 }; var inner = (): i32 => { return R.X; }; return inner(); }
function captured_method(): i32 { var R: string = "abcd"; var inner = (): i32 => { return R.len(); }; return inner(); }
function tag(r: R): i32 { match (r) { X => { return 1; }, Y => { return 2; }, Z(n) => { return n; } } }

function main(): i32 {
    if (string_local() != 3) { return 1; }
    if (struct_local() != 7) { return 2; }
    if (param(S { X: 9 }) != 9) { return 3; }
    if (tag(R.Y) != 2) { return 4; }
    if (tag(R.Z(5)) != 5) { return 5; }
    if (captured_field() != 6) { return 6; }
    if (captured_method() != 4) { return 7; }
    return 42;
}
`

const enumShadowWant = "want 42 (1 = a string local's method read as a variant, 2 = a struct local's field, " +
	"3 = a parameter's field, 4/5 = an unshadowed qualified variant stopped constructing, " +
	"6/7 = a local captured into a lambda read as a variant)"

func TestEnumNameShadowedByLocalInterp(t *testing.T) {
	if got := runInterpExit(t, enumNameShadowedProg); got != 42 {
		t.Fatalf("interp got %d, %s", got, enumShadowWant)
	}
}

func TestEnumNameShadowedByLocalX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, enumNameShadowedProg); got != 42 {
		t.Fatalf("x86-64 got %d, %s", got, enumShadowWant)
	}
}

func TestEnumNameShadowedByLocalArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, enumNameShadowedProg); got != 42 {
		t.Fatalf("arm64 got %d, %s", got, enumShadowWant)
	}
}

func TestEnumNameShadowedByLocalWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, enumNameShadowedProg); got != 42 {
		t.Fatalf("wasm got %d, %s", got, enumShadowWant)
	}
}

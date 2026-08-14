package e2eselfhost

import (
	"runtime"
	"testing"
)

// interpEnumCtorCases are enum-constructor programs run through the self-host
// interpreter's stdin driver. A payload-carrying constructor is not a
// declaration the interpreter's function table holds — the parser lowers
// `enum Shape { Circle(i32) }` to a variant STRUCT, never a FuncDecl — so
// before #6808 `Circle(7)` reached the end of call_func's walk and came back as
// `undefined function: Circle`. Only the four Option / Result names were
// special-cased, which left every user enum and both checker-injected ones
// (JsonValue, IoError) uninterpretable.
//
// Each case is oracle-checked against the native interpreter rather than
// against a hardcoded exit code, so what they pin is the language's behaviour.
var interpEnumCtorCases = []struct {
	name string
	src  string
}{
	// The issue's repro.
	{"single-payload", "enum Shape { Circle(i32), Square(i32) }\nfunction main(): i32 {\n  var s: Shape = Circle(7);\n  match (s) { Circle(r) => { return r; }, Square(_) => { return 1; } }\n}\n"},
	// A payload-less variant arrives as a bare IDENTIFIER, not a call, so it
	// took the other error path: `undefined identifier: Green`.
	{"payloadless-bare", "enum Color { Red, Green, Blue }\nfunction main(): i32 {\n  var c: Color = Green;\n  match (c) { Red => { return 1; }, Green => { return 7; }, Blue => { return 3; } }\n}\n"},
	// The qualified spelling of the same thing, which already worked — a field
	// access on an unresolvable identifier was read as a nullary variant. Kept
	// as the control on that path.
	{"payloadless-qualified", "enum Color { Red, Green }\nfunction main(): i32 {\n  var c: Color = Color.Green;\n  match (c) { Red => { return 1; }, Green => { return 7; } }\n}\n"},
	{"multi-payload", "enum Point { Zero, At(i32, i32) }\nfunction main(): i32 {\n  var p: Point = At(3, 4);\n  match (p) { Zero => { return 1; }, At(x, y) => { return x + y; } }\n}\n"},
	{"generic-enum", "enum Box[T] { Empty, Full(T) }\nfunction main(): i32 {\n  var b: Box[i32] = Full(7);\n  match (b) { Empty => { return 1; }, Full(v) => { return v; } }\n}\n"},
	// Option / Result, the four names that were special-cased before and are
	// now ordinary entries in the same variant table.
	{"option-result", "function find(n: i32): Option[i32] {\n  if (n > 0) { return Some(n * 2); }\n  return None;\n}\nfunction half(n: i32): Result[i32, string] {\n  if (n % 2 == 0) { return Ok(n / 2); }\n  return Err(\"odd\");\n}\nfunction main(): i32 {\n  var a: i32 = 0;\n  match (find(3)) { Some(v) => { a = v; }, None => { a = 100; } }\n  match (find(0 - 1)) { Some(_) => { a = a + 100; }, None => { a = a + 1; } }\n  match (half(4)) { Ok(v) => { a = a + v; }, Err(_) => { a = a + 100; } }\n  match (half(5)) { Ok(_) => { a = a + 100; }, Err(e) => { if (e == \"odd\") { a = a + 1; } } }\n  if (a == 10) { return 7; }\n  return a;\n}\n"},
	// JsonValue and IoError are declared in no module the interpreter parses —
	// the front end injects them — so the interpreter needs its own copy of
	// their variant decls, the way internal/interp gets them from the checker's
	// builtin table. `Other` is the one builtin variant carrying TWO payloads.
	{"builtin-jsonvalue", "function main(): i32 {\n  var v: JsonValue = JNumber(\"42\");\n  match (v) {\n    JNumber(s) => { if (s == \"42\") { return 7; } return 1; },\n    JNull => { return 2; },\n    _ => { return 3; }\n  }\n}\n"},
	{"builtin-ioerror", "function main(): i32 {\n  var e: IoError = Other(\"p\", \"m\");\n  match (e) {\n    NotFound(_) => { return 1; },\n    Other(p, m) => { if (p == \"p\" && m == \"m\") { return 7; } return 2; },\n    _ => { return 3; }\n  }\n}\n"},
	// A constructed value has to survive being passed, returned, and stored in
	// a collection, not just matched where it was built.
	{"variant-through-calls", "enum Shape { Circle(i32), Square(i32) }\nfunction mk(r: i32): Shape { return Circle(r); }\nfunction area(s: Shape): i32 {\n  match (s) { Circle(r) => { return r * 2; }, Square(w) => { return w; } }\n}\nfunction main(): i32 {\n  var xs: Shape[] = [mk(2), Square(3)];\n  return area(xs[0]) + area(xs[1]);\n}\n"},
	// A method declared on the ENUM type, dispatched on a constructed value —
	// the union-alias dispatch path, which only sees variants that were built
	// in the first place.
	{"enum-method-dispatch", "enum Shape { Circle(i32), Square(i32) }\nfunction (s: Shape) size(): i32 {\n  match (s) { Circle(r) => { return r; }, Square(w) => { return w * 2; } }\n}\nfunction main(): i32 {\n  var a: Shape = Circle(3);\n  var b: Shape = Square(2);\n  return a.size() + b.size();\n}\n"},
	// A variant carrying another enum, so the payload is itself a variant.
	{"nested-variant-payload", "enum Inner { One, Two }\nenum Outer { Wrap(Inner) }\nfunction main(): i32 {\n  var o: Outer = Wrap(Two);\n  match (o) {\n    Wrap(i) => { match (i) { One => { return 1; }, Two => { return 7; } } }\n  }\n}\n"},

	// CONTROLS. The fix classifies MORE names as variant constructions, so what
	// can break is a name that is a variant AND something else.
	//
	// A user function of the variant's name must still win: variant lookup is
	// gated behind user_func_exists.
	{"function-shadows-variant", "enum Shape { Circle(i32), Square(i32) }\nfunction Circle(r: i32): i32 { return r + 1; }\nfunction main(): i32 { return Circle(6); }\n"},
	// A local binding must still win: the env lookup runs before the variant
	// table is consulted at all.
	{"local-shadows-variant", "enum Color { Red, Green }\nfunction main(): i32 {\n  var Red: i32 = 7;\n  return Red;\n}\n"},
	// A CONST of a variant's name — a const is a zero-arg function rather than
	// a binding, so it is resolved on a different branch from the local above.
	{"const-shadows-variant", "enum Color { Red, Green }\nconst Red: i32 = 7;\nfunction main(): i32 { return Red; }\n"},
}

// TestSelfHostInterpEnumConstructors drives each case through a compiled
// `interp_run.fern` — the stdin driver, which resolves no imports, so every
// case here is self-contained. The std/json half of #6808 needs the module
// loader and lives in TestSelfHostInterpStdlibModload instead.
//
// Host modes mirror TestSelfHostInterpStdlibModload: on Apple Silicon the
// driver is built for arm64-darwin through the in-process Mach-O path, off it
// with the Go x86-64 backend.
func TestSelfHostInterpEnumConstructors(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run")
	}
	interpBin := buildLangBinForInterp(t)

	for _, tc := range interpEnumCtorCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 7 {
				t.Fatalf("native interp oracle exited %d, want 7 — the case itself is wrong, not the self-host engine", want)
			}
			if got := runDriverExit(t, runner, driverBin, []byte(tc.src)); got != want {
				t.Errorf("self-host interp exited %d, want %d (native interp oracle)", got, want)
			}
		})
	}
}

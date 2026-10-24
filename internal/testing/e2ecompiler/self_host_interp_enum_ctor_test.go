package e2ecompiler

import (
	"runtime"
	"testing"
)

var interpEnumCtorCases = []struct {
	name string
	src  string
}{
	{"single-payload", "enum Shape { Circle(i32), Square(i32) }\nfunction main(): i32 {\n  let s: Shape = Circle(7);\n  match (s) { Circle(r) => { return r; }, Square(_) => { return 1; } }\n}\n"},
	{"payloadless-bare", "enum Color { Red, Green, Blue }\nfunction main(): i32 {\n  let c: Color = Green;\n  match (c) { Red => { return 1; }, Green => { return 7; }, Blue => { return 3; } }\n}\n"},
	{"payloadless-qualified", "enum Color { Red, Green }\nfunction main(): i32 {\n  let c: Color = Color.Green;\n  match (c) { Red => { return 1; }, Green => { return 7; } }\n}\n"},
	{"multi-payload", "enum Point { Zero, At(i32, i32) }\nfunction main(): i32 {\n  let p: Point = At(3, 4);\n  match (p) { Zero => { return 1; }, At(x, y) => { return x + y; } }\n}\n"},
	{"generic-enum", "enum Box[T] { Empty, Full(T) }\nfunction main(): i32 {\n  let b: Box[i32] = Full(7);\n  match (b) { Empty => { return 1; }, Full(v) => { return v; } }\n}\n"},
	{"option-result", "function find(n: i32): Option[i32] {\n  if (n > 0) { return Some(n * 2); }\n  return None;\n}\nfunction half(n: i32): Result[i32, string] {\n  if (n % 2 == 0) { return Ok(n / 2); }\n  return Err(\"odd\");\n}\nfunction main(): i32 {\n  let a: i32 = 0;\n  match (find(3)) { Some(v) => { a = v; }, None => { a = 100; } }\n  match (find(0 - 1)) { Some(_) => { a = a + 100; }, None => { a = a + 1; } }\n  match (half(4)) { Ok(v) => { a = a + v; }, Err(_) => { a = a + 100; } }\n  match (half(5)) { Ok(_) => { a = a + 100; }, Err(e) => { if (e == \"odd\") { a = a + 1; } } }\n  if (a == 10) { return 7; }\n  return a;\n}\n"},
	{"builtin-ioerror", "function main(): i32 {\n  let e: IoError = Other(\"p\", \"m\", 9);\n  match (e) {\n    NotFound(_) => { return 1; },\n    Other(p, m, n) => { if (p == \"p\" && m == \"m\" && n == 9) { return 7; } return 2; },\n    _ => { return 3; }\n  }\n}\n"},
	{"variant-through-calls", "enum Shape { Circle(i32), Square(i32) }\nfunction mk(r: i32): Shape { return Circle(r); }\nfunction area(s: Shape): i32 {\n  match (s) { Circle(r) => { return r * 2; }, Square(w) => { return w; } }\n}\nfunction main(): i32 {\n  let xs: Shape[] = [mk(2), Square(3)];\n  return area(xs[0]) + area(xs[1]);\n}\n"},
	{"enum-method-dispatch", "enum Shape { Circle(i32), Square(i32) }\nfunction (s: Shape) size(): i32 {\n  match (s) { Circle(r) => { return r; }, Square(w) => { return w * 2; } }\n}\nfunction main(): i32 {\n  let a: Shape = Circle(3);\n  let b: Shape = Square(2);\n  return a.size() + b.size();\n}\n"},
	{"builtin-enum-method-dispatch", "function (b: Body) kind(): i32 {\n  match (b) { BodyText(_) => { return 1; }, BodyBytes(bs) => { return bs.len() + 5; }, _ => { return 2; } }\n}\nfunction main(): i32 {\n  let bs: u8[] = [1 as u8, 2 as u8];\n  let b: Body = BodyBytes(bs);\n  return b.kind();\n}\n"},
	{"nested-variant-payload", "enum Inner { One, Two }\nenum Outer { Wrap(Inner) }\nfunction main(): i32 {\n  let o: Outer = Wrap(Two);\n  match (o) {\n    Wrap(i) => { match (i) { One => { return 1; }, Two => { return 7; } } }\n  }\n}\n"},

	{"function-shadows-variant", "enum Shape { Circle(i32), Square(i32) }\nfunction Circle(r: i32): i32 { return r + 1; }\nfunction main(): i32 { return Circle(6); }\n"},
	{"local-shadows-variant", "enum Color { Red, Green }\nfunction main(): i32 {\n  let Red: i32 = 7;\n  return Red;\n}\n"},
	{"const-shadows-variant", "enum Color { Red, Green }\nconst Red: i32 = 7;\nfunction main(): i32 { return Red; }\n"},
}

func TestSelfHostInterpEnumConstructors(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
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

package e2ecompiler

import "testing"

var selfHostAppendValueCases = []struct {
	name string
	src  string
}{
	{"static-literal-capacity", `@noinline function pressure(): i32 {
  let noise: u8[] = __alloc_u8(32);
  if (noise[0] != 0 as u8 || noise.len() != 32) { return 1; }
  return 0;
}
function shared(bytes: u8[]): boolean {
  let alias = bytes;
  let extended = bytes.append(99 as u8);
  if (pressure() != 0 || extended.len() != alias.len()+1) { return false; }
  let i = 0;
  while (i < alias.len()) {
    if (alias[i] != (i % 251) as u8 || extended[i] != alias[i]) { return false; }
    i=i+1;
  }
  return extended[alias.len()] == 99 as u8;
}
function consumed(own bytes: u8[], count: i32): boolean {
  let extended = bytes.append(99 as u8);
  if (pressure() != 0 || extended.len() != count+1) { return false; }
  let i = 0;
  while (i < count) { if (extended[i] != (i % 251) as u8) { return false; } i=i+1; }
  return extended[count] == 99 as u8;
}
function main(): i32 {
  if (!shared([]) || !consumed([],0)) { return 1; }
  if (!shared([0 as u8]) || !consumed([0 as u8],1)) { return 2; }
  if (!shared([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8]) || !consumed([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8],31)) { return 32; }
  if (!shared([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8]) || !consumed([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8],32)) { return 33; }
  if (!shared([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8]) || !consumed([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8],33)) { return 34; }
  if (!shared([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8, 33 as u8, 34 as u8, 35 as u8, 36 as u8, 37 as u8, 38 as u8, 39 as u8, 40 as u8, 41 as u8, 42 as u8, 43 as u8, 44 as u8, 45 as u8, 46 as u8, 47 as u8, 48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, 58 as u8, 59 as u8, 60 as u8, 61 as u8, 62 as u8]) || !consumed([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8, 33 as u8, 34 as u8, 35 as u8, 36 as u8, 37 as u8, 38 as u8, 39 as u8, 40 as u8, 41 as u8, 42 as u8, 43 as u8, 44 as u8, 45 as u8, 46 as u8, 47 as u8, 48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, 58 as u8, 59 as u8, 60 as u8, 61 as u8, 62 as u8],63)) { return 64; }
  if (!shared([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8, 33 as u8, 34 as u8, 35 as u8, 36 as u8, 37 as u8, 38 as u8, 39 as u8, 40 as u8, 41 as u8, 42 as u8, 43 as u8, 44 as u8, 45 as u8, 46 as u8, 47 as u8, 48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, 58 as u8, 59 as u8, 60 as u8, 61 as u8, 62 as u8, 63 as u8]) || !consumed([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8, 33 as u8, 34 as u8, 35 as u8, 36 as u8, 37 as u8, 38 as u8, 39 as u8, 40 as u8, 41 as u8, 42 as u8, 43 as u8, 44 as u8, 45 as u8, 46 as u8, 47 as u8, 48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, 58 as u8, 59 as u8, 60 as u8, 61 as u8, 62 as u8, 63 as u8],64)) { return 65; }
  if (!shared([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8, 33 as u8, 34 as u8, 35 as u8, 36 as u8, 37 as u8, 38 as u8, 39 as u8, 40 as u8, 41 as u8, 42 as u8, 43 as u8, 44 as u8, 45 as u8, 46 as u8, 47 as u8, 48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, 58 as u8, 59 as u8, 60 as u8, 61 as u8, 62 as u8, 63 as u8, 64 as u8]) || !consumed([0 as u8, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8, 16 as u8, 17 as u8, 18 as u8, 19 as u8, 20 as u8, 21 as u8, 22 as u8, 23 as u8, 24 as u8, 25 as u8, 26 as u8, 27 as u8, 28 as u8, 29 as u8, 30 as u8, 31 as u8, 32 as u8, 33 as u8, 34 as u8, 35 as u8, 36 as u8, 37 as u8, 38 as u8, 39 as u8, 40 as u8, 41 as u8, 42 as u8, 43 as u8, 44 as u8, 45 as u8, 46 as u8, 47 as u8, 48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, 58 as u8, 59 as u8, 60 as u8, 61 as u8, 62 as u8, 63 as u8, 64 as u8],65)) { return 66; }
  let integers: i32[] = [1,2,3,4,5,6,7,8];
  let next = integers.append(9);
  if (pressure() != 0) { return 90; }
  let i = 0;
  while (i < next.len()) { if (next[i] != i+1) { return 91; } i=i+1; }
  return 0;
}
`},
	{"arg-position-i32", "function sink(xs: i32[]): i32 {\n    let s: i32 = 0;\n    for i in 0..xs.len() { s = s + xs[i]; }\n    return s;\n}\nfunction main(): i32 {\n    let roomy: i32[] = [];\n    roomy = roomy.append(1);\n    roomy = roomy.append(2);\n    roomy = roomy.append(3);\n    let a: i32 = sink(roomy.append(20));\n    return a + sink(roomy) * 2 + roomy.len();\n}"},
	{"arg-position-string", "function chars(xs: string[]): i32 {\n    let t: i32 = 0;\n    for i in 0..xs.len() { t = t + xs[i].len(); }\n    return t;\n}\nfunction main(): i32 {\n    let xs: string[] = [];\n    xs = xs.append(\"aa\");\n    xs = xs.append(\"bb\");\n    xs = xs.append(\"cc\");\n    let a: i32 = chars(xs.append(\"dddd\"));\n    return a + chars(xs) + xs.len();\n}"},
	{"arg-position-i64", "function s8(xs: i64[]): i64 {\n    let t: i64 = 0i64;\n    for i in 0..xs.len() { t = t + xs[i]; }\n    return t;\n}\nfunction main(): i32 {\n    let xs: i64[] = [];\n    xs = xs.append(1i64);\n    xs = xs.append(2i64);\n    xs = xs.append(3i64);\n    let a: i64 = s8(xs.append(20i64));\n    let b: i64 = s8(xs);\n    return (a as i32) + (b as i32) * 2 + xs.len();\n}"},
	{"arg-position-f64", "function sf(xs: f64[]): f64 {\n    let t: f64 = 0.0;\n    for i in 0..xs.len() { t = t + xs[i]; }\n    return t;\n}\nfunction main(): i32 {\n    let xs: f64[] = [];\n    xs = xs.append(1.0);\n    xs = xs.append(2.0);\n    xs = xs.append(3.0);\n    let a: f64 = sf(xs.append(20.0));\n    let b: f64 = sf(xs);\n    return (a as i32) + (b as i32) * 2 + xs.len();\n}"},
	{"map-literal-position", "import \"core/map\";\nfunction sink(xs: i32[]): i32 {\n    let s: i32 = 0;\n    for i in 0..xs.len() { s = s + xs[i]; }\n    return s;\n}\nfunction main(): i32 {\n    let roomy: i32[] = [];\n    roomy = roomy.append(1);\n    roomy = roomy.append(2);\n    roomy = roomy.append(3);\n    let m: Map[string, i32] = Map { \"k\": sink(roomy.append(20)) };\n    return m.get_or(\"k\", 0) + sink(roomy) * 2 + roomy.len();\n}"},
	{"operand-position", "function main(): i32 {\n    let roomy: i32[] = [];\n    roomy = roomy.append(1);\n    roomy = roomy.append(2);\n    roomy = roomy.append(3);\n    return roomy.append(9).len() * 10 + roomy.len();\n}"},
	{"loop-body-param", "function sink(xs: i32[]): i32 {\n    let s: i32 = 0;\n    for i in 0..xs.len() { s = s + xs[i]; }\n    return s;\n}\nfunction walk(path: i32[]): i32 {\n    let t: i32 = 0;\n    for i in 0..4 { t = t + sink(path.append(i)); }\n    return t;\n}\nfunction main(): i32 {\n    let p: i32[] = [];\n    p = p.append(1);\n    p = p.append(2);\n    return walk(p) + p.len();\n}"},
	{"inplace-shapes-control", "function tail(acc: i32[], x: i32): i32[] {\n    return acc.append(x);\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    let i: i32 = 0;\n    while (i < 20) { a = tail(a, i); i = i + 1; }\n    let b: i32[] = [];\n    let j: i32 = 0;\n    while (j < 20) { b = b.append(j); j = j + 1; }\n    return a.len() + b.len();\n}"},
}

func TestSelfHostAppendValueSemanticsX86_64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cli := newStrictCLI(t)
	for _, tc := range selfHostAppendValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostAppendValueSemanticsArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	cli := newStrictCLI(t)
	for _, tc := range selfHostAppendValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostAppendValueSemanticsWasmIR(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cli := newStrictCLI(t)
	for _, tc := range selfHostAppendValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

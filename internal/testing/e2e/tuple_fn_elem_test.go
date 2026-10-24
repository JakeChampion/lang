package e2e

import "testing"

var tupleFnElemCases = []struct {
	name string
	src  string
	exit int
}{
	{"tuple-closure-returned", "function mk(): ((i32) => i32, i32) { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return t; } function main(): i32 { let t = mk(); return t.0(37); }", 42},
	{"tuple-closure-uncalled", "function mk(): ((i32) => i32, i32) { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 41); return t; } function main(): i32 { let t = mk(); return t.1 + 1; }", 42},
	{"tuple-closure-nocapture", "function mk(): ((i32) => i32, i32) { let t = ((x: i32): i32 => { return x + 1; }, 1); return t; } function main(): i32 { let t = mk(); return t.0(41); }", 42},
	{"tuple-closure-local", "function main(): i32 { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return t.0(37); }", 42},
	{"tuple-named-fn", "function dbl(x: i32): i32 { return x * 2; } function main(): i32 { let t = (dbl, 1); return t.0(21); }", 42},
	{"tuple-two-closures", "function mk(): ((i32) => i32, (i32) => i32) { let n = 1; let m = 2; let t = ((x: i32): i32 => { return x + n; }, (x: i32): i32 => { return x + m; }); return t; } function main(): i32 { let t = mk(); return t.0(19) + t.1(20); }", 42},
	{"array-of-tuple-closure", "function main(): i32 { let n = 5; let a = [((x: i32): i32 => { return x + n; }, 1)]; return a[0].0(37); }", 42},
	{"nested-tuple-closure", "function main(): i32 { let n = 5; let t = (((x: i32): i32 => { return x + n; }, 1), 2); return t.0.0(37); }", 42},
}

func TestX86_64TupleFnElem(t *testing.T) {
	for _, c := range tupleFnElemCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}

func TestArm64TupleFnElem(t *testing.T) {
	for _, c := range tupleFnElemCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}

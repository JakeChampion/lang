package checker

import (
	"strings"
	"testing"
)

const constStructDecls = `struct Ring[const N: i32, T] { items: T[], head: i32 }
function cap[const N: i32, T](r: Ring[N, T]): i32 { return N; }
`

// A const parameter on a struct: each rejected program names its code and the
// words of its message.
func TestConstParamStructRejected(t *testing.T) {
	for name, tc := range map[string]struct{ src, code, msg string }{
		"type for const":    {`function f(r: Ring[string, i32]): i32 { return 0; }`, "E090", "const parameter N takes an integer, not the type string"},
		"integer for type":  {`function f(r: Ring[4, 5]): i32 { return 0; }`, "E090", "type parameter T takes a type, not the integer 5"},
		"type param":        {`function f[T](r: Ring[T, T]): i32 { return 0; }`, "E090", "const parameter N takes an integer, not the type T"},
		"past i32":          {`function f(r: Ring[2147483648, i32]): i32 { return 0; }`, "E089", "the const argument for N must fit in i32"},
		"identity":          {`function f(r: Ring[4, i32]): i32 { return 0; } function g(r: Ring[2, i32]): i32 { return f(r); }`, "E038", "expected Ring[4, i32], got Ring[2, i32]"},
		"no argument":       {`function f(): i32 { let r = Ring { items: [1], head: 0 }; return r.head; }`, "E040", "could not infer type parameter N for struct Ring"},
		"method assignment": {`function (r: Ring[N, T]) bad(): i32 { N = 3; return N; }`, "E091", "cannot assign to const parameter N"},
	} {
		err := checkSource(t, constStructDecls+tc.src)
		if err == nil || !hasCode(err, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: want %q in the message, got %v", name, tc.msg, err)
		}
	}
}

// What a const parameter on a struct allows: an annotation, an inferred N, a
// method that reads N, and a struct that passes its own N on.
func TestConstParamStructAccepted(t *testing.T) {
	for name, src := range map[string]string{
		"annotation": `function f(): i32 { let r: Ring[4, string] = Ring { items: ["a"], head: 0 }; return cap(r); }`,
		"written":    `function f(): i32 { return cap(Ring[0x10, i32] { items: [], head: 0 }); }`,
		"method":     `function (r: Ring[N, T]) full(): boolean { return r.items.len() >= N; }`,
		"passed on":  `function g[const M: i32](x: i32): Ring[M, i32] { let r: Ring[M, i32] = Ring { items: [x], head: 0 }; return r; }`,
		"nested":     `struct Pair[const N: i32] { a: Ring[N, i32], b: i32 } function f(): i32 { let p: Pair[3] = Pair { a: Ring { items: [1], head: 0 }, b: 1 }; return p.b; }`,
	} {
		if err := checkSource(t, constStructDecls+src); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
}

const constEnumDecls = `struct Ring[const N: i32, T] { items: T[], head: i32 }
enum Slot[const N: i32, T] { Full(Ring[N, T]), Empty }
trait Cap { function cap(self: Self): i32; }
`

// A const parameter on an enum, and an impl over N.
func TestConstParamEnumAndImpl(t *testing.T) {
	for name, tc := range map[string]struct{ src, code, msg string }{
		"type for const":   {`function f(s: Slot[string, i32]): i32 { return 0; }`, "E090", "const parameter N takes an integer, not the type string"},
		"integer for type": {`function f(s: Slot[4, 5]): i32 { return 0; }`, "E090", "type parameter T takes a type, not the integer 5"},
		"past i32":         {`function f(s: Slot[2147483648, i32]): i32 { return 0; }`, "E089", "the const argument for N must fit in i32"},
		"identity":         {`function f(s: Slot[4, i32]): i32 { return 0; } function g(s: Slot[2, i32]): i32 { return f(s); }`, "E038", "expected Slot[4, i32], got Slot[2, i32]"},
		"payload kind":     {`enum Bad[const M: i32] { One(Ring[i32, i32]) }`, "E090", "const parameter N takes an integer, not the type i32"},
		"method assigns":   {`function (s: Slot[N, T]) bad(): i32 { N = 3; return N; }`, "E091", "cannot assign to const parameter N"},
	} {
		err := checkSource(t, constEnumDecls+tc.src)
		if err == nil || !hasCode(err, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: want %q in the message, got %v", name, tc.msg, err)
		}
	}
	for name, src := range map[string]string{
		"method":    `function (s: Slot[N, T]) cap_of(): i32 { return N; }`,
		"inferred":  `function used[const N: i32, T](s: Slot[N, T]): i32 { return N; } function f(): i32 { let s: Slot[4, string] = Empty; return used(s); }`,
		"payload":   `function f(): i32 { let s: Slot[4, string] = Full(Ring { items: ["x"], head: 0 }); return 0; }`,
		"impl":      `impl[const N: i32, T] Cap for Ring[N, T] { function cap(self: Ring[N, T]): i32 { return N - self.items.len(); } }`,
		"enum impl": `impl[const N: i32, T] Cap for Slot[N, T] { function cap(self: Slot[N, T]): i32 { return N; } }`,
		"passed on": `enum Pair[const M: i32] { Two(Slot[M, i32], Slot[M, i32]) }`,
	} {
		if err := checkSource(t, constEnumDecls+src); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
}

const constParamDecls = `function filled[const N: i32](x: f64): f64[] { let out: f64[] = []; while (out.len() < N) { out = out.append(x); } return out; }
function count[const N: i32, T](x: T): i32 { return N * 10; }
`

func TestConstParamRejected(t *testing.T) {
	for name, tc := range map[string]struct{ src, code, msg string }{
		"value argument":   {`function f(k: i32): i32 { return filled[k](1.0).len(); }`, "E089", "the const argument for N must be an integer literal or a const parameter"},
		"expression":       {`function f(k: i32): i32 { return filled[k + 1](1.0).len(); }`, "E089", "the const argument for N must be an integer literal or a const parameter"},
		"type for const":   {`function f(): i32 { return count[string, string]("a"); }`, "E090", "const parameter N takes an integer, not the type string"},
		"integer for type": {`function f(): i32 { return count[3, 4]("a"); }`, "E090", "type parameter T takes a type, not the integer 4"},
		"type parameter":   {`function f[T](x: T): i32 { return count[T, T](x); }`, "E090", "const parameter N takes an integer, not the type T"},
		"assignment":       {`function bump[const N: i32](): i32 { N = 4; return N; }`, "E091", "cannot assign to const parameter N"},
		"past i32":         {`function f(): i32 { return count[2147483648, string]("a"); }`, "E089", "the const argument for N must fit in i32"},
		"index past i32":   {`function f(): i32 { return filled[0x80000000](1.0).len(); }`, "E089", "the const argument for N must fit in i32"},
		"parameter name":   {`function g[const N: i32](N: i32): i32 { return N; }`, "E018", "duplicate parameter \"N\""},
		"captured":         {`function bump[const N: i32](): i32 { let h = (x: i32): i32 => { N = x; return x; }; return h(1); }`, "E091", "cannot assign to const parameter N"},
	} {
		err := checkSource(t, constParamDecls+tc.src)
		if err == nil || !hasCode(err, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: want %q in the message, got %v", name, tc.msg, err)
		}
	}
}

func TestConstParamAccepted(t *testing.T) {
	for name, src := range map[string]string{
		"literal":        `function f(): i32 { return filled[4](1.0).len() + count[3, string]("a"); }`,
		"passed on":      `function g[const M: i32](): i32 { return filled[M](1.0).len() + count[M, i32](1); }`,
		"i32 arithmetic": `function g[const M: i32](): i64 { let n: i32 = M * 2 + 1; return n as i64; }`,
		"lambda":         `function g[const M: i32](): i32 { let h = (x: i32): i32 => x + M; return h(1); }`,
		"radix literals": `function f(): i32 { return filled[0x3](1.0).len() + count[0b101, string]("a") + count[1_000, string]("a") + count[2147483647, string]("a"); }`,
		"local copy":     `function g[const M: i32](): i32 { let k: i32 = M; k = 3; return k; }`,
		"shadowing let":  `function g[const M: i32](): i32 { let M: i32 = M * 2; M = M + 1; return M; }`,
		"lambda param":   `function g[const M: i32](): i32 { let h = (M: i32): i32 => { M = M + 1; return M; }; return h(1); }`,
		"loop variable":  `function g[const M: i32](xs: i32[]): i32 { let t: i32 = 0; for M in xs { M = M + 1; t = t + M; } return t; }`,
	} {
		if err := checkSource(t, constParamDecls+src); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
}

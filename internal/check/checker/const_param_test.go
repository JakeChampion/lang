package checker

import (
	"strings"
	"testing"
)

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

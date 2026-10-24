package e2ecompiler

import "testing"

const answerPrelude = `trait Answer { function answer(self: Self): i32; }
struct Boom { n: i32 }
impl Answer for Boom { function answer(self: Self): i32 { return self.n; } }
function inner[E: Answer](r: Result[i32, E]): i32 {
    match (r) { Ok(v) => { return v; }, Err(e) => { return e.answer(); } }
    return 0;
}
`

var genericArgBindingCases = []struct{ name, src string }{
	{"tuple-element", answerPrelude + `function outer[E: Answer](p: (i32, Result[i32, E])): i32 { return inner(p.1); }
function main(): i32 {
    let p: (i32, Result[i32, Boom]) = (1, Err(Boom { n: 7 }));
    if (outer(p) != 7) { return 1; }
    return 0;
}
`},
	{"err-construction", answerPrelude + `function outer[S, E: Answer](s: S, r: Result[i32, E]): (S, i32) { return (s, inner(r)); }
function main(): i32 {
    let o: (string, i32) = outer("s", Err(Boom { n: 7 }));
    if (o.1 != 7) { return 1; }
    return 0;
}
`},
	{"some-and-err-construction", answerPrelude + `function opt[T: Answer](o: Option[T]): i32 { match (o) { Some(x) => { return x.answer(); }, None => { return 0; } } return 0; }
function main(): i32 {
    if (inner(Err(Boom { n: 7 })) + opt(Some(Boom { n: 5 })) != 12) { return 1; }
    return 0;
}
`},
	{"map-loop-key-and-value", answerPrelude + `import "core/map";
function get[T: Answer](x: T): i32 { return x.answer(); }
function main(): i32 {
    let m: Map[i32, Boom] = Map { 1: Boom { n: 7 } };
    let t: i32 = 0;
    for (k, v) in m { t = t + k + get(v); }
    if (t != 8) { return 1; }
    return 0;
}
`},
	{"unannotated-map-loop-key-and-value", answerPrelude + `import "core/map";
function get[T: Answer](x: T): i32 { return x.answer(); }
function main(): i32 {
    let m = Map { 1: Boom { n: 7 } };
    let t: i32 = 0;
    for (k, v) in m { t = t + k + get(v); }
    if (t != 8) { return 1; }
    return 0;
}
`},
	{"unannotated-map-values-loop", answerPrelude + `import "core/map";
function get[T: Answer](x: T): i32 { return x.answer(); }
function main(): i32 {
    let m = Map { 1: Boom { n: 7 }, 2: Boom { n: 5 } };
    let t: i32 = 0;
    for v in m.values() { t = t + get(v); }
    if (t != 12) { return 1; }
    return 0;
}
`},
	{"tuple-array-loop-element", answerPrelude + `function get[T: Answer](x: T): i32 { return x.answer(); }
function main(): i32 {
    let xs: (i32, Boom)[] = [(1, Boom { n: 7 })];
    let t: i32 = 0;
    for (a, b) in xs { t = t + a + get(b); }
    if (t != 8) { return 1; }
    return 0;
}
`},
}

func TestSelfHostGenericArgBinding(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range genericArgBindingCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != 0 {
					t.Errorf("exited %d, want 0\n%s", code, stderr)
				}
			})
		}
	}
}

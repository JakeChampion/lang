package e2e

import (
	"strings"
	"testing"
)

// `?` inside a lambda propagates to the lambda's own return type (#9515). The
// checker used to read the ENCLOSING function's return type, so every such
// lambda drew E042 ("got void"). Both spellings are here: an unannotated
// binding, where the lambda's type is inferred from its returns with the
// failure edge among them, and a binding annotated with the function type.
// Each exercises Option's build-a-None edge and Result's forward-the-Err one.
const lambdaTryHelpers = `function g(v: i32): Option[i32] {
    if (v < 100) { return Some(v + 1); }
    return None;
}
function h(v: i32): Result[i32, string] {
    if (v < 100) { return Ok(v * 2); }
    return Err("big");
}
`

const lambdaTryBody = `    var a: i32 = 0;
    match (f(1)) { Some(v) => { a = v; }, None => { a = 99; } }
    match (f(500)) { Some(v) => { a = a + v; }, None => { a = a + 100; } }
    match (r(3)) { Ok(v) => { a = a + v; }, Err(e) => { a = a + 1000; } }
    match (r(300)) { Ok(v) => { a = a + v; }, Err(e) => { a = a + e.len(); } }
    return a;
}
`

const lambdaTryUnannotatedSrc = lambdaTryHelpers + `function main(): i32 {
    var f = (x: i32) => { var y: i32 = g(x)?; return Some(y + 10); };
    var r = (x: i32) => { var y: i32 = h(x)?; return Ok(y + 1); };
` + lambdaTryBody

const lambdaTryAnnotatedSrc = lambdaTryHelpers + `function main(): i32 {
    var f: (i32) => Option[i32] = (x: i32) => { var y: i32 = g(x)?; return Some(y + 10); };
    var r: (i32) => Result[i32, string] = (x: i32) => { var y: i32 = h(x)?; return Ok(y + 1); };
` + lambdaTryBody

// 12 (Some(2) + 10), then +100 for g(500)'s None, +7 for Ok(6) + 1, +3 for "big".
const lambdaTryWant = 122

func TestLambdaTryOpPropagatesToTheLambda(t *testing.T) {
	for _, src := range []struct{ name, text string }{
		{"unannotated", lambdaTryUnannotatedSrc},
		{"annotated", lambdaTryAnnotatedSrc},
	} {
		t.Run(src.name+"/x86-64", func(t *testing.T) {
			out, code := compileAndRunX86Native(t, src.text)
			if code != lambdaTryWant {
				t.Errorf("exit %d, want %d\n%s", code, lambdaTryWant, strings.TrimSpace(out))
			}
		})
		t.Run(src.name+"/arm64", func(t *testing.T) {
			out, code := compileAndRunArm64FreeOn(t, src.text)
			if code != lambdaTryWant {
				t.Errorf("exit %d, want %d\n%s", code, lambdaTryWant, strings.TrimSpace(out))
			}
		})
		t.Run(src.name+"/wasm", func(t *testing.T) {
			if code := runWasm(t, src.text); code != lambdaTryWant {
				t.Errorf("exit %d, want %d", code, lambdaTryWant)
			}
		})
	}
}

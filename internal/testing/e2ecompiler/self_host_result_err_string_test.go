package e2ecompiler

import (
	"strings"
	"testing"
)

// An unmatched Result local whose Err payload is a string is released whole on
// the AST lowering (#10439): the Err string with it when proven fresh, and the
// box when the Ok payload is a scalar, which no class claimed before.

const resultErrLoop = `import "std/i32";
import "std/result";
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
BODY
        i = i + 1;
    }
    return n;
}
`

func resultErrLoopSrc(body string) string {
	return strings.Replace(resultErrLoop, "BODY", body, 1)
}

var resultErrStringRows = []leakRow{
	{"string_ok", resultErrLoopSrc(`        let q: Result[string, string] = Err((i + 1000).to_string());
        n = (n + q.unwrap_or("xyz").len()) % 101;`), true},
	{"scalar_ok", resultErrLoopSrc(`        let q: Result[i32, string] = Err((i + 1000).to_string());
        n = (n + q.unwrap_or(3)) % 101;`), true},
	{"bool_ok", resultErrLoopSrc(`        let q: Result[boolean, string] = Err((i + 1000).to_string());
        if (q.unwrap_or(true)) { n = n + 1; }`), true},
	{"scalar_ok_built_ok", resultErrLoopSrc(`        let q: Result[i32, string] = Ok(i);
        n = (n + q.unwrap_or(3)) % 101;`), true},
	{"unused", `function main(): i32 {
    let q: Result[i32, string] = Err("a" + "b");
    return 7;
}
`, true},
	{"lent", `import "std/i32";
import "std/result";
function pick(q: Result[i32, string]): i32 { return q.unwrap_or(3); }
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let q: Result[i32, string] = Err((i + 1000).to_string());
        n = (n + pick(q)) % 101;
        i = i + 1;
    }
    return n;
}
`, true},
	// The callee leaks one block per `return Err(x.to_string())` (#10628).
	{"producer", `import "std/i32";
import "std/result";
function parse(i: i32): Result[i32, string] {
    if (i % 2 == 0) { return Ok(i); }
    return Err((i + 1000).to_string());
}
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let q: Result[i32, string] = parse(i);
        n = (n + q.unwrap_or(3)) % 101;
        i = i + 1;
    }
    return n;
}
`, false},
	// The result of `and` wraps r's Err string uncounted and outlives r, so r
	// must not release it.
	{"result_outlives", `import "std/i32";
import "std/result";
function main(): i32 {
    let s: Result[string, string] = Ok("x");
    let i: i32 = 0;
    while (i < 3) {
        let r: Result[i32, string] = Err((i + 100).to_string());
        if (i == 0) { s = r.and(Ok("vw")); }
        i = i + 1;
    }
    match (s) { Ok(v) => { return v.len(); }, Err(e) => { return e.len() + 10; } }
    return 0;
}
`, false},
	// `or` returns r's own Ok string uncounted, and s outlives r (#10439):
	// on main r's release freed the string under s (wasm answered 20, not 23).
	{"ok_result_outlives", `import "std/i32";
import "std/result";
function main(): i32 {
    let s: Result[string, string] = Err("x");
    let i: i32 = 0;
    while (i < 3) {
        let r: Result[string, string] = Ok((i + 100).to_string());
        if (i == 0) { s = r.or(Err("z")); }
        i = i + 1;
    }
    match (s) { Ok(v) => { return v.len() + 20; }, Err(e) => { return e.len(); } }
    return 0;
}
`, false},
	// The same escape through a call chain (#10660). The strings share a length
	// so a freed buffer is reused, turning a use-after-free into a wrong answer.
	{"ok_result_outlives_chained", `import "std/i32";
import "std/result";
function main(): i32 {
    let s: string = "";
    let i: i32 = 0;
    while (i < 100) {
        let q: Result[string, string] = Ok("A0" + (i + 100000).to_string() + "x".repeat(24));
        if (i == 0) { s = q.or(Ok("y")).unwrap_or("y"); }
        i = i + 1;
    }
    if (s.contains("100000")) { return 1; }
    return 2;
}
`, true},
	// The Err payload is a live local's string, so only the box is released.
	{"err_aliases_live", `import "std/i32";
import "std/result";
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let e: string = (i + 1000).to_string();
        let q: Result[i32, string] = Err(e);
        n = (n + q.unwrap_or(3) + e.len()) % 101;
        i = i + 1;
    }
    return n;
}
`, false},
}

func TestSelfHostResultErrStringX86_64(t *testing.T) {
	runLeakRowsX86_64(t, resultErrStringRows)
}

func TestSelfHostResultErrStringArm64(t *testing.T) {
	runLeakRowsArm64(t, resultErrStringRows)
}

func TestSelfHostResultErrStringWasm(t *testing.T) {
	runLeakRowsWasm(t, resultErrStringRows)
}

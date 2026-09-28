package e2eselfhost

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
    var n: i32 = 0;
    var i: i32 = 0;
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
	{"string_ok", resultErrLoopSrc(`        var q: Result[string, string] = Err((i + 1000).to_string());
        n = (n + q.unwrap_or("xyz").len()) % 101;`), true, [2]int64{}},
	{"scalar_ok", resultErrLoopSrc(`        var q: Result[i32, string] = Err((i + 1000).to_string());
        n = (n + q.unwrap_or(3)) % 101;`), true, [2]int64{}},
	{"bool_ok", resultErrLoopSrc(`        var q: Result[boolean, string] = Err((i + 1000).to_string());
        if (q.unwrap_or(true)) { n = n + 1; }`), true, [2]int64{}},
	{"scalar_ok_built_ok", resultErrLoopSrc(`        var q: Result[i32, string] = Ok(i);
        n = (n + q.unwrap_or(3)) % 101;`), true, [2]int64{}},
	{"unused", `function main(): i32 {
    var q: Result[i32, string] = Err("a" + "b");
    return 7;
}
`, true, [2]int64{}},
	{"lent", `import "std/i32";
import "std/result";
function pick(q: Result[i32, string]): i32 { return q.unwrap_or(3); }
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var q: Result[i32, string] = Err((i + 1000).to_string());
        n = (n + pick(q)) % 101;
        i = i + 1;
    }
    return n;
}
`, true, [2]int64{}},
	// The callee leaks one block per `return Err(x.to_string())` (#10628).
	{"producer", `import "std/i32";
import "std/result";
function parse(i: i32): Result[i32, string] {
    if (i % 2 == 0) { return Ok(i); }
    return Err((i + 1000).to_string());
}
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var q: Result[i32, string] = parse(i);
        n = (n + q.unwrap_or(3)) % 101;
        i = i + 1;
    }
    return n;
}
`, false, [2]int64{200, 150}},
	// The result of `and` wraps r's Err string uncounted and outlives r, so r
	// must not release it.
	{"result_outlives", `import "std/i32";
import "std/result";
function main(): i32 {
    var s: Result[string, string] = Ok("x");
    var i: i32 = 0;
    while (i < 3) {
        var r: Result[i32, string] = Err((i + 100).to_string());
        if (i == 0) { s = r.and(Ok("vw")); }
        i = i + 1;
    }
    match (s) { Ok(v) => { return v.len(); }, Err(e) => { return e.len() + 10; } }
    return 0;
}
`, false, [2]int64{12, 3}},
	// `or` returns r's own Ok string uncounted, and s outlives r (#10439):
	// on main r's release freed the string under s (wasm answered 20, not 23).
	{"ok_result_outlives", `import "std/i32";
import "std/result";
function main(): i32 {
    var s: Result[string, string] = Err("x");
    var i: i32 = 0;
    while (i < 3) {
        var r: Result[string, string] = Ok((i + 100).to_string());
        if (i == 0) { s = r.or(Err("z")); }
        i = i + 1;
    }
    match (s) { Ok(v) => { return v.len() + 20; }, Err(e) => { return e.len(); } }
    return 0;
}
`, false, [2]int64{12, 3}},
	// The Err payload is a live local's string, so only the box is released.
	{"err_aliases_live", `import "std/i32";
import "std/result";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var e: string = (i + 1000).to_string();
        var q: Result[i32, string] = Err(e);
        n = (n + q.unwrap_or(3) + e.len()) % 101;
        i = i + 1;
    }
    return n;
}
`, false, [2]int64{300, 200}},
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

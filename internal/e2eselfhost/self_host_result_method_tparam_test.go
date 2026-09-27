package e2eselfhost

import (
	"strings"
	"testing"
)

// A payload read back through a generic Option/Result method, in the AST
// lowering (FERN_SEM_IR=), is read at its instantiated type: the checker binds
// the receiver's type arguments and the method's own (`and[U]` from `Ok("vw")`)
// into the call's tag, so `.len()` and `match` read a string as a string
// (#10014). The census is not pinned: the AST lowering leaks these boxes
// (#10388).
var resultMethodTParamCases = []struct {
	name string
	src  string
	want int
}{
	{"and_string", `import "std/result";
function main(): i32 {
    var r: Result[i32, string] = Ok(1);
    var s: Result[string, string] = r.and(Ok("vw"));
    return s.unwrap_or("").len();
}
`, 2},
	{"and_string_loop", `import "std/result";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var r: Result[i32, string] = Ok(i);
        var s: Result[string, string] = r.and(Ok("v" + "w"));
        n = n + s.unwrap_or("").len();
        i = i + 1;
    }
    return n % 101;
}
`, 400 % 101},
	{"and_i32_loop", `import "std/result";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var r: Result[string, string] = Ok("a" + "b");
        var s: Result[i32, string] = r.and(Ok(i));
        n = n + s.unwrap_or(0);
        i = i + 1;
    }
    return n % 101;
}
`, 19900 % 101},
	{"and_err", `import "std/result";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var r: Result[i32, string] = Err("e" + "rr");
        var s: Result[string, string] = r.and(Ok("vw"));
        match (s) { Ok(v) => { n = n + 100; }, Err(e) => { n = n + e.len(); } }
        i = i + 1;
    }
    return n % 101;
}
`, 150 % 101},
	{"or_string", `import "std/result";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var r: Result[string, string] = Err("e");
        var s: Result[string, string] = r.or(Ok("x" + "yz"));
        n = n + s.unwrap_or("").len();
        i = i + 1;
    }
    return n % 101;
}
`, 150 % 101},
	{"unwrap_or_string", `import "std/result";
function main(): i32 {
    var s: Result[string, string] = Ok("x" + "yz");
    return s.unwrap_or("").len();
}
`, 3},
	{"map_or_string", `import "std/result";
function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var r: Result[string, string] = Ok("a" + "bcd");
        n = n + r.map_or(0, slen);
        i = i + 1;
    }
    return n % 101;
}
`, 200 % 101},
	{"flatten_string", `import "std/result";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var inner: Result[string, string] = Ok("p" + "q");
        var r: Result[Result[string, string], string] = Ok(inner);
        n = n + r.flatten().unwrap_or("").len();
        i = i + 1;
    }
    return n % 101;
}
`, 100 % 101},
	{"option_and_string", `import "std/option";
function main(): i32 {
    var o: Option[i32] = Some(4);
    var s: Option[string] = o.and(Some("abcde"));
    return s.unwrap_or("").len();
}
`, 5},
	{"generic_fn_result_matched", `function either[T, E](r: Result[T, E], other: Result[T, E]): Result[T, E] {
    match (r) { Ok(x) => { return Ok(x); }, Err(e) => { return other; } }
}
function main(): i32 {
    var r: Result[string, string] = Err("e");
    var s: Result[string, string] = either(r, Ok("x" + "yz"));
    match (s) { Ok(v) => { return v.len(); }, Err(e) => { return 50; } }
}
`, 3},
}

func TestSelfHostResultMethodTParam(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range resultMethodTParamCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_SEM_IR=")
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if target != "x86-64-linux" {
					return
				}
				stderr, exit = cli.exitOf(t, tc.src, target, "FERN_SANITIZE=1", "FERN_SEM_IR=")
				if exit != tc.want || strings.Contains(stderr, "use-after-free") || strings.Contains(stderr, "over-release") {
					t.Fatalf("sanitize: exit = %d, want %d, with no use-after-free or over-release\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

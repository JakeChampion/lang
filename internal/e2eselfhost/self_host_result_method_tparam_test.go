package e2eselfhost

import (
	"strings"
	"testing"
)

// A payload read back through a generic Option/Result method is read at its
// instantiated type: the receiver's type arguments and the method's own
// (`and[U]` from `Ok("vw")`) bind, so `.len()` and `match` read a string as a
// string (#10014). Every row balances (#10388), and every answer is
// interpreter-confirmed.
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
	{"scalar_recv_unwrap_or", `import "std/result";
function main(): i32 {
    var o: Result[i32, i32] = Ok(3);
    return o.unwrap_or(0);
}
`, 3},
	{"scalar_recv_is_ok", `import "std/result";
function main(): i32 {
    var o: Result[i32, i32] = Err(4);
    if (o.is_ok()) { return 1; }
    return 2;
}
`, 2},
	{"option_scalar_recv", `import "std/option";
function main(): i32 {
    var o: Option[i32] = Some(3);
    if (o.is_some()) { return o.unwrap_or(0); }
    return 1;
}
`, 3},
	{"scalar_recv_loop", `import "std/result";
import "std/option";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var o: Option[i32] = Some(i);
        var r: Result[i32, string] = Ok(i + 1);
        if (o.is_some() && r.is_ok()) { n = (n + o.unwrap_or(0) + r.unwrap_or(0)) % 101; }
        i = i + 1;
    }
    return n;
}
`, 4},
	{"scalar_rebound_recv", `import "std/option";
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    var o: Option[i32] = None;
    while (i < 200) {
        o = Some(i);
        n = (n + o.unwrap_or(0)) % 101;
        i = i + 1;
    }
    return n + o.unwrap_or(0) % 7;
}
`, 6},
	{"mixed_unused", `function main(): i32 {
    var r: Result[i32, string] = Ok(3);
    return 7;
}
`, 7},
	{"mixed_borrowed_arg", `@noinline
function f(o: Result[i32, string]): i32 {
    match (o) { Ok(v) => { return v; }, Err(e) => { return e.len(); } }
}
function main(): i32 {
    var r: Result[i32, string] = Ok(3);
    return f(r);
}
`, 3},
	{"fresh_ok_string_arg", `@noinline
function f(o: Result[string, string]): i32 {
    match (o) { Ok(v) => { return v.len(); }, Err(e) => { return 50; } }
}
function main(): i32 { return f(Ok("x" + "yz")); }
`, 3},
	{"fresh_some_scalar_arg", `@noinline
function f(o: Option[i32]): i32 {
    match (o) { Some(v) => { return v; }, None => { return 50; } }
}
function main(): i32 { return f(Some(4)) + f(None); }
`, 54},
	{"fresh_arg_loop", `import "std/i32";
import "std/result";
@noinline
function f(o: Result[string, string]): i32 {
    match (o) { Ok(v) => { return v.len(); }, Err(e) => { return 50; } }
}
@noinline
function g(o: Option[i32], r: Result[i32, i32]): i32 {
    match (o) { Some(v) => { return v + r.unwrap_or(1); }, None => { return 0; } }
}
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        n = (n + f(Ok((i + 1000).to_string())) + f(Err("e" + i.to_string()))) % 101;
        n = (n + g(Some(i), Ok(i)) + g(None, Err(2))) % 101;
        i = i + 1;
    }
    return n;
}
`, 100},
}

func TestSelfHostResultMethodTParam(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range resultMethodTParamCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1")
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
				if target != "x86-64-linux" {
					return
				}
				stderr, exit = cli.exitOf(t, tc.src, target, "FERN_SANITIZE=1")
				if exit != tc.want || strings.Contains(stderr, "use-after-free") || strings.Contains(stderr, "over-release") {
					t.Fatalf("sanitize: exit = %d, want %d, with no use-after-free or over-release\n%s", exit, tc.want, stderr)
				}
				if strings.Contains(stderr, "fern-sanitizer:") {
					t.Fatalf("sanitize: want the sanitizer silent\n%s", stderr)
				}
			})
		}
	}
}

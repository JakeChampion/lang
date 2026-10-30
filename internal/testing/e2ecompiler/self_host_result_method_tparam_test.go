package e2ecompiler

import (
	"strings"
	"testing"
)

var resultMethodTParamCases = []struct {
	name string
	src  string
	want int
}{
	{"and_string", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let r: Result[i32, string] = Ok(1);
    let s: Result[string, string] = r.and(Ok("vw"));
    return s.unwrap_or("").len();
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 2},
	{"and_string_loop", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let r: Result[i32, string] = Ok(i);
        let s: Result[string, string] = r.and(Ok("v" + "w"));
        n = n + s.unwrap_or("").len();
        i = i + 1;
    }
    return n % 101;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 400 % 101},
	{"and_i32_loop", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let r: Result[string, string] = Ok("a" + "b");
        let s: Result[i32, string] = r.and(Ok(i));
        n = n + s.unwrap_or(0);
        i = i + 1;
    }
    return n % 101;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 19900 % 101},
	{"and_err", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let r: Result[i32, string] = Err("e" + "rr");
        let s: Result[string, string] = r.and(Ok("vw"));
        match (s) { Ok(v) => { n = n + 100; }, Err(e) => { n = n + e.len(); } }
        i = i + 1;
    }
    return n % 101;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 150 % 101},
	{"or_string", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let r: Result[string, string] = Err("e");
        let s: Result[string, string] = r.or(Ok("x" + "yz"));
        n = n + s.unwrap_or("").len();
        i = i + 1;
    }
    return n % 101;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 150 % 101},
	{"unwrap_or_string", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let s: Result[string, string] = Ok("x" + "yz");
    return s.unwrap_or("").len();
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 3},
	{"map_or_string", `import "std/result";
function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let r: Result[string, string] = Ok("abc" + (i % 10).to_string());
        n = n + r.map_or(0, slen);
        i = i + 1;
    }
    return n % 101;
}
`, 200 % 101},
	{"flatten_string", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let inner: Result[string, string] = Ok("p" + "q");
        let r: Result[Result[string, string], string] = Ok(inner);
        n = n + r.flatten().unwrap_or("").len();
        i = i + 1;
    }
    return n % 101;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 100 % 101},
	{"option_and_string", `import "std/string";
import "std/option";
@noinline
function probe(): i32 {
    let o: Option[i32] = Some(4);
    let s: Option[string] = o.and(Some("abcde"));
    return s.unwrap_or("").len();
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 5},
	{"generic_fn_result_matched", `import "std/string";
function either[T, E](r: Result[T, E], other: Result[T, E]): Result[T, E] {
    match (r) { Ok(x) => { return Ok(x); }, Err(e) => { return other; } }
}
@noinline
function probe(): i32 {
    let r: Result[string, string] = Err("e");
    let s: Result[string, string] = either(r, Ok("x" + "yz"));
    match (s) { Ok(v) => { return v.len(); }, Err(e) => { return 50; } }
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 3},
	{"scalar_recv_unwrap_or", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let o: Result[i32, i32] = Ok(3);
    return o.unwrap_or(0);
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 3},
	{"scalar_recv_is_ok", `import "std/string";
import "std/result";
@noinline
function probe(): i32 {
    let o: Result[i32, i32] = Err(4);
    if (o.is_ok()) { return 1; }
    return 2;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 2},
	{"option_scalar_recv", `import "std/string";
import "std/option";
@noinline
function probe(): i32 {
    let o: Option[i32] = Some(3);
    if (o.is_some()) { return o.unwrap_or(0); }
    return 1;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 3},
	{"scalar_recv_loop", `import "std/string";
import "std/result";
import "std/option";
@noinline
function probe(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let o: Option[i32] = Some(i);
        let r: Result[i32, string] = Ok(i + 1);
        if (o.is_some() && r.is_ok()) { n = (n + o.unwrap_or(0) + r.unwrap_or(0)) % 101; }
        i = i + 1;
    }
    return n;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 4},
	{"scalar_rebound_recv", `import "std/string";
import "std/option";
@noinline
function probe(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    let o: Option[i32] = None;
    while (i < 200) {
        o = Some(i);
        n = (n + o.unwrap_or(0)) % 101;
        i = i + 1;
    }
    return n + o.unwrap_or(0) % 7;
}
@noinline
function keeps_heap_work(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let value = probe();
    if (data.len() != n || data[0] != first) { return 99; }
    return value;
}
function main(): i32 { return keeps_heap_work(); }
`, 6},
	{"mixed_unused", `@noinline function keep(r: Result[i32, string]): i32 { return 0; }
function main(): i32 {
    let r: Result[i32, string] = Ok(3);
    return 7 + keep(r);
}
`, 7},
	{"mixed_borrowed_arg", `@noinline
function f(o: Result[i32, string]): i32 {
    match (o) { Ok(v) => { return v; }, Err(e) => { return e.len(); } }
}
function main(): i32 {
    let r: Result[i32, string] = Ok(3);
    return f(r);
}
`, 3},
	{"fresh_ok_string_arg", `@noinline
function f(o: Result[string, string]): i32 {
    match (o) { Ok(v) => { return v.len(); }, Err(e) => { return 50; } }
}
function main(): i32 { return f(Ok("x" + "yz")); }
`, 3},
	{"fresh_some_scalar_arg", `import "std/string";
@noinline
function f(o: Option[i32]): i32 {
    match (o) { Some(v) => { return v; }, None => { return 50; } }
}
function main(): i32 {
    let data = args()[0].bytes();
    let n = data.len();
    if (n == 0) { return 99; }
    let first = data[0];
    let last = data[n - 1];
    let value = f(Some(n + 4)) + f(None) - n;
    if (data.len() != n || data[0] != first || data[n - 1] != last) { return 99; }
    return value;
}
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
    let n: i32 = 0;
    let i: i32 = 0;
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

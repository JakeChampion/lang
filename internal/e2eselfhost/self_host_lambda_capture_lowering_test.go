package e2eselfhost

import (
	"strings"
	"testing"
)

// Lambda and closure programs the native compiler runs and the self-host CLI
// refused in its typed lowering (FERN_SEM_IR) or answered wrongly. Each want
// is the native interpreter's exit code. Each case goes through the production
// CLI, because the refusals were in passes the emit drivers do not run.
var lambdaCaptureLoweringCases = []struct {
	name, src string
	want      int
}{
	// `?` in an unannotated lambda: `return Ok(y + 1)` names no error type, so
	// the lambda's result comes from unifying it with the `?` operand's
	// Result[i32, string] (#9515). The annotated spelling is the control.
	{"try-op-unannotated-lambda", lambdaTryHelpersSrc + `function main(): i32 {
    var f = (x: i32) => { var y: i32 = g(x)?; return Some(y + 10); };
    var r = (x: i32) => { var y: i32 = h(x)?; return Ok(y + 1); };
` + lambdaTryBodySrc, 122},
	{"try-op-annotated-lambda-control", lambdaTryHelpersSrc + `function main(): i32 {
    var f: (i32) => Option[i32] = (x: i32) => { var y: i32 = g(x)?; return Some(y + 10); };
    var r: (i32) => Result[i32, string] = (x: i32) => { var y: i32 = h(x)?; return Ok(y + 1); };
` + lambdaTryBodySrc, 122},

	// A written instantiation binds a type parameter no argument spells.
	{"generic-nullary-written-type-arg", `function pick[T](): T[] {
    var out: T[] = [];
    return out;
}
function main(): i32 {
    var xs = pick[i32]();
    return xs.len();
}`, 0},
	{"generic-returns-lambda", `function makeId[T](): (T) => T {
    return (x: T): T => { return x; };
}
function main(): i32 {
    var f = makeId[i32]();
    return f(42);
}`, 42},
	{"generic-returns-lambda-called-directly", `function makeId[T](): (T) => T {
    return (x: T): T => { return x; };
}
function main(): i32 {
    return makeId[i32]()(42) - 42;
}`, 0},

	// A view of closures keeps the closures' signature: `[(i32) => i32]`.
	{"view-of-closures-binding", `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var arr: ((i32) => i32)[] = [makeAdder(1), makeAdder(2), makeAdder(3)];
    var sl: [(i32) => i32] = arr[1:3];
    return sl[0](10) + sl[1](10);
}`, 25},
	{"view-of-closures-param", `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function apply_all(fs: [(i32) => i32], x: i32): i32 {
    var total: i32 = 0;
    for f in fs {
        total = total + f(x);
    }
    return total;
}
function main(): i32 {
    var arr: ((i32) => i32)[] = [makeAdder(1), makeAdder(2), makeAdder(3)];
    return apply_all(arr[1:3], 10) - 25;
}`, 0},

	// A capture written by a lambda nested inside the capturing one is boxed
	// in the frame that declares it, so the write reaches that frame.
	{"nested-lambda-writes-capture-returned", `function main(): i32 {
    var seen: i32 = 0;
    var pick: () => (i32) => void = (): (i32) => void => {
        return (n: i32): void => { seen = n; };
    };
    pick()(7);
    if (seen != 7) { return 1; }
    return 0;
}`, 0},
	{"nested-lambda-writes-capture-direct", `function main(): i32 {
    var seen: i32 = 0;
    var outer = (): i32 => {
        var inner = (n: i32): void => { seen = n; };
        inner(7);
        return 0;
    };
    outer();
    if (seen != 7) { return 1; }
    return 0;
}`, 0},
	// A lambda writing a captured parameter: the parameter's cell is built on
	// entry.
	{"lambda-writes-captured-param", `function bump(x: i32): i32 {
    var g = (): void => { x = x + 1; };
    g();
    g();
    return x;
}
function main(): i32 {
    return bump(5) - 7;
}`, 0},

	// An 8-byte capture the closure writes shares one cell with the frame that
	// declared it rather than being snapshotted per call.
	{"mutable-captured-i64", `function makeCounter(): () => i64 {
    var count: i64 = 0i64;
    function tick(): i64 {
        count = count + 1i64;
        return count;
    }
    return tick;
}
function main(): i32 {
    var c = makeCounter();
    var a: i64 = c();
    var b: i64 = c();
    if (a != 1i64) { return 1; }
    if (b != 2i64) { return 2; }
    return 0;
}`, 0},
	{"mutable-captured-i64-param-and-nested", `function makeCounter(start: i64): () => i64 {
    function tick(): i64 {
        start = start + 1i64;
        return start;
    }
    return tick;
}
function main(): i32 {
    var total: i64 = 0i64;
    var outer = (): () => i64 => {
        return (): i64 => { total = total + 5i64; return total; };
    };
    var f = outer();
    f();
    f();
    var c = makeCounter(10i64);
    c();
    var b: i64 = c();
    if (total != 10i64) { return 1; }
    if (b != 12i64) { return 2; }
    return 0;
}`, 0},
	// The closure is called only inside another function; the declaring frame
	// reads the count afterwards.
	{"mutable-captured-i64-called-by-callee", `function apply(f: (i64) => i64, x: i64): i64 { return f(x); }
function main(): i32 {
    var count: i64 = 0i64;
    var g = (x: i64): i64 => { count = count + 1i64; return x; };
    var a: i64 = apply(g, 5i64);
    var b: i64 = apply(g, 6i64);
    if (count != 2i64) { return 40 + (count as i32); }
    return 0;
}`, 0},
}

const lambdaTryHelpersSrc = `function g(v: i32): Option[i32] {
    if (v < 100) { return Some(v + 1); }
    return None;
}
function h(v: i32): Result[i32, string] {
    if (v < 100) { return Ok(v * 2); }
    return Err("big");
}
`

const lambdaTryBodySrc = `    var a: i32 = 0;
    match (f(1)) { Some(v) => { a = v; }, None => { a = 99; } }
    match (f(500)) { Some(v) => { a = a + v; }, None => { a = a + 100; } }
    match (r(3)) { Ok(v) => { a = a + v; }, Err(e) => { a = a + 1000; } }
    match (r(300)) { Ok(v) => { a = a + v; }, Err(e) => { a = a + e.len(); } }
    return a;
}
`

func TestSelfHostLambdaCaptureLowering(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range lambdaCaptureLoweringCases {
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.src, target)
				if code != tc.want {
					t.Errorf("exit %d, want %d\n%s", code, tc.want, strings.TrimSpace(stderr))
				}
			})
		}
	}
}

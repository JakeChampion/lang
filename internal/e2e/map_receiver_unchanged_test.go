package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A Map mutator returns a new map and leaves its receiver as it was when the
// receiver is read again or only borrowed (#9834, #8764): a binding, a lent
// parameter, a struct field, and a result used as a temporary. The receiver
// is retained across the call so the copy-on-write copies; each case also
// pins a balanced leak census, since a forced copy nobody releases would
// trade the aliasing bug for a leak.
var mapReceiverUnchangedCases = []struct {
	name string
	src  string
	want int
}{
	{"bindings", `import "core/map";
function grown(m: Map[i32, i32], k: i32): Map[i32, i32] { return m.insert(k, k * 3); }
function threaded(m: Map[i32, i32], n: i32): Map[i32, i32] {
    var i: i32 = 0;
    while (i < n) { m = m.insert(100 + i, i); i = i + 1; }
    return m;
}
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    var n: Map[i32, i32] = m.insert(2, 20);
    var g: Map[i32, i32] = grown(n, 7);
    var t: Map[i32, i32] = threaded(n, 3);
    var (rest, had) = n.without(2);
    var c: Map[i32, i32] = n.cleared();
    var acc: Map[i32, i32] = map_new(8);
    var k: i32 = 0;
    while (k < 5) { var next: Map[i32, i32] = acc.insert(k, k); acc = next; k = k + 1; }
    if (!had || g.get_or(7, -1) != 21 || n.get_or(2, -1) != 20) { return 1; }
    return m.len() + n.len() * 2 + g.len() * 4 + t.len() * 8 + rest.len() * 16 + c.len() * 32 + acc.len();
}`, 78},
	{"temporaries", `import "core/map";
function size(m: Map[i32, i32]): i32 { return m.len(); }
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    var a: i32 = size(m.insert(2, 20));
    var b: i32 = m.insert(3, 30).insert(4, 40).len();
    var c: i32 = m.cleared().len();
    var d: boolean = m.without(1).1;
    return m.len() + a * 2 + b * 4 + c * 8 + (if (d) { 16 } else { 0 });
}`, 33},
	{"fields", `import "core/map";
struct Box { m: Map[i32, i32] }
function peek(b: Box): Map[i32, i32] { return b.m.insert(9, 9); }
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    var b: Box = Box { m: m };
    var n: Map[i32, i32] = b.m.insert(2, 20);
    var p: Map[i32, i32] = peek(b);
    var (rest, had) = b.m.without(1);
    var c: Map[i32, i32] = b.m.cleared();
    var built: Box = Box { m: b.m.insert(3, 30) };
    return b.m.len() + n.len() * 2 + p.len() * 4 + rest.len() * 8 + c.len() * 16 + built.m.len() * 32;
}`, 77},
	{"cleared-string-keys", `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(16);
    var a: string = "alpha" + "-longer-than-sso";
    m = m.insert(a, 7);
    m = m.insert("beta" + "-longer-than-sso", 9);
    var c: Map[string, i32] = m.cleared();
    var got: i32 = 0 - 1;
    match (m.get(a)) { Some(v) => { got = v; }, None => { got = 0 - 2; } }
    return c.len() * 100 + m.len() * 10 + got;
}`, 27},
}

func checkMapReceiverCensus(t *testing.T, want int, stderr string, exit int) {
	t.Helper()
	if exit != want {
		t.Fatalf("exit %d, want %d; stderr: %s", exit, want, stderr)
	}
	a, f, live := parseLeakCheckLine(t, stderr)
	if a != f || live != 0 {
		t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", a, f, live)
	}
}

func TestMapReceiverIsUnchanged(t *testing.T) {
	for _, tc := range mapReceiverUnchangedCases {
		t.Run(tc.name+"/interp", func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
		})
		t.Run(tc.name+"/x86-64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckX86_64(t, tc.src)
			checkMapReceiverCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/arm64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckArm64(t, tc.src)
			checkMapReceiverCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/wasm", func(t *testing.T) {
			stdout, stderr, exit := runLeakCheckWasm(t, tc.src, false)
			if exit != 0 || strings.TrimSpace(stdout) != strconv.Itoa(tc.want) {
				t.Fatalf("exit %d stdout %q, want exit 0 printing %d; stderr: %s", exit, stdout, tc.want, stderr)
			}
			a, f, live := parseWasmLeakCheckLine(t, stderr)
			if a != f || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", a, f, live)
			}
		})
	}
}

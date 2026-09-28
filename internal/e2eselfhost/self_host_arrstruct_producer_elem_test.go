package e2eselfhost

import (
	"os/exec"
	"testing"
)

// An array of structs whose elements come from a strict-fresh struct producer
// (`hs = hs.append(hold(xs))`, `hs = hs.with(i, hold(xs))`, `[hold(a), hold(b)]`)
// earns the element walk the literal-built form does (#10582). Without it the
// AST lowering freed only the buffer and stranded every element box and its
// fields. The guard rows must stay refused without over-releasing: a producer
// that hands back a shared struct, and an element or nested field that outlives
// the array. Their `refused` census pins the shallow fallback on the AST leg.
var arrStructProducerElemCases = []struct {
	name     string
	src      string
	want     int
	balanced bool
	refused  [2]int64 // allocs, frees on the AST leg
}{
	{"arrfield_append", `struct H { xs: i32[] }
@noinline function hold(xs: i32[]): H { return H { xs: xs }; }
function main(): i32 {
    var hs: H[] = [];
    var i: i32 = 0;
    while (i < 5) { hs = hs.append(hold([i])); i = i + 1; }
    return hs.len();
}
`, 5, true, [2]int64{}},
	{"arrfield_literal", `struct H { xs: i32[] }
@noinline function hold(xs: i32[]): H { return H { xs: xs }; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5) {
        var hs: H[] = [hold([i]), hold([i, 1])];
        acc = acc + hs[1].xs.len();
        i = i + 1;
    }
    return acc;
}
`, 10, true, [2]int64{}},
	{"strfield_append", `struct L { tag: string, n: i32 }
@noinline function label(p: string, k: i32): L { return L { tag: p + "-suffix-long", n: k }; }
function main(): i32 {
    var prefix: string = "prefix-str";
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 3) {
        var ls: L[] = [];
        var i: i32 = 0;
        while (i < 4) { ls = ls.append(label(prefix, i)); i = i + 1; }
        acc = acc + ls[2].tag.len() + ls[3].n;
        r = r + 1;
    }
    return acc + prefix.len();
}
`, 85, true, [2]int64{}},
	{"nested_append", `struct In { xs: i32[] }
struct Out { inner: In, k: i32 }
@noinline function wrap(xs: i32[], k: i32): Out { return Out { inner: In { xs: xs }, k: k }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 3) {
        var os: Out[] = [];
        var i: i32 = 0;
        while (i < 4) { os = os.append(wrap([i, r], i)); i = i + 1; }
        acc = acc + os[3].k + os.len();
        r = r + 1;
    }
    return acc;
}
`, 21, true, [2]int64{}},
	// A read through a nested field (`os[i].inner.xs[j]`) is a borrow like the
	// one-link `os[i].xs[j]`, for producer and literal elements alike.
	{"nested_deep_read", `struct In { xs: i32[], k: i32 }
struct Out { inner: In, k: i32 }
@noinline function wrap(xs: i32[], k: i32): Out { return Out { inner: In { xs: xs, k: k }, k: k }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 3) {
        var os: Out[] = [];
        var ls: Out[] = [Out { inner: In { xs: [r, 5], k: 1 }, k: 2 }, Out { inner: In { xs: [r], k: 3 }, k: 4 }];
        var i: i32 = 0;
        while (i < 4) { os = os.append(wrap([i, r], i)); i = i + 1; }
        acc = acc + os[2].inner.xs[1] + os[3].inner.k + os[1].inner.xs.len() + os.len();
        acc = acc + ls[0].inner.xs[1] + ls[1].inner.k + ls[1].inner.xs.len();
        r = r + 1;
    }
    return acc;
}
`, 57, true, [2]int64{}},
	// `with` replaces an element, so the superseded producer element must be
	// released exactly once as well as the one that stays.
	{"arrfield_with", `struct H { xs: i32[] }
@noinline function hold(xs: i32[]): H { return H { xs: xs }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 3) {
        var hs: H[] = [hold([r]), hold([r, 1])];
        var i: i32 = 0;
        while (i < 3) { hs = hs.with(i % 2, hold([i, r, 7])); i = i + 1; }
        acc = acc + hs[0].xs.len() + hs[1].xs.len();
        r = r + 1;
    }
    return acc;
}
`, 18, true, [2]int64{}},
	{"guard_shared_producer", `struct H { xs: i32[] }
@noinline function pick(src: H[], k: i32): H { return src[k]; }
function main(): i32 {
    var src: H[] = [H { xs: [1, 2] }, H { xs: [3, 4] }];
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 3) {
        var hs: H[] = [];
        var i: i32 = 0;
        while (i < 4) { hs = hs.append(pick(src, i % 2)); i = i + 1; }
        acc = acc + hs[3].xs[1] + hs.len();
        r = r + 1;
    }
    return acc + src[0].xs[0] + src[1].xs[1];
}
`, 29, false, [2]int64{8, 4}},
	{"guard_elem_returned", `struct H { xs: i32[] }
@noinline function hold(xs: i32[]): H { return H { xs: xs }; }
@noinline function second(k: i32): H {
    var hs: H[] = [];
    var i: i32 = 0;
    while (i < 3) { hs = hs.append(hold([i, k])); i = i + 1; }
    return hs[1];
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 3) {
        var h: H = second(r);
        var junk: i32[] = [9, 9, 9];
        acc = acc + h.xs[0] + h.xs[1] + junk.len();
        r = r + 1;
    }
    return acc;
}
`, 15, false, [2]int64{24, 6}},
	{"guard_elem_kept", `struct H { xs: i32[] }
@noinline function hold(xs: i32[]): H { return H { xs: xs }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    var keep: H = hold([0]);
    while (r < 3) {
        var hs: H[] = [];
        var i: i32 = 0;
        while (i < 3) { hs = hs.append(hold([i, r])); i = i + 1; }
        keep = hs[2];
        var junk: i32[] = [9, 9, 9];
        acc = acc + keep.xs[1] + junk.len();
        r = r + 1;
    }
    return acc + keep.xs[0];
}
`, 14, false, [2]int64{26, 13}},
	{"guard_nested_field_kept", `struct In { xs: i32[], k: i32 }
struct Out { inner: In, k: i32 }
@noinline function wrap(xs: i32[], k: i32): Out { return Out { inner: In { xs: xs, k: k }, k: k }; }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    var kin: In = In { xs: [0], k: 0 };
    var kxs: i32[] = [0];
    while (r < 3) {
        var os: Out[] = [];
        var i: i32 = 0;
        while (i < 4) { os = os.append(wrap([i, r], i)); i = i + 1; }
        kin = os[2].inner;
        kxs = os[3].inner.xs;
        var junk: i32[] = [9, 9, 9];
        acc = acc + kin.xs[1] + kxs[0] + junk.len();
        r = r + 1;
    }
    return acc + kin.xs[0] + kxs[1];
}
`, 25, false, [2]int64{45, 7}},
}

var arrStructProducerElemLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
	{"ast_main", "FERN_SEM_IR_SKIP=main"},
	{"ast_callees", "FERN_SEM_IR_SKIP=hold,label,wrap,pick,second"},
}

func TestSelfHostArrStructProducerElemX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrStructProducerElemCases {
		src := writeMixedStructReleaseSrc(t, tc.name, tc.src)
		for _, lw := range arrStructProducerElemLowerings {
			balanced := tc.balanced || lw.name == "semantic"
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env), nil)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if balanced {
					assertBalancedCensus(t, stderr)
				} else if lw.name == "ast" {
					assertRefusedCensus(t, stderr, tc.refused)
				}
				stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env), nil)
				if exit != tc.want || forArrStructSanitizerFault(stderr, balanced) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer report\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostArrStructProducerElemWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range arrStructProducerElemCases {
		src := writeMixedStructReleaseSrc(t, tc.name, tc.src)
		for _, lw := range arrStructProducerElemLowerings {
			balanced := tc.balanced || lw.name == "semantic"
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env))
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if balanced {
					assertBalancedCensus(t, stderr)
				} else if lw.name == "ast" {
					assertRefusedCensus(t, stderr, tc.refused)
				}
			})
		}
	}
}

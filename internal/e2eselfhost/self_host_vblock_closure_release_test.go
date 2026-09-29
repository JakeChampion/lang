package e2eselfhost

import (
	"testing"
)

// A value block whose tail names an array local the block declared moves that
// local into the block's value (#10393): the variable the block initializes
// takes the local's count and element typing, and the local is left null. On
// the AST lowering the tail used to be retained into an untyped temp the
// binding never released, and an unannotated binding lost the element type, so
// `e[0].len()` over a string[] read the wrong word.
const vblockTailMoveSrc = `import "std/i32";
struct P { x: i32, y: i32 }
function mk(j: i32): i32[] { return { var q = [j, j * 2]; q = q.append(3); q }; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    var outer: i32[] = [0];
    while (j < 10) {
        var a = { var q = [j, j + 1]; q };
        var b: i32[] = { var q: i32[] = [j, 4]; q };
        var c: P[] = { var q = [P { x: 5, y: j }]; q };
        var d = { var q = [P { x: j, y: 2 }, P { x: 1, y: j }]; t = t + q[0].x; q };
        var e = { var q = ["a" + j.to_string(), "b"]; q };
        var f = { var q = [j]; q = q.append(2); q };
        var h = { var q = [j, 9]; if (j == 8) { break; } q };
        outer = { var q = [j, 5]; q };
        var m = mk(j);
        t = t + a[1] + a.len() + b[1] + c[0].y + d[1].y + e[0].len() + f[1] + h[1] + outer[1] + m[2];
        j = j + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed.
const vblockTailMoveWant = 41

// The move through a nested value block, on both lowerings (#10436).
const vblockNestedTailMoveSrc = `function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 10) {
        var d = { var z = { var q = [j, 7]; q }; z };
        var e = { var z: i32[] = { var q = [j, 3]; q = q.append(j); q }; z };
        t = t + d[1] + d[0] + e[2];
        j = j + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed.
const vblockNestedTailMoveWant = 59

// A closure box a local receives from a call or a value-position match is
// released when a loop rebinds the local, as the exit sweep releases the last
// one (#10392). The tuple-pattern match reads its scrutinee through a
// destructured tuple literal, whose box is now released once its scalar
// elements are read out.
const closureRebindReleaseSrc = `struct H { f: (i32) => i32, n: i32 }
function mk(b: i32): (i32) => i32 { return (x: i32) => x - b; }
function mks(s: string): (i32) => i32 { return (x: i32) => x + s.len(); }
function pick(i: i32): (i32) => i32 {
    var g: (i32) => i32 = mks("ab" + "");
    if (i > 2) { g = mks("abcd"); }
    return g;
}
function main(): i32 {
    var base: i32 = 5;
    var t: i32 = 0;
    var fns: ((i32) => i32)[] = [];
    var keep: (i32) => i32 = mk(100);
    var i: i32 = 0;
    while (i < 6) {
        var k: i32 = i;
        var g: (i32) => i32 = mk(i);
        var v: (i32) => i32 = (match (i % 3) { 0 => ((x: i32) => x - base), 1 => ((x: i32) => k), _ => ((x: i32) => x + 1) });
        var w: (i32) => i32 = (match ((i % 3, 0)) { (0, _) => ((x: i32) => x - base), (1, _) => ((x: i32) => k), _ => ((x: i32) => x + 1) });
        var h: H = H { f: g, n: i };
        var c: (i32) => i32 = (y: i32) => g(y) + 1;
        var p = pick(i);
        var (l, r) = (i % 2, i * 3);
        if (i == 3) { keep = g; }
        fns = fns.append(v);
        t = t + g(3) % 7 + v(3) % 7 + w(3) % 7 + h.f(10) + h.n + c(10) + p(1) + l + r;
        i = i + 1;
    }
    for f in fns { t = t + f(20); }
    return (t + keep(0)) % 101;
}
`

// Interpreter-confirmed.
const closureRebindReleaseWant = 76

// A defer inside a value block names an unannotated local of that block, which
// the desugar cannot lift, and replays at the function's exit, so the block
// keeps the local rather than moving it out (#10496).
const vblockDeferUnliftedSrc = `function main(): i32 {
    var r = 0;
    var c = 1;
    var d = { var q = [1, 2, 3]; if (c > 0) { defer { r = q[0] + q.len() } } q };
    var e = { var s = ["ab", "c"]; if (c > 5) { defer { r = r + s.len() } } s };
    return d[0] + r * 10 + e[0].len() * 50;
}
`

// Interpreter-confirmed: the replays run after the return value is read.
const vblockDeferUnliftedWant = 101

// An unannotated binding of an array of arrays that a value block yields, or
// that a call returns, takes the array-of-arrays class and its rows' credit
// (#10497): a block that captures nothing is lifted to a `__lam_N` call, and
// `c` is inlined because it captures `j`.
const vblockArrArrSrc = `function mk(j: i32): i32[][] { return [[j, 1], [2]]; }
function main(): i32 {
    var t = 0;
    var j = 0;
    while (j < 5) {
        var a = { var q = [[3, 2], [3, 4]]; q };
        var b = { var q = [["ab", "c"], ["def"]]; q };
        var c = { var q = [[j * 2, 5], [6]]; q };
        var m = mk(j);
        var f = { var q = [[1.5, 2.5]]; q };
        t = t + a[0][0] + a[1].len() + b[1][0].len() + c[0][0] + c[1].len() + m[0][0] + m[1].len() + (f[0][1] * 2.0) as i32;
        j = j + 1;
    }
    return t % 101;
}
`

// Interpreter-confirmed.
const vblockArrArrWant = 4

type vblockClosureLowering struct{ name, env string }

var (
	vblockClosureBoth = []vblockClosureLowering{{"semantic", "FERN_SEM_IR=1"}, {"ast", "FERN_SEM_IR="}}
	vblockClosureAST  = []vblockClosureLowering{{"ast", "FERN_SEM_IR="}}
)

var vblockClosureReleaseCases = []struct {
	name      string
	src       string
	want      int
	lowerings []vblockClosureLowering
}{
	{"vblock_tail_move", vblockTailMoveSrc, vblockTailMoveWant, vblockClosureBoth},
	{"vblock_nested_tail_move", vblockNestedTailMoveSrc, vblockNestedTailMoveWant, vblockClosureBoth},
	{"closure_rebind_release", closureRebindReleaseSrc, closureRebindReleaseWant, vblockClosureBoth},
	{"vblock_defer_unlifted_local", vblockDeferUnliftedSrc, vblockDeferUnliftedWant, vblockClosureBoth},
	{"vblock_arrarr", vblockArrArrSrc, vblockArrArrWant, vblockClosureBoth},
}

func TestSelfHostVblockClosureReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range vblockClosureReleaseCases {
		for _, lw := range tc.lowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
				stderr, exit = cli.exitOf(t, tc.src, "x86-64-linux", "FERN_SANITIZE=1", lw.env)
				if exit != tc.want || forArrStructSanitizerFault(stderr, true) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostVblockClosureReleaseArm64(t *testing.T) {
	checkVblockClosureRelease(t, "arm64-linux")
}

func TestSelfHostVblockClosureReleaseWasm(t *testing.T) {
	checkVblockClosureRelease(t, "wasm32-wasi")
}

func checkVblockClosureRelease(t *testing.T, target string) {
	cli := buildSelfHostCLI(t)
	for _, tc := range vblockClosureReleaseCases {
		for _, lw := range tc.lowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1", lw.env)
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}

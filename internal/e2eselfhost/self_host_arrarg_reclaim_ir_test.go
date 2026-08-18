package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrArgReclaimCases pin the #4365 stage-(b) borrowed-call-arg temp reclaim for
// ARRAYS: a fresh scalar-element array literal passed directly to a borrowing
// free function (`take([i, i+1])`) allocated a buffer per evaluation that
// nothing freed on the self-host IR path (native bounds the shape). The
// call lowering now stashes such an arg (discardable_scalar_arr_lit at a
// call_arg_borrowable position) and __fern_rc_dec's it right after the call —
// the array sibling of the #4355 string literal-arg box reclaim.
//
// The last four cases widen that stash: a fresh array a "STRARR:" / "ARR:"
// PRODUCER returned is the same temp one step removed, and a COUNTED-RETAIN
// param position admits one where borrowability cannot — the callee stores the
// argument, but every appearance of its parameter is a counted store or a
// non-retaining read, so this one dec nets it to a single owner either way.
// Pointer-element arrays ride only the counted-retain half: the release is the
// shallow buffer dec, which at a borrowable position would strand the element
// boxes.
//
// The consuming-callee case additionally pins the borrow-verdict soundness fix
// this slice required: a param that is REASSIGNED (`xs = xs.append(9)`) or used
// as an `.append` receiver (`var ys = xs.append(7)`) is never borrowable —
// append reuses/frees a unique receiver buffer on growth, so a caller-side
// release after such a callee double-freed (rc underflow; pre-existing for the
// Level-2 named-local precise drop, which shares the verdict).
var arrArgReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The core churn shape: a fresh scalar array literal arg, rebuilt per call.
	{"arrarg-churn-flat", `function take(xs: i32[]): i32 {
    return xs[0] + xs[1];
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { acc = (acc + take([w, w + 1])) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { acc = (acc + take([i, i + 1])) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Multi-arg frees, a forwarding callee (borrow via the interproc registry),
	// f64 elements, and a string-element literal (excluded from the reclaim —
	// leak-mode but CORRECT at detector zero, values exact).
	{"arrarg-multi-fwd-flat", `function take2(xs: i32[], ys: i32[]): i32 {
    return xs[0] + ys[1];
}
function fwd(xs: i32[]): i32 {
    return take2(xs, xs);
}
function fsum(xs: f64[]): i32 {
    if (xs[0] + xs[1] > 2.9) { return 3; }
    return 2;
}
function slen(xs: string[]): i32 {
    return xs[0].len() + xs.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) {
        acc = (acc + take2([w, w], [w, w + 1])) % 251;
        acc = (acc + fwd([w, w + 2])) % 251;
        acc = (acc + fsum([1.5, 1.5])) % 251;
        acc = (acc + slen(["ab", "c"])) % 251;
        w = w + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) {
        acc = (acc + take2([i, i], [i, i + 1])) % 251;
        acc = (acc + fwd([i, i + 2])) % 251;
        i = i + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    var f3: i32 = fsum([2.0, 1.0]);
    if (f3 != 3) { return 96; }
    var s3: i32 = slen(["xy", "z"]);
    if (s3 != 4) { return 95; }
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 94; }
    return 0;
}`, 0},
	// ESCAPE negative: the callee returns its arg — ownership moves out, the
	// param is non-borrowable, no post-call free (values exact, no dangle).
	{"arrarg-escape-safe", `function keep(xs: i32[]): i32[] {
    return xs;
}
function main(): i32 {
    var k: i32[] = keep([7, 8]);
    var acc: i32 = k[0] + k[1];
    if (__rc_underflow() != 0) { return 99; }
    return acc;
}`, 15},
	// CONSUMING-callee negative (the soundness fix): `xs = xs.append(9)` and the
	// unbound `var ys = xs.append(7)` both free a unique receiver buffer on
	// growth — the param must be non-borrowable so neither the call-arg temp
	// reclaim nor the Level-2 named-local precise drop releases the buffer a
	// second time. Was a pre-existing rc underflow for the named-local shape.
	{"arrarg-consuming-callee-safe", `function mut(xs: i32[]): i32 {
    xs = xs.append(9);
    return xs[2] + xs.len();
}
function mut2(xs: i32[]): i32 {
    var ys: i32[] = xs.append(7);
    return ys[2] + ys.len();
}
function main(): i32 {
    var r: i32 = mut([4, 5]);
    if (r != 12) { return 97; }
    var v: i32[] = [1, 2];
    var r2: i32 = mut2(v);
    if (r2 != 10) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// POINTER-ELEMENT producer arg at a COUNTED-RETAIN position (#6522): the
	// `deps_of(pre)` temp in `node(w(pre), deps_of(pre), n)` is a fresh string[]
	// nothing released — the stash arm admitted only scalar-element "ARR:"
	// producers, because a shallow buffer dec of a pointer-element array at a
	// BORROWABLE position would strand the element boxes. At a counted-retain
	// position the callee's struct construction has already retained the array,
	// so the same dec takes rc 2 -> 1 and the struct's own drop deep-frees the
	// elements it owns. Bounded high-water: 544 KB of leaked buffers over the
	// second 2000-iteration churn before, flat after.
	{"arrarg-strarr-producer-counted-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { var f: Node = node(w(pre), deps_of(pre), n); return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var w0: i32 = churn(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (w0 != x) { return 97; }
    return 0;
}`, 0},
	// The released temp's array must SURVIVE the call: the caller holds the
	// struct across three further array allocations, and deps_of's LENGTH varies
	// with n, so a buffer freed by an unbalanced dec and re-served from the
	// freelist reports the wrong element count. Value-exact over 2000 rounds.
	{"arrarg-strarr-counted-held-struct", `struct Held { deps: string[], n: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string, n: i32): string[] { var out: string[] = []; var i: i32 = 0; while (i < (n % 5) + 1) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(p: string[], n: i32): Held { return Held { deps: p, n: n }; }
function round(pre: string, n: i32): i32 {
    var h: Held = keep(deps_of(pre, n), n);
    var k1: string[] = deps_of(pre, n + 1);
    var k2: string[] = deps_of(pre, n + 3);
    var k3: string[] = deps_of(pre, n + 7);
    if (k1.len() < 0 || k2.len() < 0 || k3.len() < 0) { return 0; }
    return h.deps.len() + h.n - n;
}
function main(): i32 { var pre: string = "ab"; var i: i32 = 0; while (i < 2000) { if (round(pre, i) != (i % 5) + 1) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// A READ-ONLY callee is counted-retain too (nothing in the use vocabulary
	// refuses `p.len()`), and there the dec takes rc 1 -> 0 and frees the buffer
	// outright — 56 B/round recovered. The element boxes stay leaked: the release
	// is the shallow __fern_rc_dec, not the element walk. Sound, and the deep
	// free is a later slice — a borrowable callee may hand an element back out.
	{"arrarg-strarr-read-only-callee-safe", `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function count(p: string[]): i32 { return p.len() + p.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var i: i32 = 0; while (i < n) { if (count(deps_of(pre)) != 6) { return 97; } i = i + 1; } return 0; }
function main(): i32 { var a: i32 = churn(2000); if (a != 0) { return a; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// The tier now admits a STRUCT-RETURNING callee, so a SCALAR-array param
	// reaches the release through a shape the concrete-scalar-result guard used
	// to exclude: `esci` stores the argument in a local struct (a counted store)
	// and hands it out again through a FIELD READ into the struct it returns.
	// The field-read copy takes its own construction retain, so the returned
	// struct owns a counted reference and the caller's dec cannot strand it.
	{"arrarg-scalar-struct-ret-alias-safe", `struct SIn { a: i32[] }
struct SOut { b: i32[] }
function nums_of(n: i32): i32[] { var out: i32[] = []; var i: i32 = 0; while (i < 4) { out = out.append(n + i); i = i + 1; } return out; }
function esci(p: i32[]): SOut { var q: SIn = SIn { a: p }; return SOut { b: q.a }; }
function round(n: i32): i32 { var o: SOut = esci(nums_of(n)); var k: i32[] = nums_of(n + 9); if (k.len() < 0) { return 0; } return o.b.len() + o.b[0] - n + o.b[3] - n; }
function main(): i32 { var i: i32 = 0; while (i < 2000) { if (round(i) != 7) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostArrArgReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostArrArgReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range arrArgReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = arg temp leaked; 99 = over-release/underflow; 94-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

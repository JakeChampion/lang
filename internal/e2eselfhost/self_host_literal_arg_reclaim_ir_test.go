package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLiteralArgReclaimIRX86_64 pins #4355 slice 6: a string-LITERAL
// call arg allocates a fresh 16-byte rc-headered box per evaluation
// (const_str; its .rodata data is heap-guard-skipped), and nothing freed it —
// one box leaked per call in any loop passing literal string args. At a
// BORROWABLE param position of a known free function (borrowable_params_of:
// provably borrow-read-only, never escaping — so the callee cannot retain or
// return the arg) the call lowering now stashes the literal arg and frees it
// right after the call via the rc-aware __fern_str_free, net-zero on the
// operand stack under the live result. Non-borrowable positions (returned /
// stored / forwarded args) keep the sound leak — pinned below.
func TestSelfHostLiteralArgReclaimIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = literal-arg box leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// Literal arg at a borrowable position — churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit("ab"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + readit("ab"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 4400) { return 97; }
    return 0;
}`, "literal-arg-borrowable-flat", 0)

	// NON-borrowable position (callee RETURNS its param): the literal box is
	// retained by the binding, so it must NOT be freed at the call edge — the
	// bound value stays readable at detector zero (the box keeps its prior
	// sound leak).
	run(t, `function keepit(nm: string): string { return nm; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var got: string = keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "literal-arg-retained-safe", 0)

	// FRESH CONCAT arg (#4355 slice 7): `readit(base + "bc")` — the concat
	// byte-copies into a fresh anonymous temp; is_fresh_str_temp admits it at
	// the borrowable position and the post-call free reclaims it. base (an
	// operand, only read) stays readable; churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var base: string = "a";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit(base + "bc"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + readit(base + "bc"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (base.len() != 1) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 6600) { return 97; }
    return 0;
}`, "fresh-concat-arg-flat", 0)

	// FRESH PRODUCER-METHOD arg (#4355 slice 7): `readit(src.to_ascii_upper())` —
	// a copying string method's result is a fresh temp; reclaimed after the
	// call, the receiver survives, churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var src: string = "AbC" + "d";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit(src.to_ascii_upper()); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + readit(src.to_ascii_upper()); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (src.len() != 4) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 8800) { return 97; }
    return 0;
}`, "fresh-producer-arg-flat", 0)

	// BARE-IDENT arg trap: `readit(src)` aliases a live local —
	// is_fresh_str_temp excludes it, so NO free is emitted; src stays
	// readable across every call at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var src: string = "aa" + "bb";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        if (readit(src) != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (src.len() != 4) { return 88; }
    if (bad != 0) { return 87; }
    return 0;
}`, "bare-ident-arg-safe", 0)

	// MIXED args: the literal frees, the LIVE ident arg is untouched (a
	// borrowed read — no stash, no free) and stays readable; churn flat.
	run(t, `function pick(a: string, b: string): i32 { return a.len() + b.len(); }
function main(): i32 {
    var live: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + pick("x", live); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + pick("x", live); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (live.len() != 4) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 11000) { return 97; }
    return 0;
}`, "literal-arg-mixed-flat", 0)

	// #6544: the same reclaim at a METHOD call. It was wired at the free-call
	// site only, so `x.m("lit")` leaked a box per call where `m(x, "lit")` did
	// not — measured 23 B/round on a string receiver and 24 on an i32 one, in
	// every program that passes a literal to a method. The borrowability
	// registry already keyed methods "<Type>.<method>"; what was missing was
	// the stash at the three user-method arms and a census that can see a
	// method callee at all.
	run(t, `function (s: string) readit(nm: string): i32 { return s.len() + nm.len(); }
function main(): i32 {
    var recv: string = "rr";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + recv.readit("ab"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + recv.readit("ab"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (recv.len() != 2) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 8800) { return 97; }
    return 0;
}`, "method-literal-arg-borrowable-flat", 0)

	// The PRIMITIVE-receiver arm is a separate site from the struct one.
	run(t, `function (k: i32) tally(nm: string): i32 { return k + nm.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + (i % 4).tally("abc"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + (j % 4).tally("abc"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "method-literal-arg-prim-recv-flat", 0)

	// A fresh anonymous temp at a method call — the concat shape, not just a
	// literal.
	run(t, `function (s: string) readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var recv: string = "rr";
    var base: string = "a";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + recv.readit(base + "bc"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + recv.readit(base + "bc"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (base.len() != 1) { return 88; }
    if (recv.len() != 2) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 6600) { return 97; }
    return 0;
}`, "method-fresh-concat-arg-flat", 0)

	// NON-borrowable method param — the callee RETURNS it. Freeing at the call
	// edge would free what the binding now holds.
	run(t, `function (s: string) keepit(nm: string): string { return nm; }
function main(): i32 {
    var recv: string = "rr";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var got: string = recv.keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "method-literal-arg-retained-safe", 0)

	// The other non-borrowable direction, and the shape
	// conformance/cases/alloc_flat_method_identity_return is built from: the
	// param is MOVED into a struct field, so the field owns it after the call
	// and the literal keeps its prior sound leak. A reclaim that ignored the
	// borrowability verdict would free the tag out from under the result.
	run(t, `struct Box { tag: string, n: i32 }
function (b: Box) relabel(t: string): Box {
    if (t.len() == 0) { return b; }
    return Box { tag: t, n: b.n };
}
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var b: Box = Box { tag: "start", n: i % 8 };
        var r: Box = b.relabel("fresh-tag-value");
        if (r.tag.len() != 15) { bad = 1; }
        if (b.tag.len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "method-literal-arg-consumed-safe", 0)

	// A fresh PRODUCER CALL in the same argument position. The stash already
	// admitted a literal, a concat and a scalar `.to_string()`; a call to a
	// function str_fresh_ret_fns proves returns a fresh sole-owned box —
	// `size(mks(i))` — reached none of them, so the box leaked once per
	// evaluation (70 B/round measured). The producer is the ordinary way to
	// build the argument, so the leak was on the ordinary path.
	run(t, `function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function size(s: string): i32 { return s.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + size(mks(i))) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { acc = (acc + size(mks(j))) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "producer-call-arg-borrowable-flat", 0)

	// REFUSED — the callee RETURNS the argument, so the result aliases the
	// temp and the caller reads it after. Freeing at the call would be a
	// use-after-free rather than a leak, which is why this reads the result
	// back rather than only counting bytes.
	run(t, `function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function pick(s: string): string { return s; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) {
        var r: string = pick(mks(i));
        if (r.len() < 41) { bad = 1; }
        if (r[0:1] != "a") { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arg-returned-safe", 0)

	// REFUSED — the callee MOVES the argument into a struct field, so the
	// returned box owns it. The field is read back after churn has recycled
	// the freed bytes.
	run(t, `struct Box { name: string, k: i32 }
function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function keep(s: string, k: i32): Box { return Box { name: s, k: k }; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) {
        var b: Box = keep(mks(i), i);
        if (b.name.len() < 41) { bad = 1; }
        if (b.k != i) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arg-stored-safe", 0)

	// A LOCAL at the same borrowable position is NOT a temp — `nm` is read
	// after the call and its own scope-exit release owns it. A stash here
	// would double-free.
	run(t, `function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function size(s: string): i32 { return s.len(); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) {
        var nm: string = mks(i);
        if (size(nm) < 41) { bad = 1; }
        if (nm.len() < 41) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arg-bound-local-safe", 0)

	// The ARRAY sibling of the producer-call arg. The stash arm admitted only a
	// `parser.ExprArray` literal via discardable_scalar_arr_lit, so the same
	// temp one step removed — a call to an "ARR:"-registered producer — leaked
	// its buffer per evaluation (55 B/round measured). The registry already
	// admits a loop-built producer (body_returns_local_built_arr), so this is a
	// call-site widening, not a registry one.
	run(t, `function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function size(d: i32[]): i32 { return d.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + size(mk(i))) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { acc = (acc + size(mk(j))) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "producer-call-arr-arg-borrowable-flat", 0)

	// REFUSED — the callee RETURNS the array, so the result aliases the temp and
	// is read after. Freeing at the call would be a use-after-free.
	run(t, `function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function pick(d: i32[]): i32[] { return d; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) {
        var r: i32[] = pick(mk(i));
        if (r.len() != 3) { bad = 1; }
        if (r[2] != i + 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arr-arg-returned-safe", 0)

	// REFUSED — a constructor STORES the array, so the returned struct owns it.
	// This is the shape conformance/cases/alloc_flat_fresh_array_arg is built
	// from; closing it needs native's per-argument counted-retain admission, not
	// this borrowable-position stash.
	run(t, `struct Node { deps: i32[], k: i32 }
function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function node(deps: i32[], k: i32): Node { return Node { deps: deps, k: k }; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) {
        var b: Node = node(mk(i), i);
        if (b.deps.len() != 3) { bad = 1; }
        if (b.deps[1] != i + 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arr-arg-stored-safe", 0)

	// A bound LOCAL at the same position is not a temp — it is read after the
	// call and its own scope-exit release owns it.
	run(t, `function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function size(d: i32[]): i32 { return d.len(); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) {
        var live: i32[] = mk(i);
        if (size(live) != 3) { bad = 1; }
        if (live[0] != i) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arr-arg-bound-local-safe", 0)

	// COUNTED-RETAIN: `keep` STORES the array in a struct, so its parameter is
	// not borrowable and the stash above declines — yet every appearance of that
	// parameter is a counted store, so the temp is rc 2 on the escaping path and
	// rc 1 otherwise and one post-call dec nets it to a single owner either way.
	// That is native's paramCountedRetain, and it is what admits here. 55 B/round
	// before.
	run(t, `struct Node { deps: i32[], k: i32 }
function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function keep(deps: i32[], k: i32): i32 { var n: Node = Node { deps: deps, k: k }; return n.k + n.deps.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + keep(mk(i), i)) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { acc = (acc + keep(mk(j), j)) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "counted-retain-arr-arg-flat", 0)

	// REFUSED on the RESULT TYPE, not on the parameter. `both`'s parameter is
	// counted-retain exactly like `keep`'s, but it returns `i32[]` — the result
	// can BE the argument, and the dec fires immediately after the call, so
	// releasing would hand the caller freed memory. The elements are re-read, so
	// a wrongly admitted release shows as a wrong value rather than a byte count.
	run(t, `struct Node { deps: i32[], k: i32 }
function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function both(deps: i32[], k: i32): i32[] { var n: Node = Node { deps: deps, k: k }; return n.deps; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) {
        var r: i32[] = both(mk(i), i);
        if (r.len() != 3) { bad = 1; }
        if (r[0] != i || r[2] != i + 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "counted-retain-arr-arg-pointer-result-safe", 0)

	// REFUSED on the PARAMETER: `hand` returns its argument, so the appearance
	// in return position is not a counted store and the param is never credited.
	run(t, `function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function hand(deps: i32[]): i32 { var keepit: i32[] = deps; return keepit.len() + deps[0]; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) { if (hand(mk(i)) != 3 + i) { bad = 1; } i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "counted-retain-arr-arg-aliased-param-safe", 0)
}

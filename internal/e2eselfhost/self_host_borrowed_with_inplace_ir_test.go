package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostBorrowedWithInPlaceIRX86_64 pins #6158: the in-place `arr_set`
// behind a `x = x.with(i, v)` SELF-REASSIGN is sound only when `x` is a sole
// owner, and the self-host was taking it for BORROWED values too — so the write
// landed in an array the caller still owned.
//
// The gate it consulted (`aliased_names`, #3599) models local ALIASING — a name
// bound to a second local, or stored into a container literal — and knows
// nothing about ownership. A local bound from a struct field read has no local
// alias at all, so it sailed through.
//
// SCOPE: the FIELD-READ shapes, the bare-ident REBIND shapes (#6170) —
// `var heap = heap_in; heap = heap.with(…)` over a borrowed param, and
// `var b = a; b = b.with(…)` over a still-live local — and the DIRECT form
// (#6185), `function f(buf: i32[]) { buf = buf.with(…); }`.
//
// The direct form is the one that needed a representation change rather than a
// gate tweak. A mutable capture is written the same way: box_mutated_scalar_-
// captures rewrites a captured `x = v` into `x = x.with(0, v)` on a 1-element
// cell, and the param-lift hands that cell to the lifted body as a plain array
// param (#5301), which MUST write through it — that write-through IS the by-
// reference capture semantics. Both arrive as a non-`own` array param and
// demand opposite lowerings, so no aliasing analysis can separate them; a first
// attempt that credited the param unconditionally cost five red CI shards,
// failing `loop-accumulator` (32, want 42) and `outer-and-inner-write`
// (38, want 42) on all three backends.
//
// The cell now carries a `$cell$` name (box_rewrite_stmt), the same collision-
// free marker `$wc$` uses for wide captures, so the mode rides on the name that
// ParamDecl has no field for. `borrowed_names_of` credits every other non-`own`
// array param as a member, and the two cases are distinguishable at the gate.
//
// Every expected value came from `bin/fern -interp`, and each failing case was
// confirmed to diverge on the pre-fix compiler at the value named in its
// comment.
func TestSelfHostBorrowedWithInPlaceIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// The shape #6158 was filed as: an array field read off a BORROWED struct
	// param. std/regex's __rx_addthread is written this way, which is how the
	// bug reached the regex engine. Called twice with the same `st`, so the
	// second call observes the first's write. Pre-fix: 10.
	run(t, `struct St { seen: i32[], n: i32 }
function mark(st: St, pc: i32): St {
    var sn: i32[] = st.seen;
    if (sn[pc] == 1) { return St { seen: sn, n: 0 }; }
    sn = sn.with(pc, 1);
    return St { seen: sn, n: 1 };
}
function main(): i32 {
    var st: St = St { seen: [0, 0, 0, 0], n: 0 };
    var a: St = mark(st, 2);
    var b: St = mark(st, 2);
    return a.n * 10 + b.n;
}`, "borrowed-struct-param-field", 11)

	// The source need not be a param: a LOCAL struct still read afterwards has
	// the same hazard. Pre-fix: 11 (st.seen[2] became 1).
	run(t, `struct St { seen: i32[], n: i32 }
function main(): i32 {
    var st: St = St { seen: [0, 0, 0, 0], n: 0 };
    var sn: i32[] = st.seen;
    sn = sn.with(2, 1);
    return st.seen[2] * 10 + sn[2];
}`, "local-struct-field", 1)

	// MUST NOT REGRESS: the `own` param + field-clear idiom. The field is
	// emptied before the mutation, so the local genuinely is the only
	// reference and the in-place write is correct. This is arm64_asm_resolve's
	// shape — the three quadratic patch loops #6011 made fast — and an
	// ownership-blind fix would send it down the clone path.
	run(t, `struct St { seen: i32[], n: i32 }
function bump(own st: St, pc: i32): St {
    var sn: i32[] = st.seen;
    st = St { ...st, seen: [] };
    sn = sn.with(pc, 1);
    return St { seen: sn, n: 1 };
}
function main(): i32 {
    var a: St = bump(St { seen: [0, 0, 0, 0], n: 0 }, 2);
    return a.seen[2] * 10 + a.n;
}`, "own-param-field-cleared", 11)

	// `.append` is NOT this bug — #6155 fixed that path by bracketing the
	// receiver. Here so a future change to the shared gate cannot quietly
	// alter it.
	run(t, `struct St { xs: i32[], n: i32 }
function grow(st: St, v: i32): i32 {
    var a: i32[] = st.xs;
    a = a.append(v);
    return a.len();
}
function main(): i32 {
    var st: St = St { xs: [1, 2], n: 0 };
    var p: i32 = grow(st, 9);
    var q: i32 = grow(st, 8);
    return p * 10 + q;
}`, "append-unaffected", 33)

	// The PERF guard, and the reason it is an assertion rather than a comment:
	// an over-broad fix to this gate fails as SILENT QUADRATIC COPYING, not as
	// a wrong answer, so every correctness case above would still pass while
	// the assembler's patch loops became O(n^2).
	//
	// 4000 `.with` writes through an `own` param must allocate nothing — the
	// whole point of the annotation. Measured with __heap_bump_bytes() (the
	// bump allocator's high-water mark) rather than RSS, which is not
	// comparable across hosts. Pre-fix and post-fix this reads 0 MB; with the
	// ownership check dropped it reads ~128 MB.
	run(t, `function fill(own buf: i32[], n: i32): i32[] {
    var i: i32 = 0;
    while (i < n) { buf = buf.with(i, i); i = i + 1; }
    return buf;
}
function main(): i32 {
    var b: i32[] = [];
    var k: i32 = 0;
    while (k < 4000) { b = b.append(0); k = k + 1; }
    var before: i64 = __heap_bump_bytes();
    b = fill(b, 4000);
    var after: i64 = __heap_bump_bytes();
    if (b[3999] != 3999) { return 90; }
    // Any cloning at all shows up here: one clone is 16 KB, and the loop would
    // do 4000 of them. Allow 64 KB of slack for unrelated allocation.
    if (after - before > 65536) { return 91; }
    return 7;
}`, "own-param-with-loop-allocates-nothing", 7)

	// #6170: the bare-ident REBIND of a borrowed array param. `alias_idents_in_value`
	// credits `heap_in` — the name that ACQUIRED an alias — while the name actually
	// mutated is `heap`, which is neither a param nor a field read, so nothing marked
	// it and the write went through into the caller's buffer. Pre-fix: 77.
	run(t, `function run(heap_in: i32[], at: i32, v: i32): i32[] {
    var heap: i32[] = heap_in;
    heap = heap.with(at, v);
    return heap;
}
function main(): i32 {
    var h: i32[] = [0, 0, 0, 0];
    var r: i32[] = run(h, 1, 7);
    return h[1] * 10 + r[1];
}`, "rebind-of-borrowed-param", 7)

	// The same rebind over a plain LOCAL that is still live afterwards. No param
	// and no field read is involved, so this one is invisible to both prior gates;
	// it is the shape the liveness arm of the rule exists for. Pre-fix: 77.
	run(t, `function main(): i32 {
    var a: i32[] = [0, 0, 0, 0];
    var b: i32[] = a;
    b = b.with(1, 7);
    return a[1] * 10 + b[1];
}`, "rebind-of-live-local", 7)

	// MUST NOT REGRESS, and the case that keeps the rule from being written as
	// "any rebind clones": the source is an `own` param and is dead after the
	// rebind, so `heap` is the buffer's only remaining name and the stores stay
	// in place. This is the const-eval VM's shape (irlower's `eval_ops`, which
	// rebinds `heap` from `heap_in` and writes it a dozen times per op), and the
	// reason #6170 was split out of #6158 in the first place.
	//
	// Asserted by ALLOCATION, not by answer: getting this wrong is silent
	// quadratic copying that every correctness case above still passes. With the
	// rebind credited unconditionally this read ~128 MB instead of 0.
	run(t, `function fill(own heap_in: i32[], n: i32): i32[] {
    var heap: i32[] = heap_in;
    var i: i32 = 0;
    while (i < n) { heap = heap.with(i, i); i = i + 1; }
    return heap;
}
function main(): i32 {
    var b: i32[] = [];
    var k: i32 = 0;
    while (k < 4000) { b = b.append(0); k = k + 1; }
    var before: i64 = __heap_bump_bytes();
    b = fill(b, 4000);
    var after: i64 = __heap_bump_bytes();
    if (b[3999] != 3999) { return 90; }
    if (after - before > 65536) { return 91; }
    return 7;
}`, "own-param-rebind-loop-allocates-nothing", 7)

	// #6185: the DIRECT borrowed-param self-reassign. No rebind and no field
	// read, so both prior gates are blind to it and only the param membership
	// added with the `$cell$` marker catches it. Pre-fix: 77.
	run(t, `function run(heap: i32[], at: i32, v: i32): i32[] {
    heap = heap.with(at, v);
    return heap;
}
function main(): i32 {
    var h: i32[] = [0, 0, 0, 0];
    var r: i32[] = run(h, 1, 7);
    return h[1] * 10 + r[1];
}`, "direct-borrowed-param", 7)

	// The same direct form on a LIFTED lambda's own DECLARED array param. This
	// is why the fix distinguishes capture params from declared ones instead of
	// exempting `__lam_*` bodies wholesale: `a` here is a borrow from the
	// closure's caller and must clone, while `k` in the same body is a capture.
	// Pre-fix: 104.
	run(t, `function main(): i32 {
    var k: i32 = 5;
    var g: (i32[]) => i32 = function (a: i32[]): i32 { a = a.with(0, 9); return a[0] + k; };
    var b: i32[] = [1, 2];
    var r: i32 = g(b);
    return b[0] * 10 + r;
}`, "lifted-lambda-declared-array-param", 24)

	// MUST NOT REGRESS, and the case the whole design turns on: the capture
	// cell reaches the lifted body as a non-`own` array param exactly like
	// `direct-borrowed-param` above, and must write THROUGH rather than clone.
	// A fix that credits every array param without the `$cell$` exemption
	// returns 38 here.
	run(t, `function main(): i32 {
    var x: i32 = 0;
    var f: () => i32 = function (): i32 { x = x + 4; return 0; };
    x = 3;
    var r: i32 = f();
    return x + 35;
}`, "mut-capture-cell-param-writes-through", 42)
}

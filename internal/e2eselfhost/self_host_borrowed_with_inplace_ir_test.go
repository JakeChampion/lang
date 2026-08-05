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
// nothing about ownership. A borrowed array parameter has no local alias at
// all, so it sailed through.
//
// The language already models the sound version and the hot paths already use
// it: `arm64_write_word(own buf: i32[], …)` consumes its buffer precisely so
// the rc check sees a unique owner, with E051 making that annotation
// trustworthy at every call site. The fix keys on ownership rather than on the
// syntax of the binding, so those stay in place.
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

	// The smallest shape of the bug: no struct, no field read. A BORROWED array
	// param overwritten in place, so the caller's array changed under it.
	// Pre-fix: 77 (b[1] became 7).
	run(t, `function patch(buf: i32[], at: i32, v: i32): i32[] {
    buf = buf.with(at, v);
    return buf;
}
function main(): i32 {
    var b: i32[] = [0, 0, 0, 0];
    var p: i32[] = patch(b, 1, 7);
    return b[1] * 10 + p[1];
}`, "borrowed-array-param", 7)

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
}

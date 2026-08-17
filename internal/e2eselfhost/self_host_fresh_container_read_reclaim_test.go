package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// freshContainerReadReclaimCases pin the #6491 read-site reclaim: `mk()[i]` and
// `mk().f` read a value out of a container NOTHING NAMES, so the read is the
// only place that container can be reclaimed — there is no slot for the exit
// sweep to find. The self-host leaked the whole container per evaluation where
// native is flat.
//
// Every case decides its own verdict and reports it as an exit code, so the
// harness needs no output parsing: 91/93 = wrong value, 92 = the allocator's
// fresh-byte curve GREW when it must be flat (the shape
// conformance/cases/alloc_flat_index_of_fresh_container asserts), 94 = it was
// flat where a known-open shape is still expected to grow, 90 = a live buffer
// was freed and reused underneath its owner, 99 = a box released twice.
//
// `fern -interp` is NOT an oracle for these: it has no rc runtime, so
// __rc_underflow_count is undefined there and __heap_bump_bytes answers 0.
var freshContainerReadReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// A SCALAR element read out of a fresh owned array — the "ARR:" strict-fresh
	// registry entry. Leaked the whole 4-element buffer per evaluation
	// (2800 B / 50 rounds, exactly doubling); the buffer is now rc-dec'd at the
	// read, element-blind because a scalar rides the freed buffer.
	{"fresh-arr-index", `function lit(n: i32): i32[] { return [n, n + 1, n + 2, n + 3]; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { acc = acc + lit(i)[2]; i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1325) { return 91; }
    if (y != 5150) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// A SCALAR field read off a fresh owned struct with no reclaimable field:
	// the box alone is dec'd, no __struct_drop_<T> involved.
	{"fresh-struct-field-scalar", `struct Pair { j: i32, k: i32 }
function pair(n: i32): Pair { return Pair { j: n, k: n + 1 }; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { acc = acc + pair(i).k; i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1275) { return 91; }
    if (y != 5050) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The same read on a struct that DOES carry an rc-array field, so the box dec
	// is preceded by __struct_drop_<T>. This is the half that would double-free if
	// the admission were looser than the strict-fresh registry: the field buffer is
	// reclaimed as well as the box, and the churn afterwards would surface a
	// surviving reference as a corrupt read.
	{"fresh-struct-field-deep", `struct Bag { xs: i32[], k: i32 }
function bag(n: i32): Bag { return Bag { xs: [n, n + 1, n + 2, n + 3], k: n }; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { acc = acc + bag(i).k; i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1225) { return 91; }
    if (y != 4950) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The refusal that carries the safety argument. `borrowed` hands back a struct
	// whose array field is the CALLER's live buffer, so it is not in the
	// strict-fresh registry and the read reclaims nothing. If the admission were
	// widened to "any struct-returning call", __struct_drop_Bag would free `live`
	// out from under main — the churn below re-fills the freed buffer, so a
	// surviving double-free reads 9s and reports 90 rather than passing by luck.
	{"borrowed-field-refused", `struct Bag { xs: i32[], k: i32 }
function borrowed(v: i32[], n: i32): Bag { return Bag { xs: v, k: n }; }
function main(): i32 {
    var live: i32[] = [1, 2, 3];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { t = t + borrowed(live, i).k; i = i + 1; }
    var churn1: i32[] = [9, 9, 9];
    var churn2: i32[] = [9, 9, 9];
    if (t != 4950) { return 91; }
    if (live[0] + live[1] + live[2] != 6) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The accumulator idiom — the way a producer is actually written. `nums`
	// builds its result in a LOCAL and returns that, which the literal-return rule
	// declines; `body_returns_local_built_arr` admits it by proving the buffer is
	// built entirely in that frame (literal init, self-append only, no other
	// escape), so it reaches the caller as its sole rc == 1 reference. Leaked
	// 2824 B / 50 rounds, doubling, before that admission.
	{"local-built-producer", `function nums(n: i32): i32[] {
    var out: i32[] = [];
    var i: i32 = 0;
    while (i < 4) { out = out.append(n + i); i = i + 1; }
    return out;
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { acc = acc + nums(i)[1]; i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1275) { return 91; }
    if (y != 5050) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The refusal that keeps the local-built admission honest. `seeded` rebinds its
	// local FROM A PARAMETER before appending, so the buffer it hands back is the
	// caller's — reclaiming it at the read would free `live` out from under main.
	// `body_unsafe_for_allow_ret` cannot catch this on its own (its assign arm reads
	// only the assigned value, and `out = src` never mentions `out`), which is why
	// `arr_reassigned_other_than_selfappend` exists. The churn re-fills the freed
	// buffer with 9s, so a widened admission reports 90 rather than passing by luck.
	{"param-seeded-producer-refused", `function seeded(src: i32[], n: i32): i32[] {
    var out: i32[] = [];
    out = src;
    out = out.append(n);
    return out;
}
function main(): i32 {
    var live: i32[] = [1, 2, 3];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { t = t + seeded(live, i)[0]; i = i + 1; }
    var churn1: i32[] = [9, 9, 9];
    var churn2: i32[] = [9, 9, 9];
    if (t != 100) { return 91; }
    if (live[0] + live[1] + live[2] != 6) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// ── the POINTER half ────────────────────────────────────────────────────
	// A `string` element ALIASES the buffer being freed, so the release is a deep
	// one (__fern_str_arr_free: every element box, then the buffer) preceded by a
	// retain of the element that survives it. Leaked the whole 4-string container
	// per evaluation — 17256 B / 50 rounds, exactly doubling.
	{"fresh-strarr-index-bound", `function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function strs(n: i32): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 4) { out = out.append(wide(n + i)); i = i + 1; }
    return out;
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: string = strs(i)[1]; acc = acc + b.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 2091) { return 91; }
    if (y != 4192) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The same read with NO destination to own the extracted string: a borrowing
	// `.len()`. The container reclaim alone would strand the element it kept
	// alive, so the receiver position frees it after the read — the binding
	// credit's twin.
	{"fresh-strarr-index-borrowed", `function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function strs(n: i32): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 4) { out = out.append(wide(n + i)); i = i + 1; }
    return out;
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { acc = acc + strs(i)[0].len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 2090) { return 91; }
    if (y != 4190) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// A `string` FIELD read off a fresh owned struct. Unlike the element, this one
	// is a MOVE and not a retain-and-drop: the strict-fresh admission plus the
	// per-field freshness proof mean the box holds the string's only reference,
	// so the read hands it on and the box dec is the whole release.
	{"fresh-struct-field-string", `struct Box { name: string, k: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function boxed(n: i32): Box { return Box { name: wide(n), k: n }; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var f: string = boxed(i).name; acc = acc + f.len() + boxed(i).name.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 4180) { return 91; }
    if (y != 8380) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The same read off a METHOD producer (#6544). `bump()` returns a
	// strict-fresh Box every time, so the temp the read leaves behind is this
	// frame's own allocation — the free-function cases above with the receiver
	// the registry already keys ("<Base>.<method>"). The struct box AND its
	// fresh tag leaked per round before.
	{"fresh-struct-method-field-string", `struct Box { tag: string, n: i32 }
function (b: Box) bump(): Box { return Box { tag: b.tag + "!", n: b.n + 1 }; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: 1 }; acc = acc + b.bump().tag.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 900) { return 91; }
    if (y != 1800) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The SCALAR field off a method producer: the box dec alone, no field move —
	// fresh-struct-field-scalar with the receiver. The struct carries no string
	// field, which is the same restriction that case has: the bare registry entry
	// proves a fresh LITERAL, not that the box owns its string buffers, so a
	// string-bearing struct read for a scalar still strands them (the STRFLDF
	// admission carries that proof, and spending it here is the next slice).
	{"fresh-struct-method-field-scalar", `struct Pair { j: i32, k: i32 }
function (p: Pair) bump(): Pair { return Pair { j: p.j + 1, k: p.k + 1 }; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var p: Pair = Pair { j: i, k: i + 1 }; acc = acc + p.bump().k; i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1325) { return 91; }
    if (y != 5150) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The IDENTITY-return refusal, which is what stops the method admission from
	// becoming an over-release: `me()` hands the RECEIVER back, so the "temp" the
	// read would free is `keep`'s own box and the moved-out tag is `keep`'s own
	// string. Every return must be a struct LITERAL for the registry to admit,
	// and this one is not.
	{"method-identity-return-refused", `struct Box { tag: string, n: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function (b: Box) me(): Box { return b; }
function main(): i32 {
    var keep: Box = Box { tag: wide(7), n: 7 };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { var f: string = keep.me().tag; t = t + f.len(); i = i + 1; }
    var churn1: string = "0123456789" + "0123456789";
    var churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (keep.tag.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The RECEIVER-FIELD refusal, the method sibling of strfld-param-value-refused:
	// `same()` builds a fresh box around the receiver's own string, so moving that
	// string out and freeing it would release `keep`'s tag underneath it.
	{"method-receiver-field-value-refused", `struct Box { tag: string, n: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function (b: Box) same(): Box { return Box { tag: b.tag, n: b.n }; }
function main(): i32 {
    var keep: Box = Box { tag: wide(7), n: 7 };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { var f: string = keep.same().tag; t = t + f.len(); i = i + 1; }
    var churn1: string = "0123456789" + "0123456789";
    var churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (keep.tag.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The refusal that carries the pointer half's safety argument on the ELEMENT
	// side. `pair` builds its array out of a FIELD READ, which the array literal
	// does not retain — so if the producer were admitted, the deep free would
	// release `keep`'s own string box. The churn re-uses the freed bytes, so a
	// widened admission reads a corrupted length and reports 90.
	{"strarr-aliased-element-refused", `struct Box { name: string, k: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function pair(b: Box): string[] { return [b.name, b.name]; }
function main(): i32 {
    var keep: Box = Box { name: wide(7), k: 7 };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { t = t + pair(keep)[0].len(); i = i + 1; }
    var churn1: string = "0123456789" + "0123456789";
    var churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (keep.name.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The FIELD-side refusal. `wrap` stores its own PARAMETER in the returned
	// struct, so the box does not own that string — moving it out and letting the
	// destination free it would release main's `live` underneath it.
	{"strfld-param-value-refused", `struct Box { name: string, k: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function wrap(s: string, n: i32): Box { return Box { name: s, k: n }; }
function main(): i32 {
    var live: string = wide(3);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { var f: string = wrap(live, i).name; t = t + f.len(); i = i + 1; }
    var churn1: string = "0123456789" + "0123456789";
    var churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (live.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostFreshContainerReadReclaimIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostFreshContainerReadReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range freshContainerReadReclaimCases {
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
				t.Errorf("%s = %d, want %d (90 = a live buffer was freed and reused; "+
					"91/93 = wrong value; 92 = fresh bytes grew where the shape must be flat; "+
					"94 = flat where a known-open shape still grows — see the case comment; "+
					"99 = over-release/underflow)", tc.name, code, tc.want)
			}
		})
	}
}

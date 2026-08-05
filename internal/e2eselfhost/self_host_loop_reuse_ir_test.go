package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// loopReuseIRCases exercise Perceus FBIP constructor reuse fired INSIDE a LOOP
// BODY (and every nested block — if / match arms) on the self-hosted stack-IR
// path (irlower `lower_block`). Native's high-value same-block reuse pass reclaims
// loop-churn allocations: a fresh struct / tuple construction that reuses an
// earlier dead same-block donor's box is lowered in place (zero-alloc) instead of
// allocating a fresh box. In a loop the recipient slot is loop-carried, so the
// reuse emitter releases its prior (previous-execution) box
// (`emit_reuse_recip_prior_release`) before the in-place overwrite — one alloc +
// one free per execution, balanced (this holds for an if-arm nested in a loop too).
//
// Two contracts per case:
//   - exit code pins VALUE correctness (a reuse that mis-stored, double-freed, or
//     stranded a live read would corrupt or crash);
//   - boxAssert pins the EMISSION contract: a reuse loop allocates ONE tuple/struct
//     box (the per-iteration donor, handed to the reuser), a donor-live control
//     allocates TWO. A regression to no-loop-reuse bumps the reuse cases to 2.
var loopReuseIRCases = []struct {
	name      string
	src       string
	expected  int
	boxAssert int // exact `call __fern_arr_box` count in the program
}{
	// Struct loop reuse: `a` is dead by the time `b` is built each iteration, so b
	// reuses a's box in place — ONE allocation. sum over i in 0..3 of
	// (i + (i+1)) + (i*2 + 3): i=0:1+3=4; i=1:3+5=8; i=2:5+7=12; i=3:7+9=16 = 40.
	{"loop-struct-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`,
		40, 1},
	// Struct donor-live control: `a` is read AFTER `b` is built, so reuse is
	// suppressed and both allocate (TWO boxes). Same value shape: sum over i in
	// 0..3 of (i + (i+1)) + (i + 3) = 4+... i=0:1+3=4;i=1:3+4=7;i=2:5+5=10;i=3:7+6=13 = 34.
	{"loop-struct-donor-live",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var b: P = P { x: i, y: 3 }; sum = sum + a.x + a.y + b.x + b.y; i = i + 1; } return sum; }`,
		34, 2},
	// Tuple loop reuse: b reuses a's box in place each iteration — ONE allocation.
	// sum over i in 0..3 of (i + (i+1)) + (i + 3) = 34.
	{"loop-tuple-reuse",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`,
		34, 1},
	// Tuple donor-live control: no reuse, TWO boxes.
	{"loop-tuple-donor-live",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var b: (i32, i32) = (i, 3); sum = sum + a.0 + a.1 + b.0 + b.1; i = i + 1; } return sum; }`,
		34, 2},
	// Memory-safety at scale: five million iterations of struct loop reuse. A
	// per-iteration double-free would crash (SIGSEGV) and a leaked recipient box
	// would exhaust the heap; the exit code 0 (sum kept mod 1000 = 0) with a
	// single static allocation proves the per-iteration alloc/free stays balanced.
	{"loop-struct-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 1000; i = i + 1; } return sum; }`,
		0, 1},
	// Functional-update (self-overwrite) reuse in a loop: `c = P { ...d, y: 3 }`
	// reuses the dead `d`'s box in place each iteration — ONE allocation. The
	// immutable-state-threading loop shape. sum over i in 0..3 of (d.x=i) + 3 =
	// (0+3)+(1+3)+(2+3)+(3+3) = 18.
	{"loop-funcupdate-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: P = P { x: i, y: 0 }; var c: P = P { ...d, y: 3 }; sum = sum + c.x + c.y; i = i + 1; } return sum; }`,
		18, 1},
	// Functional-update memory safety at scale: 5M iterations, balanced alloc/free
	// (the recipient's prior box freed each turn), exit 0 (sum mod 1000).
	{"loop-funcupdate-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var d: P = P { x: i, y: 0 }; var c: P = P { ...d, y: 3 }; sum = (sum + c.x + c.y) % 1000; i = i + 1; } return sum; }`,
		0, 1},
	// Same-block reuse now fires in an IF-ARM body too (not just loops): `b` reuses
	// the dead `a`'s box inside the `if` — ONE allocation. Value: (10+20) + (3+4) = 37.
	// `* cond` (cond == 1, values unchanged) keeps both literals off the
	// STATIC-CONSTANT path (#6149) — an all-scalar-literal aggregate is placed in
	// data and allocates nothing, which would make this case measure zero against
	// zero instead of pinning that reuse fires.
	{"if-arm-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { var cond: i32 = 1; var r: i32 = 0; if (cond > 0) { var a: P = P { x: 10 * cond, y: 20 }; var s: i32 = a.x + a.y; var b: P = P { x: 3 * cond, y: 4 }; r = s + b.x + b.y; } return r; }`,
		37, 1},
	// An if-arm reuse NESTED in a loop churns per iteration: the recipient slot is
	// loop-carried, so its prior box is released each turn (a double-free would
	// crash, a leak would exhaust the heap). 5M iterations; exit = interp oracle.
	{"if-in-loop-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { if (i > 0) { var a: P = P { x: i, y: 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i, y: 2 }; sum = (sum + s + b.x + b.y) % 1000; } i = i + 1; } return sum; }`,
		229, 1},
	// ESCAPING RECIPIENT (regression): the reuse recipient `b` is moved into a
	// container (`acc.append(b)`) each iteration, so its box is owned by `acc`, not
	// by b's slot. The loop prior-release must NOT fire for it — freeing a box the
	// container still references is a use-after-free. Guarded by
	// slot_is_reclaimable_struct: an escaping recipient is not reclaimable, so no
	// prior-release. Value: acc holds (i,100) for i in 0..4, summed with the outer
	// loop's a.x+a.y … = 254 (a UAF corrupts this).
	{"loop-escaping-recipient-struct",
		`struct P { x: i32, y: i32 } function main(): i32 { var acc: P[] = []; var i: i32 = 0; while (i < 5) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i, y: 100 }; acc = acc.append(b); i = i + 1; } var sum: i32 = 0; var j: i32 = 0; while (j < acc.len()) { sum = sum + acc[j].x + acc[j].y; j = j + 1; } return sum; }`,
		254, 3},
	// Same regression for an escaping TUPLE recipient (slot_is_reclaimable_tuple).
	{"loop-escaping-recipient-tuple",
		`function main(): i32 { var acc: (i32, i32)[] = []; var i: i32 = 0; while (i < 5) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, 100); acc = acc.append(b); i = i + 1; } var sum: i32 = 0; var j: i32 = 0; while (j < acc.len()) { sum = sum + acc[j].0 + acc[j].1; j = j + 1; } return sum; }`,
		254, 3},
	// Struct with a leak-safe ARRAY field, reused in a loop: the reuse must release
	// the donor's OLD array (each iteration) before writing the recipient's fresh
	// one, or the old array leaks / the box double-frees. Three static boxes (the
	// two array literals + the reused struct box). sum over i in 0..3 of
	// (n + xs[0]=1) + (b.xs[1]=5 + b.n=i*2) = (i+1) + (5 + 2i) = 3i + 6 = 6+9+12+15 = 42.
	{"loop-struct-array-field-reuse",
		`struct P { xs: i32[], n: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { xs: [1, 2, 3], n: i }; var s: i32 = a.n + a.xs[0]; var b: P = P { xs: [4, 5], n: i * 2 }; sum = sum + s + b.xs[1] + b.n; i = i + 1; } return sum; }`,
		42, 3},
	// CROSS-BLOCK reuse (irlower xblock_pending): the donor `a` lives at the loop
	// body's top level and is dead by the nested `if`, whose recipient `b` reuses
	// a's box — ONE allocation even though a and b are in different blocks. This is
	// native's cross-block pass (a loop-body value reused by a construction nested
	// in an if inside the loop). sum over i in 1..3 of (i+3) + a-side s… computes
	// to 31 (a corruption from a stranded a-read or bad reuse would differ).
	{"cross-block-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; if (i > 0) { var b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + s; i = i + 1; } return sum; }`,
		31, 1},
	// Cross-block donor USED AFTER the if: `a` is read after the nested if, so it is
	// NOT dead-from-k and reuse must be suppressed (both allocate — TWO boxes). A
	// spurious reuse would strand the later a.x / a.y read. Value 25.
	{"cross-block-donor-used-after",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: 1 }; if (i > 0) { var b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + a.x + a.y; i = i + 1; } return sum; }`,
		25, 2},
	// Cross-block ESCAPING recipient: `b` (in the nested if) is appended into acc, so
	// its box is container-owned — the reuse fires but the reclaimable-gated
	// prior-release must NOT free it (a UAF otherwise). Value 56.
	{"cross-block-escaping-recipient",
		`struct P { x: i32, y: i32 } function main(): i32 { var acc: P[] = []; var i: i32 = 0; while (i < 6) { var a: P = P { x: i, y: 1 }; var s: i32 = a.x + a.y; if (i > 2) { var b: P = P { x: i, y: 100 }; acc = acc.append(b); } i = i + 1; } var sum: i32 = 0; var j: i32 = 0; while (j < acc.len()) { sum = sum + acc[j].x + acc[j].y; j = j + 1; } return sum; }`,
		56, 3},
	// Cross-block memory safety at scale: 5M iterations of a loop-body donor reused
	// by an if-nested recipient. The exit value matching the interp oracle (229)
	// proves balanced alloc/free — a double-free would crash, a leaked recipient
	// box would exhaust the heap. One static allocation (the reuse fires).
	{"cross-block-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var a: P = P { x: i, y: 1 }; var s: i32 = a.x + a.y; if (i > 0) { var b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 1000; } i = i + 1; } return sum; }`,
		229, 1},
	// Cross-block reuse also fires for a TUPLE recipient: the loop-body tuple donor
	// `a` is reused by the if-nested tuple `b` — ONE allocation. Value 31.
	{"cross-block-tuple-reuse",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; if (i > 0) { var b: (i32, i32) = (i, 3); sum = sum + b.0 + b.1; } sum = sum + s; i = i + 1; } return sum; }`,
		31, 1},
	// Cross-block tuple memory safety at scale: now that scalar tuple loop
	// temporaries are reclaimed, the reused box is freed each turn — 5M iterations
	// stay balanced (exit 229, one static allocation), not the pre-fix 190 MB leak.
	{"cross-block-tuple-churn-safe",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var a: (i32, i32) = (i, 1); var s: i32 = a.0 + a.1; if (i > 0) { var b: (i32, i32) = (i, 3); sum = (sum + b.0 + b.1) % 1000; } i = i + 1; } return sum; }`,
		229, 1},
	// NESTED-STRUCT FIELD reuse (Delta B): `d` has a nested-struct field
	// (`inner: Inner`) and is dead by the time `c` is built, so c reuses d's Outer
	// box in place — the recipient's Outer alloc is elided. The reuse RELEASES d's
	// OLD inner box (full freeing drop) before writing c's fresh inner, so no leak /
	// double-free. Static box sites: d's Outer + d's Inner + c's Inner = THREE (the
	// reused c.Outer is not allocated). Value: sum over i in 0..3 of
	// (d.inner.a+d.inner.b+d.n) + (c.inner.a=2i + 3 + 5) = (3i+1)+(2i+8) = 5i+9 →
	// 9+14+19+24 = 66.
	{"loop-nested-struct-field-reuse",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; var s: i32 = d.inner.a + d.inner.b + d.n; var c: Outer = Outer { inner: Inner { a: i * 2, b: 3 }, n: 5 }; sum = sum + s + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		66, 3},
	// Nested-struct donor-LIVE control: `d` is read AFTER `c` is built, so reuse is
	// suppressed and c's Outer allocates too — FOUR box sites. Same value 66.
	{"loop-nested-struct-field-donor-live",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; var c: Outer = Outer { inner: Inner { a: i * 2, b: 3 }, n: 5 }; sum = sum + d.inner.a + d.inner.b + d.n + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		66, 4},
	// Nested-struct field reuse memory safety at scale: 5M iterations. A leaked old
	// inner box would exhaust the heap; a double-free of it (or of the reused Outer
	// box) would crash. Exit 0 (sum mod 1000) with THREE static box sites proves the
	// per-iteration inner alloc/free stays balanced through the full-freeing-drop.
	{"loop-nested-struct-field-churn-safe",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; var s: i32 = d.inner.a + d.inner.b + d.n; var c: Outer = Outer { inner: Inner { a: i, b: 3 }, n: 5 }; sum = (sum + s + c.inner.a + c.inner.b + c.n) % 1000; i = i + 1; } return sum; }`,
		0, 3},
	// FUNCTIONAL-UPDATE (self-overwrite) reuse of a struct with a nested-struct
	// field: `c = Outer { ...d, inner: Inner{...} }` reuses d's dead box in place.
	// The OVERRIDDEN `inner` field full-freeing-drops d's old inner before writing
	// c's fresh inner; the CARRIED `n` field moves with the reused box. Static box
	// sites: d's Outer + d's Inner + c's fresh Inner = THREE (c's Outer reuses d's).
	// Value: sum over i in 0..3 of (c.inner.a=2i + c.inner.b=3 + c.n=i) = 3i+3 →
	// 3+6+9+12 = 30.
	{"loop-funcupdate-nested-struct-reuse",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; var c: Outer = Outer { ...d, inner: Inner { a: i * 2, b: 3 } }; sum = sum + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		30, 3},
	// Funcupdate nested-struct donor-LIVE control: `d` is read after `c` is built,
	// so d is not dead-after and reuse is suppressed — c allocates a fresh Outer too
	// (FOUR box sites). Value 46 (d.inner.a+d.inner.b added on top).
	{"loop-funcupdate-nested-struct-donor-live",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; var c: Outer = Outer { ...d, inner: Inner { a: i * 2, b: 3 } }; sum = sum + d.inner.a + d.inner.b + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		46, 4},
	// Funcupdate nested-struct memory safety at scale: 5M iterations. The overwritten
	// inner box is full-freeing-dropped each turn and the reused Outer box carried;
	// exit 0 (sum mod 1000) with THREE box sites proves alloc/free stays balanced (a
	// leaked inner would exhaust the heap, a double-free would crash).
	{"loop-funcupdate-nested-struct-churn-safe",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; var c: Outer = Outer { ...d, inner: Inner { a: i, b: 3 } }; sum = (sum + c.inner.a + c.inner.b + c.n) % 1000; i = i + 1; } return sum; }`,
		0, 3},
	// CROSS-BLOCK reuse of a struct with an ENUM field (Delta B follow-through:
	// xblock_scan_body now takes struct_fields_reusable_cross + the shared
	// cross_recipient_fields_fresh gate). The loop-body donor `d` (dead by the
	// nested if) is reused by the if-arm recipient `b`; b's enum value is a fresh
	// variant ctor, so the reuse arm's flat old-enum release is alias-free. Static
	// box sites: d's M + d's On payload + b's On payload = THREE (b's M box is
	// reused, __fern_alloc_reuse not arr_box). Value: sum over i of s=(i+1)+i plus
	// r=3+i for i>0 → 16 + 15 = 31, matching the interp oracle.
	{"cross-block-enum-field-reuse",
		`enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: M = M { tag: i, st: On(i + 1) }; var s: i32 = 0; match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } } if (i > 0) { var b: M = M { tag: i, st: On(3) }; var r: i32 = 0; match (b.st) { On(v) => { r = v + b.tag; }, Off => { r = 0; } } sum = sum + r; } sum = sum + s; i = i + 1; } return sum; }`,
		31, 3},
	// Cross-block enum-field donor USED AFTER the if: d's match sits after the
	// nested if, so d is not dead-from-k and the reuse must be suppressed — b's M
	// box allocates too (FOUR sites). Same value 31.
	{"cross-block-enum-field-donor-live",
		`enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: M = M { tag: i, st: On(i + 1) }; if (i > 0) { var b: M = M { tag: i, st: On(3) }; var r: i32 = 0; match (b.st) { On(v) => { r = v + b.tag; }, Off => { r = 0; } } sum = sum + r; } var s: i32 = 0; match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } } sum = sum + s; i = i + 1; } return sum; }`,
		31, 4},
	// Cross-block enum-field memory safety at scale: 5M iterations of the reuse
	// shape. The reuse arm flat-releases d's old enum payload box each turn; exit
	// 229 (sum mod 1000, the interp oracle) with THREE static sites proves the
	// per-iteration enum alloc/release stays balanced (a leak would exhaust the
	// heap, a double-free would crash).
	{"cross-block-enum-field-churn-safe",
		`enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var d: M = M { tag: i, st: On(i + 1) }; var s: i32 = 0; match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } } if (i > 0) { var b: M = M { tag: i, st: On(3) }; var r: i32 = 0; match (b.st) { On(v) => { r = v + b.tag; }, Off => { r = 0; } } sum = (sum + r) % 1000; } sum = (sum + s) % 1000; i = i + 1; } return sum; }`,
		229, 3},
}

// TestSelfHostLoopReuseIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserting the exit code and the exact box
// allocation count (the loop-reuse emission contract).
func TestSelfHostLoopReuseIRX86_64(t *testing.T) {
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

	for _, tc := range loopReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			boxes := countUserArrBoxAllocs(asm)
			if boxes != tc.boxAssert {
				t.Errorf("%s: expected %d box allocations (call __fern_arr_box), found %d — loop-reuse emission contract regressed", tc.name, tc.boxAssert, boxes)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

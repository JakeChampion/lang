package e2eselfhost

import (
	"strings"
	"testing"
)

// --- A struct payload's rc fields need a DEEP drop (#6127) -------------------
//
// `var o: Option[P] = Some(P { xs: [..], n: .. })` consumed by one `match (o)`
// is released by consumed_rcpayload_option_frees → emit_opt_payload_drop, which
// dec'd the payload BOX and the option box. For a scalar-only struct that is
// complete; for a struct with an rc-ARRAY field it freed the box and stranded
// the buffer — and because the drop zeroes the slot, the exit sweep's own
// OPTSTRUCT deep free then saw a null and never picked it up.
//
// 100 rounds, the leaked block identified by which dimension moves it:
//
//	struct P { xs: i32[2], n }   allocs 300  frees 200   4000
//	struct P { xs: i32[10], n }  allocs 300  frees 200  10400   <- array grew
//	struct P { xs: i32[2], n+4 } allocs 300  frees 200   4000   <- struct did not
//
// so the unfreed block is the FIELD BUFFER, not the payload box. (The #6127
// sweep recorded this shape as a leaked struct box; the size sweep above is what
// corrected it, and it changed the fix from "free the box" to "drop the fields".)
//
// The REBOUND sibling has deep-dropped the same shape since #6252
// (emit_optstruct_deep_free → __struct_drop_<P>), which is also the evidence
// that the construction alias-incs the field: the comment on
// rcpayload_option_cand used to assert a deep drop here would over-release, and
// the underflow counter says otherwise (see the hazards test below).

func TestSelfHostOptStructPayloadDropX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// The minimal shape: the arm reads only a SCALAR field, so nothing
			// even mentions the buffer that leaked.
			name: "single_bind_scalar_field_read",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
		{
			// `.len()` on the array field — a borrow, and the drop must still run.
			name: "single_bind_field_len",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.xs.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 34,
		},
		{
			// An indexed read of the field — the other borrow the escape walker
			// must admit.
			name: "single_bind_element_read",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.xs[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 70,
		},
		{
			// A 10-element field: the case that identified the leaked block, and
			// the one that fails loudest if the drop reverts to box-only.
			name: "single_bind_bigger_array_field",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i, i, i, i, i, i, i, i, i], n: i });
    match (o) { Some(p) => { acc = p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
		{
			// Result's Ok arm — the candidate is admitted for Ok/Err too, and the
			// slot type Result[P, string] cannot name the payload struct, so the
			// type has to ride in the drop record rather than be read back off the
			// slot.
			name: "result_ok_struct_payload",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Result[P, string] = Ok(P { xs: [i, i + 1], n: i });
    match (o) { Ok(p) => { acc = p.n + p.xs.len(); }, Err(e) => { acc = e.len(); } }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 4,
		},
		{
			// A STRING field beside the array one: __struct_drop_<P>'s k_str arm
			// frees it rc-aware. A literal's .rodata data is heap-guard-skipped, so
			// a wrong second release would tick the underflow counter — which the
			// hazards test asserts stays at zero.
			name: "string_field_beside_the_array_field",
			src: `struct P { xs: i32[], s: string }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], s: "hello" });
    match (o) { Some(p) => { acc = p.xs.len() + p.s.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 36,
		},
		{
			// Two Some arms, one guarded. The drop runs after the whole match on a
			// box whose variant is statically known, so a guard changes nothing
			// about WHAT is freed — but every Some arm has to be checked for a
			// moved field, not just the first.
			name: "guarded_arm",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) {
        Some(p) when p.n > 50 => { acc = p.n; },
        Some(p) => { acc = p.xs.len(); },
        None => {}
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
		{
			// The other half of the admission split: a SCALAR-ONLY struct payload
			// has no rc field, so the shallow box dec is complete and must stay —
			// emitting __struct_drop_<P> for it would be a call with nothing to do
			// at best, and this pins that the two shapes keep their own drops.
			name: "scalar_only_struct_payload_stays_balanced",
			src: `struct P { n: i32, m: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { n: i, m: i });
    match (o) { Some(p) => { acc = p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osp_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d", exit, tc.want)
			}
			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
			if summary == "" {
				t.Fatalf("no leakcheck summary")
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if allocs == 0 {
				t.Fatalf("allocated nothing — the probe is not exercising the path")
			}
			if live != 0 {
				t.Errorf("live_bytes=%d, want 0 — the payload struct's array field leaks "+
					"once per round, so this scales with the loop count", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

// TestSelfHostOptStructPayloadDropHazardsX86_64 — the deep drop frees the
// payload's rc FIELDS, so it needs a stronger property than the box dec did:
// no field may be moved out of the arm binding. `binding_escapes_arm` proves
// only that the payload BOX does not outlive the match, and `held = p.xs`
// satisfies that while handing `held` the buffer the deep drop would free.
//
// Each case pairs a live reference to a moved field with a read AFTER the match
// and asserts BEHAVIOUR: a wrong answer or a crash means the drop freed a value
// something else still holds. They are worth more than a leak assertion here —
// while writing this, the deep drop landed WITHOUT the field-escape gate and
// every one of these still exited correctly, purely because the freelist had
// not yet reused the block. What gave it away was the free COUNT running ahead
// of native's, which is why the counts are asserted too.
//
// Every `want` is from `fern -interp`.
func TestSelfHostOptStructPayloadDropHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
		// maxFrees bounds the releases: the shape leaks by design (the field
		// moved out is not reclaimed), so exceeding this is an over-release the
		// exit code alone may not surface.
		maxFrees int64
	}{
		{
			name: "field_extracted_to_an_outer_local",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { held = p.xs; acc = p.n; }, None => {} }
    return acc + held[0] + held.len();
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:     57,
			maxFrees: 300,
		},
		{
			name: "field_appended_into_a_container",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var keep: i32[][] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { keep = keep.append(p.xs); acc = p.n; }, None => {} }
    return acc + keep[0][1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:     40,
			maxFrees: 400,
		},
		{
			name: "field_passed_to_a_callee_that_keeps_it",
			src: `struct P { xs: i32[], n: i32 }
function keepit(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { held = keepit(p.xs); acc = p.n; }, None => {} }
    return acc + held[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:     40,
			maxFrees: 300,
		},
		{
			// The whole payload box escapes — refused one level up, by the
			// box-level gate, and nothing is released at all.
			name: "whole_payload_struct_escapes",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var hp: P = P { xs: [0], n: 0 };
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { hp = p; acc = p.n; }, None => {} }
    return acc + hp.xs[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:     40,
			maxFrees: 0,
		},
		{
			// The consuming match in a NESTED block routes through the precise-drop
			// path (a drop KIND rather than a drop record), so the gate has to be
			// applied on both sides.
			name: "nested_block_match_field_extracted",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    if (i >= 0) {
        match (o) { Some(p) => { held = p.xs; acc = p.n; }, None => {} }
    }
    return acc + held[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:     40,
			maxFrees: 100,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osph_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"deep drop freed a field the arm moved out (use-after-free)", exit, tc.want)
			}
			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
			if summary == "" {
				t.Fatalf("no leakcheck summary")
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if frees > tc.maxFrees {
				t.Errorf("frees=%d, want at most %d (allocs=%d live=%d) — the extra release "+
					"is the moved field being freed under a live reference", frees, tc.maxFrees, allocs, live)
			}
		})
	}
}

// TestSelfHostOptStructPayloadDropNoUnderflowX86_64 — the deep drop rests on the
// struct construction having alias-inc'd its rc fields, which the comment on
// rcpayload_option_cand denied ("a __struct_drop_<T> deep-drop here would
// OVER-RELEASE them"). An over-release lands on a box whose count is already
// zero, which the runtime counts rather than crashes on, so the counter is the
// direct test of the claim. Both programs return it; 0 is the assertion.
func TestSelfHostOptStructPayloadDropNoUnderflowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{
			name: "array_field",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len() + p.xs[0]; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`,
		},
		{
			// A string LITERAL field is the sharpest version: its data lives in
			// .rodata and the heap guard skips it, so a double release shows up
			// only here.
			name: "string_literal_field",
			src: `struct P { xs: i32[], s: string }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], s: "hello" });
    match (o) { Some(p) => { acc = p.xs.len() + p.s.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "ospu_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != 0 {
				t.Errorf("__rc_underflow_count() == %d, want 0 — the deep drop released a "+
					"payload field the construction did not retain", exit)
			}
		})
	}
}

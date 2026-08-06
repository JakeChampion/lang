package e2eselfhost

import (
	"strings"
	"testing"
)

// --- A `string[]` field is reclaimable too (#6127) ---------------------------
//
// `rcpayload_option_cand` admits a struct payload under
// `struct_is_scalar_only || nested_field_deep_drop_ok`. `nddo_reach` credits an
// rc-array, a struct/enum-array, a nested struct and a bare `string` — but not a
// `string[]`. So `P { xs: string[], n: i32 }` reached neither disjunct, the
// candidate was refused outright, and NOTHING was released.
//
// The field's element type was the only variable, 100 rounds:
//
//	xs: i32[]                        300 / 300      0
//	s:  string                       300 / 300      0
//	xs: string[]                     500 /   0  17600
//	xs: string[]  bound BARE         400 / 400      0
//
// and the discriminating probe is the last row plus this one: adding an `i32[]`
// beside the `string[]` admits the identical type and reclaims BOTH arrays, with
// `__struct_drop_P` emitted. So the drop side was already complete and only the
// admission was short — which is why the fix reuses `struct_has_strarr_field` at
// the admission rather than adding an arm to `nddo_reach`, whose verdict also
// decides deep-vs-shallow for nested-struct fields in all three backends'
// `__struct_drop` emission (the surface #6148's use-after-free came from).
//
// Whether the deep drop fires is still decided at emit time by
// `struct_routes_field_reclaim`, whose strarrfld verdict is whole-program — see
// the partial-reclaim case below, which is admitted, releases its boxes, and
// leaves the string[] alone because a read elsewhere in the program disqualifies
// the type. That fallback is the design, not a gap.

func TestSelfHostOptStrArrFieldX86_64(t *testing.T) {
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
			// The shape that released nothing at all.
			name: "strarr_field_only",
			src: `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
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
			// The discriminating case: an i32[] beside the string[] was already
			// admitted, via the OTHER disjunct, and reclaims both arrays. It is the
			// control that proves the drop side was never the problem.
			name: "strarr_field_beside_an_i32_array",
			src: `struct P { xs: string[], ys: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: ["alpha", "beta"], ys: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len() + p.ys.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 38,
		},
		{
			// The two neighbours that already worked, kept so a regression on the
			// existing disjuncts fails here too.
			name: "i32_array_field_unchanged",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
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
			name: "bare_string_field_unchanged",
			src: `struct P { s: string, n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { s: "alpha", n: i });
    match (o) { Some(p) => { acc = p.n + p.s.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 55,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osaf_"+tc.name, asm)
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
				t.Errorf("live_bytes=%d, want 0 — the option box, the payload box, the "+
					"string[] buffer and its element boxes all leak once per round", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

// TestSelfHostOptStrArrFieldHazardsX86_64 — the string[] field extracted out of
// the arm binding, and a string ELEMENT extracted through it. All four must keep
// their answers, and none may release the extracted value.
//
// These assert the underflow counter rather than an exact free count, because the
// admission legitimately MOVES the counts (a shape that released nothing now
// releases its option and payload boxes) and a count alone cannot say whether the
// extra release was the safe one. The counter can: an over-release lands on a box
// whose count is already zero. Each case also still leaks the extracted value,
// which is the positive evidence that the deep drop was declined.
//
// Every `want` is from `fern -interp`.
func TestSelfHostOptStrArrFieldHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{
			name: "strarr_field_extracted_to_a_local",
			body: `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    var held: string[] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { held = p.xs; acc = p.n; }, None => {} }
    return acc + held.len() + held[0].len();
}`,
			want: 6,
		},
		{
			name: "strarr_field_into_a_container",
			body: `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    var keep: string[][] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { keep = keep.append(p.xs); acc = p.n; }, None => {} }
    return acc + keep[0][1].len();
}`,
			want: 38,
		},
		{
			name: "strarr_field_to_a_callee_that_keeps_it",
			body: `struct P { xs: string[], n: i32 }
function keepit(xs: string[]): string[] { return xs; }
function round(i: i32): i32 {
    var held: string[] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { held = keepit(p.xs); acc = p.n; }, None => {} }
    return acc + held[1].len();
}`,
			want: 38,
		},
		{
			// A string ELEMENT read out through the field. `p.xs[0]` is an indexed
			// read, which the escape walker admits as a borrow — but the element is a
			// BOX, not a value copy, so the deep drop freeing the array would dangle
			// it. This one is worth more than the others: it is the shape where the
			// borrow vocabulary and the runtime representation could disagree.
			name: "string_element_extracted_through_the_field",
			body: `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    var hs: string = "";
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { hs = p.xs[0]; acc = p.n; }, None => {} }
    return acc + hs.len();
}`,
			want: 55,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The answer, with leakcheck on so the leak evidence is available too.
			valueSrc := tc.body + `
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
			asm := hevCompile(t, runner, driverBin, valueSrc, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osafh_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"deep drop released a string[] (or one of its element boxes) that the "+
					"arm moved out", exit, tc.want)
			}
			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if live == 0 {
				t.Errorf("live_bytes=0 (allocs=%d frees=%d) — the extracted value is expected "+
					"to LEAK here; nothing live means the deep drop was granted after all",
					allocs, frees)
			}

			// The same program returning the underflow counter. A moved value released
			// under a live reference lands on a zero count, which no exit code shows.
			ufSrc := tc.body + `
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`
			ufAsm := hevCompile(t, runner, driverBin, ufSrc, nil)
			ufBin := buildBin(t, gcc, dir, "osafu_"+tc.name, ufAsm)
			_, ufExit := hevRun(t, runner, ufBin)
			if ufExit != 0 {
				t.Errorf("__rc_underflow_count() == %d, want 0 — a box released twice", ufExit)
			}
		})
	}
}

// TestSelfHostOptStrArrFieldPartialReclaimX86_64 — the emit-time fallback, pinned
// because it looks like a bug and is the design.
//
// This program reads a string ELEMENT (`p.xs[0].len()`), which disqualifies the
// type in the whole-program strarrfld scan. The candidate is still admitted, so
// the option box and the payload box are released — but `struct_routes_field_reclaim`
// refuses at emit time, `emit_struct_field_drops` emits nothing, and the string[]
// stays live. Partial reclaim, no over-release: strictly better than the zero
// releases this shape had, and the counter confirms which side of the line it is on.
func TestSelfHostOptStrArrFieldPartialReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	body := `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len() + p.xs[0].len(); }, None => {} }
    return acc;
}`
	asm := hevCompile(t, runner, driverBin, body+`
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "osafp", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 0 {
		t.Fatalf("__rc_underflow_count() == %d, want 0", exit)
	}
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if frees == 0 {
		t.Errorf("frees=0 (allocs=%d) — the candidate should still be admitted and free "+
			"the option and payload boxes even when the field drop is declined", allocs)
	}
	if frees == allocs {
		t.Errorf("allocs=%d frees=%d — this shape is expected to reclaim only PARTIALLY; "+
			"a full balance means the whole-program strarrfld gate stopped declining and "+
			"this case no longer pins the fallback", allocs, frees)
	}
}

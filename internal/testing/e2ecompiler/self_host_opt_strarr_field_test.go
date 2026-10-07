package e2ecompiler

import (
	"strings"
	"testing"
)

// --- A `string[]` field is reclaimable too (#6127) ---------------------------
//
// An `Option[P]` whose struct payload has a `string[]` field must release the
// box, the payload and the array, as it does for an `i32[]` field or a bare
// `string`. The field's element type is the only variable, 100 rounds:
//
//	xs: i32[]                        300 / 300      0
//	s:  string                       300 / 300      0
//	xs: string[]  (unreleased)       500 /   0  17600

func TestSelfHostOptStrArrFieldX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// The string[] field alone.
			name: "strarr_field_only",
			src: `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 4,
		},
		{
			// An i32[] beside the string[]: both arrays are reclaimed.
			name: "strarr_field_beside_an_i32_array",
			src: `struct P { xs: string[], ys: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: ["alpha", "beta"], ys: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len() + p.ys.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 38,
		},
		{
			// The two neighbouring field kinds, which must keep reclaiming.
			name: "i32_array_field_unchanged",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 4,
		},
		{
			name: "bare_string_field_unchanged",
			src: `struct P { s: string, n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { s: "alpha", n: i });
    match (o) { Some(p) => { acc = p.n + p.s.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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
// their answers, and none may release the extracted value while it is live.
//
// Each asserts a balanced census and the underflow counter: an over-release
// lands on a box whose count is already zero, which a count alone cannot see.
//
// Every `want` is from `fern -interp`.
func TestSelfHostOptStrArrFieldHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{
			name: "strarr_field_extracted_to_a_local",
			body: `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    let held: string[] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { held = p.xs; acc = p.n; }, None => {} }
    return acc + held.len() + held[0].len();
}`,
			want: 6,
		},
		{
			name: "strarr_field_into_a_container",
			body: `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    let keep: string[][] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { keep = keep.append(p.xs); acc = p.n; }, None => {} }
    return acc + keep[0][1].len();
}`,
			want: 38,
		},
		{
			name: "strarr_field_to_a_callee_that_keeps_it",
			body: `struct P { xs: string[], n: i32 }
@noinline function keepit(xs: string[]): string[] { return xs; }
function round(i: i32): i32 {
    let held: string[] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
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
    let hs: string = "";
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
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
    let x: i32 = 0;
    let r: i32 = 0;
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
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance", allocs, frees, live)
			}

			// The same program returning the underflow counter. A moved value released
			// under a live reference lands on a zero count, which no exit code shows.
			ufSrc := tc.body + `
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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

// TestSelfHostOptStrArrFieldPartialReclaimX86_64 reads a string ELEMENT through
// an Option's struct payload (`p.xs[0].len()`): the option box, the payload and
// the string[] are all released, with no over-release.
func TestSelfHostOptStrArrFieldPartialReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	body := `struct P { xs: string[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: ["alpha", "beta"], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len() + p.xs[0].len(); }, None => {} }
    return acc;
}`
	asm := hevCompile(t, runner, driverBin, body+`
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance", allocs, frees, live)
	}
}

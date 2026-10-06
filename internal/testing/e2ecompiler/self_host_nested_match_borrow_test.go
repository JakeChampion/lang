package e2ecompiler

import (
	"strings"
	"testing"
)

// --- A `match (o)` scrutinee is a borrow, wherever the match sits (#6127) -----
//
// `expr_unsafe_for` reports any bare ident as an escape, so the consuming match
// of an Option reads as an escape of the Option. `consumed_rcpayload_option_frees`
// has always disagreed — `name_escapes_outside_stmt` skips the consuming match —
// but it skips it by top-level statement INDEX, so the argument only reaches a
// match that IS a top-level statement.
//
// `precise_drop_names` covers the other case, where the match sits inside an `if`
// or a `while`, and it gated on the coarse `body_unsafe_for`. So the nested form
// was refused outright: not a partial release, NOTHING released, and the whole
// box leaked every round.
//
// The nesting was the only variable, 100 rounds:
//
//	match at top level        200 / 200      0
//	match inside an `if`      200 /   0   8000
//	match inside a `while`    200 /   0   8000
//	string payload, in `if`   200 /   0   6400
//	struct payload, in `if`   300 /   0  12800
//
// The struct-payload row also exercises the `"opt-structpayload:<P>"` drop kind
// added in #6308, which until now could not fire — the kind was computed and the
// candidate carrying it was refused one gate earlier.

func TestSelfHostNestedMatchBorrowX86_64(t *testing.T) {
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
			// The control: the same program with the match at top level always
			// reclaimed. It is here so a regression on the OLD path fails too.
			name: "match_at_top_level",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
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
			name: "match_inside_an_if",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
    }
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
			name: "match_inside_a_while",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    let k: i32 = 0;
    while (k < 1) {
        match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
        k = k + 1;
    }
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
			// The STRING payload kind — released by __fern_str_free rather than a
			// plain dec, so it is a distinct emission and needs its own case.
			name: "string_payload_inside_an_if",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[string] = Some("hello");
    if (i >= 0) {
        match (o) { Some(s) => { acc = s.len(); }, None => {} }
    }
    return acc + i;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 55,
		},
		{
			// The STRUCT payload kind — the "opt-structpayload:<P>" drop from #6308,
			// reachable for the first time.
			name: "struct_payload_inside_an_if",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    if (i >= 0) {
        match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
    }
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "nmb_"+tc.name, asm)
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
				t.Errorf("live_bytes=%d, want 0 — the whole option box and its payload leak "+
					"once per round, so this scales with the loop count", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

// TestSelfHostNestedMatchBorrowHazardsX86_64 — the scrutinee is a borrow, and it
// must exempt nothing else: an alias, a return, a call argument or an escaping
// arm binding keeps the value live past the match.
//
// These assert allocation and free counts as well as the exit. On the typed lowering every
// row reclaims what it allocates; an over-release shows up as a count above
// that even when the program still exits correctly. A
// sibling change in #6308 exited correctly on all four of its hazards while
// freeing a live buffer — the freelist had simply not reused it yet.
//
// Every `want` is from `fern -interp`.
func TestSelfHostNestedMatchBorrowHazardsX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name       string
		src        string
		want       int
		wantAllocs int64
	}{
		{
			// Aliased in the same block as the match. The initial None is
			// static; each round allocates only the Some box and its array.
			name: "aliased_beside_the_match",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let keep: Option[i32[]] = None;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        keep = o;
        match (o) { Some(a) => { acc = a[0]; }, None => {} }
    }
    match (keep) { Some(b) => { acc = acc + b[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			// Aliased AFTER the block — the drop point is the last top-level
			// reference, so this is the one that would free a box still to be read.
			name: "aliased_after_the_block",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { acc = a[0]; }, None => {} }
    }
    let keep: Option[i32[]] = o;
    match (keep) { Some(b) => { acc = acc + b[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			// Returned to the caller from the same function that matches it.
			name: "returned_to_the_caller",
			src: `function build(i: i32): Option[i32[]] {
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { if (a[0] < 0) { return None; } }, None => {} }
    }
    return o;
}
function round(i: i32): i32 {
    let g: Option[i32[]] = build(i);
    match (g) { Some(a) => { return a[0] + a[1]; }, None => {} }
    return 0;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			// Passed to a callee that keeps it. The initial None is static;
			// each round allocates only the Some box and its array.
			name: "passed_to_a_callee_that_keeps_it",
			src: `function keepit(o: Option[i32[]]): Option[i32[]] { return o; }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let held: Option[i32[]] = None;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        held = keepit(o);
        match (o) { Some(a) => { acc = a[0]; }, None => {} }
    }
    match (held) { Some(b) => { acc = acc + b[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			// The arm BINDING escapes to an outer local.
			name: "arm_binding_escapes_to_an_outer_local",
			src: `function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { held = a; acc = a[0]; }, None => {} }
    }
    return acc + held[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			// The arm binding escapes into a container that outlives the
			// match.
			name: "arm_binding_escapes_into_a_container",
			src: `function round(i: i32): i32 {
    let keep: i32[][] = [];
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { keep = keep.append(a); acc = a[0]; }, None => {} }
    }
    return acc + keep[0][1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 300,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "nmbh_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"scrutinee borrow laundered a genuine escape and the drop freed a box "+
					"something else still holds", exit, tc.want)
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
			if allocs != tc.wantAllocs || frees != tc.wantAllocs || live != 0 {
				t.Errorf("allocation census: allocs=%d frees=%d live=%d; want allocs=frees=%d and live=0",
					allocs, frees, live, tc.wantAllocs)
			}
		})
	}
}

// TestSelfHostNestedMatchBorrowNoUnderflowX86_64 — the array and string payload
// kinds released through a nested match, with the release counted rather than
// inferred. A string LITERAL payload is the sharp case: its data lives in
// .rodata and the heap guard skips it, so a double release registers only here.
func TestSelfHostNestedMatchBorrowNoUnderflowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
    }
    let s: Option[string] = Some("hello");
    let k: i32 = 0;
    while (k < 1) {
        match (s) { Some(t) => { acc = acc + t.len(); }, None => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`
	asm := hevCompile(t, runner, driverBin, src, nil)
	progBin := buildBin(t, gcc, dir, "nmbu", asm)
	_, exit := hevRun(t, runner, progBin)
	if exit != 0 {
		t.Errorf("__rc_underflow_count() == %d, want 0 — a box released twice", exit)
	}
}

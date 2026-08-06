package e2eselfhost

import (
	"strings"
	"testing"
)

// --- `.len()` on an array-of-structs / -tuples is a borrow (#6127) -----------
//
// `arrstruct_elem_esc_expr` and its tuple twin whitelisted `name[i].f.len()` —
// a `.len()` on an ELEMENT's array field — but a `.len()` on the LOCAL ITSELF
// fell through to the generic call tail, which recurses into the callee, hits
// the bare-ident arm, and reports an escape. One `name.len()` anywhere in the
// function therefore refused the whole ARRSTRUCT: / ARRTUP: credit.
//
// The symptom is backwards, which is what made it hard to see: reading LESS
// made it leak. Same array, same struct, 100 rounds —
//
//	ps[0].xs[0]   (element read)   300 / 300      0
//	ps[0].n       (scalar field)   300 / 300      0
//	ps.len()      (header read)    300 / 100   8000
//	ps.len() + ps[0].xs[0]         300 / 100   8000   <- one .len() poisons it
//
// It is not scope-related. This was first chased as a block-scoped gap (#6285
// left `ARRSTRUCT` and `ARRTUP` out on the grounds that switching their
// retired-name lookup changed nothing), and the top-level form turned out to
// leak identically — the predicate never reached the name lookup at all.
//
// The existing arrstruct/arrtup suites never caught it because every case there
// reads an element and none calls `.len()` on the array, and because they assert
// bump-allocator growth rather than alloc/free balance, which the freelist can
// mask.

func TestSelfHostArrStructLenBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d", name, exit, wantExit)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// The minimal shape: `.len()` is the ONLY use.
			name: "arrstruct_len_is_the_only_use",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    var ps: P[] = [P { xs: [r, r + 1] }];
    return ps.len();
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 17,
		},
		{
			// `.len()` alongside an element read — this is the one that shows the
			// old behaviour was a poison rather than a missing admission: the
			// element read on its own always reclaimed.
			name: "arrstruct_len_alongside_an_element_read",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    var ps: P[] = [P { xs: [r, r + 1] }];
    return ps.len() + ps[0].xs[0];
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
			// Bound through a local rather than used inline, so the fix cannot be
			// keyed on the return position.
			name: "arrstruct_len_bound_to_a_local",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    var ps: P[] = [P { xs: [r, r + 1] }];
    var n: i32 = ps.len();
    return n;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 17,
		},
		{
			// The tuple sibling, ARRTUP:.
			name: "arrtup_len_is_the_only_use",
			src: `function round(r: i32): i32 {
    var ts: (i32, i32[])[] = [(r, [r, r + 1])];
    return ts.len();
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 17,
		},
		{
			// Declared inside a loop — the shape #6285 measured at 35200 and left
			// open. It is the same cause, not a block-scoped one.
			name: "arrstruct_len_declared_in_a_loop",
			src: `struct P { xs: i32[], n: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var ps: P[] = [P { xs: [i, i + 1], n: i }];
        acc = acc + ps.len();
        i = i + 1;
    }
    return acc + r;
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
			// The ARRTUP twin of the above — 32000 in #6285.
			name: "arrtup_len_declared_in_a_loop",
			src: `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var ts: (i32, i32[])[] = [(i, [i, i + 1])];
        acc = acc + ts.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 38,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, tc.want)
			if live != 0 {
				t.Errorf("live_bytes=%d, want 0 — the element boxes and their array "+
					"fields leak once per round, so this scales with the loop count", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

// TestSelfHostArrStructLenBorrowHazardsX86_64 — `.len()` is a borrow, but it
// must not launder anything else in the same function. Each of these pairs a
// `.len()` with a genuine escape and asserts the credit is still refused;
// behaviour is the assertion, since a wrongly-granted credit frees a value
// something else still holds. Every `want` is from `fern -interp`.
func TestSelfHostArrStructLenBorrowHazardsX86_64(t *testing.T) {
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
			name: "len_plus_alias_to_an_outer_local",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    var keep: P[] = [];
    var ps: P[] = [P { xs: [r, r + 1] }];
    keep = ps;
    return ps.len() + keep[0].xs[0];
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
			name: "len_plus_return_to_the_caller",
			src: `struct P { xs: i32[] }
function build(r: i32): P[] {
    var ps: P[] = [P { xs: [r, r + 1] }];
    if (ps.len() > 0) { return ps; }
    return [];
}
function round(r: i32): i32 {
    var got: P[] = build(r);
    if (got.len() == 0) { return 0; }
    return got[0].xs[0];
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
			// The element's array field is extracted whole and outlives the array.
			name: "len_plus_element_field_extracted",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    var held: i32[] = [];
    var ps: P[] = [P { xs: [r, r + 1] }];
    held = ps[0].xs;
    return ps.len() + held[0];
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
			// The array itself is passed to a callee that keeps it.
			name: "len_plus_call_arg_the_callee_keeps",
			src: `struct P { xs: i32[] }
function keepit(ps: P[]): P[] { return ps; }
function round(r: i32): i32 {
    var ps: P[] = [P { xs: [r, r + 1] }];
    var held: P[] = keepit(ps);
    return ps.len() + held[0].xs[0];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 70,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "aslen_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"`.len()` borrow laundered a genuine escape and the reclaim freed a "+
					"value something else still holds", exit, tc.want)
			}
		})
	}
}

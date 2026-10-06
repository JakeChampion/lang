package e2ecompiler

import (
	"strings"
	"testing"
)

// --- The STRING-position assign-form tuple rebind (#7226) --------------------
//
// `t = (k, u)` where the tuple's annotation puts a string at position 1. The
// rebind must release the old tuple's string exactly once, whichever writer
// filled it, and must never release a box a view or a borrowed alias still
// needs.
//
// The leak these close is per ROUND, so `live_bytes` doubles with the loop
// bound; a bounded strand does not move. Every want was confirmed against BOTH
// oracles — bin/fern -interp and the native x86-64 backend.

type tupStrRebindCase struct {
	name string
	src  string
	want int
}

func tupStrRebindCases() []tupStrRebindCase {
	// The main shape at two round counts: a per-round leak doubles live_bytes
	// between them, which separates it from a bounded strand.
	repro := `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    let u: string = w("cd");
    t = (i + 1, u);
    return t.1.len() + s.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < ROUNDS) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`

	return []tupStrRebindCase{
		{name: "str_pos_rebind_100", src: strings.Replace(repro, "ROUNDS", "100", 1), want: 31},
		{name: "str_pos_rebind_200", src: strings.Replace(repro, "ROUNDS", "200", 1), want: 62},
		{
			// Both writers bound before the tuple, so the var site and the rebind
			// name two locals of the same producer. This was the recorded
			// "string_pos_rebind_refused" hazard; the answer is unchanged and the
			// leak is gone, so it moves here.
			name: "str_pos_two_fresh_writers",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s1: string = w("ab");
    let s2: string = w("cd");
    let t: (i32, string) = (i, s1);
    t = (i + 1, s2);
    return t.0 + t.1.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 17,
		},
		{
			// ONE local at both writers. Both tuples retain it, so two releases
			// are owed on one box and the refcount arbitrates; a walk that freed
			// the box outright at the first would dangle the second.
			name: "one_source_both_writers",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    t = (i + 1, s);
    return t.1.len() + s.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 62,
		},
		{
			// Kinds "as": an array position and a string position in one tuple,
			// both rebound. The two halves release through one walk, so a kinds
			// string that admitted only one character class would drop the other.
			name: "str_and_arr_pos_rebind",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s: string = w("ab");
    let xs: i32[] = [i, i + 1];
    let t: (i32[], string) = (xs, s);
    let u: string = w("cd");
    let ys: i32[] = [i + 2, i + 3];
    t = (ys, u);
    return t.1.len() + t.0[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 39,
		},
		{
			// Both source locals read AFTER the rebind. The walk gives back only
			// the tuple's reference, so each local's own box must still be live
			// here — the case a source-releasing walk corrupts.
			name: "both_sources_read_after_rebind",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    let u: string = w("cd");
    t = (i + 1, u);
    return t.1.len() + s.len() + u.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 4,
		},
		{
			// The rebind on one branch only: on the untaken branch the store's cow
			// guard and the element walk's null guard must leave the var site's
			// tuple alone, and the scope-exit sweep still owes exactly one release.
			name: "str_pos_cond_rebind",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    let u: string = w("cd");
    if (i % 2 == 0) { t = (i + 1, u); }
    return t.1.len() + s.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 62,
		},
		{
			// The ARRAY-position control, unchanged by the widening: it released
			// before this change and must release identically after.
			name: "arr_pos_rebind_control",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 2, i + 3];
    let t: (i32, i32[]) = (i, xs);
    t = (i + 1, ys);
    return t.1[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 8,
		},
	}
}

// tupStrRebindHazards — writers that store a static literal, a borrowed alias
// or a view at the string position. Each is pinned by answer plus
// __rc_underflow_count(): a doubly-released block returns to the freelist, so
// the byte count and the sum both still come out right and only the counter
// separates the two readings.
func tupStrRebindHazards() []tupStrRebindCase {
	return []tupStrRebindCase{
		{
			// A string LITERAL at the rebind's rc position. The class gate wants a
			// bare ident there, so the rebind earns no release at all; what this
			// row pins is that the var site's own kinds are not disturbed by it
			// (the literal's box is static, so agreement holds) and nothing
			// under-flows.
			name: "str_pos_literal_writer",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    t = (i + 1, "a-literal-string-payload-past-any-inline-threshold");
    return t.1.len() + s.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 63,
		},
		{
			// A borrowed ALIAS: `u` names the box `b` still holds. Refused because
			// the writer test proves ownership positively, and `b`'s own release
			// would otherwise be racing the tuple's.
			name: "str_pos_alias_writer",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, string) = (i, s);
    let b: string = w("cd");
    let u: string = b;
    t = (i + 1, u);
    return t.1.len() + s.len() + b.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 4,
		},
		{
			// A VIEW: the rebind stores a `slice_unchecked` result, which shares the
			// receiver's buffer. This is the writer the restriction was written
			// for, and it must stay refused — an owned-string release here would
			// claim a box the view's own sweep still owns. The element is read
			// through the source local rather than `t.1`, which keeps the row off
			// the separate `str`-spelled tuple-element divergence recorded in
			// docs/rc-log/2026-09-03-tuple-str-rebind-writer-agreement.md.
			name: "str_pos_slice_view_writer",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (s: string) tail(n: i32): str { return slice_unchecked(s, n, s.len()); }
function round(i: i32): i32 {
    let s: string = w("ab");
    let t: (i32, str) = (i, s);
    let b: string = w("cd");
    let u: str = b.tail(2);
    t = (i + 1, u);
    return t.0 + u.len() + s.len() + b.len();
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 35,
		},
	}
}

// TestSelfHostTupleStrRebindX86_64 — the admitted shapes balance exactly, and a
// sanitizer leg re-runs each. allocs == frees is essential in both
// directions: short is the leak this closes, above is a double free.
func TestSelfHostTupleStrRebindX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range tupStrRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
			stderr, exit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "tupstr", asm))
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (both oracles agree on %d; an offset of "+
					"+N is N rc underflows)", tc.name, exit, tc.want, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does). The "+
					"retain is per round, so anything stranded scales with the loop "+
					"bound; compare the 100- and 200-round rows", tc.name, summary)
			}
			tupStrRebindSanitize(t, cli, tc)
		})
	}
}

// TestSelfHostTupleStrRebindHazardsX86_64 — the refused writers, each answered
// correctly with a zero underflow count under the sanitizer too. These assert
// answers, not leak counts.
func TestSelfHostTupleStrRebindHazardsX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range tupStrRebindHazards() {
		t.Run(tc.name, func(t *testing.T) {
			if exit, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); exit != tc.want {
				t.Errorf("%s exited %d, want %d (an offset of +N is N rc underflows — "+
					"a wrongly granted credit, not a wrong sum)", tc.name, exit, tc.want)
			}
			tupStrRebindSanitize(t, cli, tc)
		})
	}
}

func tupStrRebindSanitize(t *testing.T, cli *strictCLI, tc tupStrRebindCase) {
	t.Helper()
	asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_SANITIZE=1")
	sanErr, sanExit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "tupstrsan", asm))
	if sanExit != tc.want {
		t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
	}
	if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
		t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
	}
}

// TestSelfHostTupleStrRebindWasmIR — the wasm sibling. Exit codes only:
// FERN_LEAKCHECK is x86-64-only, and the answer (with the underflow count folded
// in) is what proves the releases claimed no live box.
func TestSelfHostTupleStrRebindWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range append(tupStrRebindCases(), tupStrRebindHazards()...) {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("tuple string-position rebind wasm %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleStrRebindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleStrRebindIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range append(tupStrRebindCases(), tupStrRebindHazards()...) {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

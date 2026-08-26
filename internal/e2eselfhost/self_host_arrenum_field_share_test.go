package e2eselfhost

import (
	"testing"
)

// --- The struct-literal FIELD share of an array-of-enums local ---------------
//
// The arrenum twin of the arrstruct counted field share. `var p: P = P { f: xs, … }`
// where `xs` is a credited `E[]` local: the construction RETAINS it — the
// ExprStructLit fallback arm alias-incs any bare arr-slot ident whose field type
// is an array, which covers `E[]` even though the gate above it names only
// scalar- and struct-arrays — so the field holds a COUNTED share. Every escape
// gate on the ARRENUM credit read that bare ident as an escape anyway, and `xs`
// lost its reclaim: 450 allocs / 350 frees, 4000 bytes over 100 rounds, against
// native's 450/450.
//
// Both releases are rc-gated, which is what makes granting it safe. The holder's
// walk (__enum_arr_elems_drop_<E>) already is_unique-gated the buffer; the
// source's (emit_arrenum_deep_free) now does too. That gate is a no-op for every
// shape that existed before — a sole owner is rc 1, verified across the whole
// probe corpus before anything was lifted — and the prerequisite for this one.
// It matters more here than on the struct side because this walk FREES each
// element box rather than deccing it, so two owners both walking is a double free.
//
// TWO PRECONDITIONS, and this class hides them better than any other:
//
//   - `respread`: `P { ...q, … }` copies the buffer pointer into a third box with
//     NO inc, so three owners sit at rc 2. Without the gate: exit 99 at 600 allocs,
//     600 frees, live_bytes 0.
//   - `moved_ret`: the retain is MOVE-gated (#6726), so at a move site the box
//     takes over the local's reference and both the inc and the sweep dec are
//     dropped. `return P { f: xs, … }` is that shape — the return is xs's last use.
//
// The second is the dangerous one, because NOTHING counts it. Without the gate,
// `moved_ret` measures 500 allocs / 400 frees — MORE frees than the correct
// 500/100, since a double free counts as a free — and `__rc_underflow_count()`
// stays silent, because this class frees element boxes rather than deccing them.
// The arm64 stage-2 fixpoint does not see it either: the compiler's own source has
// no enum-array moved share, so gen2 stays green. `moved_uaf` below is what
// catches it — it reads the payload back after the callee returned and checks the
// value, and without the gate the self-host binary SEGFAULTS (139) where native
// and interp both exit 25.
//
// So: a wrong-ANSWER case, not a census case. Every want was confirmed against
// BOTH oracles — bin/fern -interp and the native x86-64 backend agreed on each —
// never read off the self-host run under test.

type arrenumShareCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

const arrenumShareMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrenumShareDecl = `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
`

func arrenumShareCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// The repro: the share is in a branch taken half the time, so the
			// rounds that skip it are the ones that leaked. 450/350 before.
			name: "conditional",
			src: arrenumShareDecl + `function round(i: i32): i32 {
    var src: E[] = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = p.f.len() + p.n; }
    return t % 101;
}` + arrenumShareMain,
			want: 10, balance: true,
		},
		{
			// The share always runs and the source outlives it. The buffer gate is
			// what lets the two owners decide between them.
			name: "always",
			src: arrenumShareDecl + `function round(i: i32): i32 {
    var src: E[] = mkv(i);
    var p: P = P { f: src, n: i };
    return (p.f.len() + p.n + src.len()) % 101;
}` + arrenumShareMain,
			want: 69, balance: true,
		},
		{
			// The holder goes to a callee that may keep it. The source finding
			// rc > 1 simply declines.
			name: "holder_escapes",
			src: arrenumShareDecl + `function keepit(p: P): i32 { return p.f.len() + p.n; }
function round(i: i32): i32 {
    var src: E[] = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = keepit(p); }
    return (t + src.len()) % 101;
}` + arrenumShareMain,
			want: 27, balance: true,
		},
		{
			// PRECONDITION 1, the one the census does see: without the respread
			// gate this is exit 99 at 600/600, live_bytes 0.
			name: "respread",
			src: arrenumShareDecl + `function round(i: i32): i32 {
    var src: E[] = mkv(i);
    var q: P = P { f: src, n: i };
    var p: P = P { ...q, n: i + 1 };
    return (p.f.len() + p.n + q.n) % 101;
}` + arrenumShareMain,
			want: 70, balance: true,
		},
		{
			// PRECONDITION 2, the census-invisible one. Refused, so it stays the
			// leak it was — 500/100. Note that REMOVING the gate takes it to
			// 500/400, which reads as an improvement and is a double free.
			name: "moved_ret",
			src: arrenumShareDecl + `function hold(i: i32): P {
    var src: E[] = mkv(i);
    return P { f: src, n: i };
}
function round(i: i32): i32 { var p: P = hold(i); return (p.f.len() + p.n) % 101; }` + arrenumShareMain,
			want: 70,
		},
		{
			// The case that actually catches precondition 2: read the payload back
			// after the callee returned, with allocation churn in between so freed
			// memory is reused, and check the value. Without the move gate the
			// self-host binary segfaults (139) here while native and interp exit 25
			// — and both leak counters stay silent throughout.
			name: "moved_uaf",
			src: arrenumShareDecl + `function hold(i: i32): P {
    var src: E[] = mkv(i);
    return P { f: src, n: i };
}
function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var p: P = hold(i);
    var junk: i32 = churn(i * 7 + 3);
    var t: i32 = 0;
    match (p.f[0]) {
        E.A(xs) => { t = xs[0] + xs[1]; },
        E.B => { t = 0 - 1; }
    }
    if (t != i + i + 1) { return 0 - 1; }
    return t % 101;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    var bad: i32 = 0;
    while (i < 200) {
        var r: i32 = round(i);
        if (r < 0) { bad = bad + 1; }
        t = t + r;
        i = i + 1;
    }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 25,
		},
	}
}

// TestSelfHostArrEnumFieldShareX86_64 — a counted struct-literal field share keeps
// an array-of-enums source its rc-gated element walk, and the two shapes whose
// share count is incomplete keep refusing it.
func TestSelfHostArrEnumFieldShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrenumShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumshare_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = the payload "+
					"read back wrong; 139 = it read freed memory)", tc.name, exit, tc.want)
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
			// The two refused cases stay leaks, deliberately: the source keeps no
			// walk where the share count is incomplete.
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}

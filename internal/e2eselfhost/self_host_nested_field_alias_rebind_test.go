package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"
)

// An EXPLICITLY aliased nested-struct field in a self-rebind — `o = S { xs:
// o.xs.append(i), inner: o.inner, n: i }` — cost the local its whole reclaim
// credit, where the `...o` spelling of the same update kept it (#6623).
//
// The NODEEP field-move scan reports a bare non-scalar field read in a
// struct-literal field value as a MOVE out of the local, so `o` lost the deep
// drop AND the consume-rebind's `__field_reclaim_S` with it: every superseded
// box was stranded along with the `xs` buffer it owned, which is why the leak
// grew with the iteration count rather than being flat in it.
//
// A nested-struct / enum field value is the one class the struct-literal
// override path retains UNCONDITIONALLY (only a fresh `I { … }` literal or a
// fresh variant ctor is handed over uncounted), so the successor box holds a
// counted reference and the superseded box's deep drop decs a dup. Array and
// string field values take a CONDITIONAL retain, so they stay unexempt — the
// `array_field_alias_not_exempt` row below is the shape #6628 tracks, kept here
// so widening the exemption without widening the retain shows up as a wrong
// answer rather than as a byte count.

func nestedAliasRebindSrc(k int, spelling string) string {
	return fmt.Sprintf(`struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9] }, n: 0 };
    var i: i32 = 0;
    while (i < k) {
        %s
        i = i + 1;
    }
    return o.xs.len() + o.inner.tag;
}
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}
`, spelling, k)
}

const (
	nestedAliasExplicit = `o = S { xs: o.xs.append(i), inner: o.inner, n: i };`
	nestedAliasSpread   = `o = S { ...o, xs: o.xs.append(i), n: i };`
)

// The enum sibling: `e: o.e` takes the same unconditional retain the
// nested-struct field does. The payload is read back through a MATCH, which is a
// borrow — handing `o.e` to a call would be a genuine move position and would
// (correctly) still withhold the drop.
const nestedAliasEnumSrc = `enum E { A(i32[]), B }
struct S { xs: i32[], e: E, n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], e: A([7]), n: 0 };
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), e: o.e, n: i };
        i = i + 1;
    }
    var acc: i32 = o.xs.len();
    match (o.e) { A(d) => { acc = acc + d[0]; }, B => { acc = acc + 1; } }
    return acc;
}
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}
`

// THE OVER-RELEASE PROBE. `keep` holds the same inner box the loop keeps
// aliasing forward, and is read AFTER the loop — so granting the deep drop over
// a field whose retain went missing shows up as a wrong exit rather than as a
// balanced byte count.
const nestedAliasKeepLiveSrc = `struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 3, data: [9] }, n: 0 };
    var keep: I = o.inner;
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: o.inner, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.inner.tag + keep.tag + keep.data[0];
}
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}
`

// #6628's shape: an ARRAY field aliased explicitly. The override path's array
// retain is conditional, so this position is NOT exempt and the local still
// loses its credit — it leaks. Every carried buffer is read back, so the row
// fails loudly if the exemption is widened without widening the retain first.
const nestedAliasArrayFieldSrc = `struct S { xs: i32[], ys: i32[], n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], ys: [5, 6, 7], n: 0 };
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), ys: o.ys, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.ys.len() + o.ys[2];
}
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}
`

func TestSelfHostNestedFieldAliasRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	// counts compiles `src` through the self-host IR driver and returns the
	// leakcheck triple, having first agreed the exit code with `fern -interp`:
	// these shapes read every carried buffer back, so an over-release is a wrong
	// answer and that matters more than the bytes.
	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — a carried "+
				"field was released under a live reference", name, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", name)
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

	// The two spellings mean the same thing, so they must cost the same. Before
	// the fix the explicit one was 280/170/10160 against the spread's 280/260/800.
	t.Run("explicit_alias_matches_spread", func(t *testing.T) {
		ea, ef, el := counts(t, "alias_explicit", nestedAliasRebindSrc(8, nestedAliasExplicit))
		sa, sf, sl := counts(t, "alias_spread", nestedAliasRebindSrc(8, nestedAliasSpread))
		if ea != sa || ef != sf || el != sl {
			t.Errorf("explicit `inner: o.inner` = %d/%d/%d, spread `...o` = %d/%d/%d — "+
				"both spellings carry the same field and must reclaim identically",
				ea, ef, el, sa, sf, sl)
		}
	})

	// The defect was PER-ITERATION: every superseded box, and the xs buffer it
	// owned, was stranded. So the discriminator is the k curve, not the absolute
	// count — 1760 / 2480 / 10160 / 98480 before, one flat residual after. That
	// residual is the per-CALL nested-field carry of #6605, not this defect.
	t.Run("flat_in_k", func(t *testing.T) {
		var first int64 = -1
		for _, k := range []int{1, 2, 8, 32} {
			_, _, live := counts(t, fmt.Sprintf("alias_explicit_k%d", k), nestedAliasRebindSrc(k, nestedAliasExplicit))
			if first < 0 {
				first = live
				continue
			}
			if live != first {
				t.Errorf("k=%d leaked %d bytes against %d at k=1 — the per-iteration "+
					"strand is back: the local lost its __field_reclaim_S credit", k, live, first)
			}
		}
	})

	t.Run("enum_field_alias", func(t *testing.T) {
		_, _, live := counts(t, "alias_enum", nestedAliasEnumSrc)
		if live > 800 {
			t.Errorf("enum field alias leaked %d bytes — an enum field value takes the "+
				"same unconditional override retain a nested-struct one does, so the "+
				"local keeps its deep drop", live)
		}
	})

	// Exit agreement inside counts is the assertion here; the balance is not
	// claimed, only that nothing is freed under `keep`.
	t.Run("keep_alias_live", func(t *testing.T) {
		counts(t, "alias_keep_live", nestedAliasKeepLiveSrc)
	})

	// #6628: pins the exit code only. When the array retain widens, this row
	// becomes a flatness assertion like the ones above.
	t.Run("array_field_alias_not_exempt", func(t *testing.T) {
		_, _, live := counts(t, "alias_array_field", nestedAliasArrayFieldSrc)
		if live == 0 {
			t.Errorf("the array-field alias reclaimed fully — the override path's array " +
				"retain is conditional, so exempting the read without widening it hands " +
				"the successor an uncounted buffer (#6628)")
		}
	})
}

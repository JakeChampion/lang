package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Option[P] where P's only rc field is a `string` (#6360) -----------------
//
// The OPTSTRUCT class — the fresh, non-escaping `Option[<struct>]` local whose
// exit sweep / loop-rebind deep-frees the payload's fields, its box, and the
// option box — admitted a payload struct only when it carried an rc-ARRAY field
// (`struct_has_reclaim_array_field`). A payload whose sole reclaimable field is a
// bare `string` matched nothing: no credit, so no drop of any kind, so the string
// AND both boxes leaked. 48000 bytes over 100 rounds, exactly x2.0 per doubling,
// against 0 on native.
//
// Nothing downstream needed widening — the leak was entirely in admission:
//
//   - `__struct_drop_<P>`'s k_str arm has freed string fields in all three
//     backends since #4355, and the arm binding escape analysis
//     (`optstruct_arm_expr_escapes`) is field-TYPE generic: a bare `p.name`
//     extraction escapes because `string` is non-scalar, `p.name.len()` is a
//     borrow. The same struct bound as a BARE local has been reclaimed since
//     #4357, and a payload carrying a string field ALONGSIDE an array field was
//     already clean — the array field alone made the whole struct admissible.
//
//   - The string half is gated a SECOND time at emit, by
//     `struct_routes_field_reclaim`'s whole-program STRFLDOK verdict. A type that
//     scan refuses emits no `__struct_drop_<P>` call at all, so the payload box
//     and the option box are still freed and only the string is stranded. The
//     construction-side retain reads the same verdict, so an ALIASED string field
//     is inc'd at construction and the k_str dec is balanced rather than a
//     double-release.

func TestSelfHostOptStructStringFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the payload drop "+
				"reached a live string", name, exit, want)
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
		t.Logf("%s: allocs=%d frees=%d live_bytes=%d", name, allocs, frees, live)
		return allocs, frees, live
	}

	balanced := func(t *testing.T, name, src, was string) {
		t.Helper()
		allocs, frees, live := counts(t, name, src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance; %s",
				allocs, frees, live, was)
		}
	}

	// The headline row: block-scoped in a loop, the shape the leak list measured.
	t.Run("block_scoped_string_field", func(t *testing.T) {
		balanced(t, "oss_block", `struct P { name: string, n: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var o: Option[P] = Some(P { name: "a" + "b", n: i });
        match (o) { Some(p) => { acc = acc + p.n + p.name.len(); }, None => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`, "this was 48000, with frees=800 of 2400 allocs")
	})

	// The arm never touches the string field. The leak was identical either way —
	// admission never looked at the read — so this pins that it is not the read
	// that earns the reclaim.
	t.Run("payload_string_never_read", func(t *testing.T) {
		balanced(t, "oss_noread", `struct P { name: string, n: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var o: Option[P] = Some(P { name: "a" + "b", n: i });
        match (o) { Some(p) => { acc = acc + p.n; }, None => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`, "the string field alone must earn the OPTSTRUCT credit")
	})

	// Function-level single bind rather than a loop block.
	t.Run("fn_level_single_bind", func(t *testing.T) {
		balanced(t, "oss_fnlevel", `struct P { name: string, n: i32 }
function round(r: i32): i32 {
    var o: Option[P] = Some(P { name: "a" + "b", n: r });
    var acc: i32 = 0;
    match (o) { Some(p) => { acc = p.n + p.name.len(); }, None => {} }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`, "the fn-level bind leaked the same way as the block-scoped one")
	})

	// The loop-REBIND path (emit_optstruct_reclaim_store): each rebind must
	// deep-free the superseded box before the store.
	t.Run("rebound_in_a_loop", func(t *testing.T) {
		balanced(t, "oss_rebind", `struct P { name: string, n: i32 }
function round(r: i32): i32 {
    var o: Option[P] = Some(P { name: "a" + "b", n: 0 });
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        o = Some(P { name: "c" + "d", n: i });
        i = i + 1;
    }
    match (o) { Some(p) => { acc = p.n + p.name.len(); }, None => {} }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`, "every superseded box has to go at the rebind, not just the last one")
	})

	// A payload carrying BOTH kinds of field. This was already clean — the array
	// field alone admitted the struct — so it is the regression guard for the
	// widened predicate: the array half must still reclaim.
	t.Run("string_and_array_fields", func(t *testing.T) {
		balanced(t, "oss_both", `struct P { name: string, xs: i32[], n: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var o: Option[P] = Some(P { name: "a" + "b", xs: [i, i + 1], n: i });
        match (o) { Some(p) => { acc = acc + p.n + p.name.len() + p.xs[0]; }, None => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`, "the array half of the payload must still be reclaimed")
	})

	// A SCALAR-ONLY payload has nothing to deep-drop, so the reclaim is the two box
	// decs alone — and it was excluded for exactly that reason while the class was
	// admission-gated on having a field worth walking. Found while sweeping this row:
	// 35200 bytes over 100 rounds, frees=0.
	t.Run("scalar_only_payload", func(t *testing.T) {
		balanced(t, "oss_scalar", `struct P { a: i32, b: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var o: Option[P] = Some(P { a: i, b: i + 1 });
        match (o) { Some(p) => { acc = acc + p.a + p.b; }, None => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`, "this was 35200, with frees=0 — neither box was released")
	})

	// SOUNDNESS. The payload's string field is a bare ident aliasing a local the
	// round keeps reading after the match, and the freed box would be recycled by
	// the churn that follows. The construction-side retain fires under the same
	// STRFLDOK verdict as the k_str dec, so the two balance and `shared` survives;
	// an unbalanced release shows up as an exit-code divergence from `fern -interp`,
	// which `counts` fails on.
	t.Run("aliased_string_field_payload_survives", func(t *testing.T) {
		src := `struct P { name: string, n: i32 }
function round(r: i32): i32 {
    var shared: string = "ab" + "cd";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var o: Option[P] = Some(P { name: shared, n: i });
        match (o) { Some(p) => { acc = acc + p.n + p.name.len(); }, None => {} }
        i = i + 1;
    }
    var junk: string = "";
    var c: i32 = 0;
    while (c < 6) { junk = "zz" + "zz"; c = c + 1; }
    var sum: i32 = 0;
    var k: i32 = 0;
    while (k < shared.len()) { sum = sum + (shared[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		counts(t, "oss_aliased", src)
	})

	// REFUSED, and it must stay refused: the arm binds the payload into a value
	// that outlives the match, so the deep drop would dangle rather than leak.
	// optstruct_payload_escapes sees the bare `p.name` extraction (a `string` field
	// is non-scalar, so extracting it escapes) and withholds the credit.
	t.Run("escaping_payload_field_still_strands", func(t *testing.T) {
		src := `struct P { name: string, n: i32 }
function round(r: i32): i32 {
    var held: string = "";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var o: Option[P] = Some(P { name: "a" + "b", n: i });
        match (o) { Some(p) => { held = p.name; acc = acc + p.n; }, None => {} }
        i = i + 1;
    }
    return acc + held.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 97;
}`
		_, _, live := counts(t, "oss_escaping", src)
		if live == 0 {
			t.Errorf("live_bytes=0 — want a nonzero remainder. This arm extracts the " +
				"string field into a name that outlives the match, so releasing the " +
				"payload here would DANGLE, not leak. If a later slice teaches the pass " +
				"to see through the extraction, convert this case rather than delete it")
		}
	})
}

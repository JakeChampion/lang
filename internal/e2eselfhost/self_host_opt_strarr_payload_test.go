package e2eselfhost

import (
	"testing"
)

// --- Option/Result whose payload is a `string[]` (#6495) ---------------------
//
// The rc-payload Option class admits a payload its drop releases WHOLE, and
// `is_leaksafe_array_field` decides which arrays qualify: a flat scalar buffer
// one `__fern_rc_dec` frees with no inner walk. It refuses `string[]`, correctly
// — a plain dec frees the buffer and strands every element box — and nothing
// else claimed the shape, so `rcpayload_option_cand` returned no candidate at
// all. Not a partial release: frees=0. 51200 bytes over 400 iterations, exactly
// x2.0 per doubling, against an exact balance on native.
//
// The release already existed. `__fern_str_arr_free` walks a string[]'s element
// boxes and then frees the buffer, rc-guarded, on all three backends (wasm routes
// it to `$__fern_arr_dec_ptr`) — it is what the string[] LOCAL sweep and the
// string[] struct FIELD reclaim have called since #4355. So this is an admission
// plus a routing change in irlower and no runtime work.
//
// Freshness is per ELEMENT, and that is the whole soundness argument. The scalar
// case only ever frees the one buffer it was handed, so a fresh literal is enough;
// here each element box is freed too, so one aliased element would be released out
// from under its owner. `all_fresh_string_elems` requires every element to be a
// literal or a fresh producer, which is why the aliased rows below stay refused.

func TestSelfHostOptStrArrPayloadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	// run compiles and runs a probe, returning its leak census. `want` is the
	// expected exit code: pass the interp oracle's for a plain probe, or -1 for
	// one that calls `__rc_underflow()`, which the interpreter does not implement
	// (it has no rc runtime and exits 1 on every such program, so comparing
	// against it reports a false failure). Those probes carry their own verdict
	// instead — 99 is their underflow sentinel.
	run := func(t *testing.T, name, src string, want int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if want >= 0 && exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the payload drop "+
				"reached a live string", name, exit, want)
		}
		if want < 0 && exit == 99 {
			t.Fatalf("%s: __rc_underflow() fired — the payload drop OVER-released", name)
		}
		allocs, frees, live := parseLeakcheck(t, name, stderr)
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		t.Logf("%s: allocs=%d frees=%d live_bytes=%d exit=%d", name, allocs, frees, live, exit)
		return allocs, frees, live
	}

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		return run(t, name, src, interpExit(t, interpBin, src))
	}

	// countsRC is `counts` for a probe whose own body calls __rc_underflow().
	countsRC := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		return run(t, name, src, -1)
	}

	balanced := func(t *testing.T, name, src, was string) {
		t.Helper()
		allocs, frees, live := counts(t, name, src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance; %s",
				allocs, frees, live, was)
		}
	}

	// The headline row: Option[string[]], block-scoped in a loop. An exact balance
	// is what separates "freed the buffer and both boxes" from "freed the elements
	// too" — a bump-growth bound cannot tell those apart, and the element boxes are
	// most of the bytes.
	t.Run("option_string_array", func(t *testing.T) {
		balanced(t, "osa_option", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var o: Option[string[]] = Some(["a" + "b", "c"]);
        match (o) { Some(xs) => { acc = (acc + xs.len()) % 251; }, None => {} }
        i = i + 1;
    }
    return acc % 7;
}`, "this was 51200 live with frees=0 of 1600 allocs")
	})

	// The Result spelling of the same payload. It is a separate row because the
	// slot type alone cannot tell an Ok-array box from an Err-scalar one — the
	// candidate's recorded variant is what makes offset 8 a pointer here.
	t.Run("result_ok_string_array", func(t *testing.T) {
		balanced(t, "osa_result", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var o: Result[string[], string] = Ok(["a" + "b", "c"]);
        match (o) { Ok(xs) => { acc = (acc + xs.len()) % 251; }, Err(e) => { acc = acc + e.len(); } }
        i = i + 1;
    }
    return acc % 7;
}`, "this was 51200 live with frees=0 of 1600 allocs")
	})

	// Un-annotated: `Some(["a", "b"])` infers its parameter, so admission has only
	// the literal's shape to go on. Its number siblings have been admitted that way
	// since the class existed.
	t.Run("unannotated_literal", func(t *testing.T) {
		balanced(t, "osa_unannot", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var o = Some(["ab", "cde"]);
        match (o) { Some(xs) => { acc = (acc + xs.len()) % 251; }, None => {} }
        i = i + 1;
    }
    return acc % 7;
}`, "the un-annotated spelling must earn the same credit as Option[string[]]")
	})

	// Element reads through the payload, and an exact value guard on them: the
	// release must not run before the arm's last read. 400 * (2 + 3 + 2) = 2800,
	// and 2800 % 251 = 39.
	t.Run("element_reads_exact_value", func(t *testing.T) {
		allocs, frees, live := countsRC(t, "osa_reads", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var o: Option[string[]] = Some(["ab", "cde"]);
        match (o) { Some(xs) => { acc = (acc + xs[0].len() + xs[1].len() + xs.len()) % 251; }, None => {} }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != 39) { return 98; }
    return 0;
}`)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance; an element "+
				"read is a borrow, so the payload is released after the whole match",
				allocs, frees, live)
		}
	})

	// The scalar-array control. It shares every line of the drop with the rows
	// above and only the helper differs, so a regression that routed a flat buffer
	// through the element walk would surface here rather than as a silent leak.
	t.Run("scalar_array_control", func(t *testing.T) {
		balanced(t, "osa_control", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        var o: Option[i32[]] = Some([i, i + 1]);
        match (o) { Some(xs) => { acc = (acc + xs[0]) % 251; }, None => {} }
        i = i + 1;
    }
    return acc % 7;
}`, "the i32[] payload was already flat and must stay flat")
	})

	// --- hazards: these must stay REFUSED ------------------------------------
	//
	// Both leave a nonzero remainder on purpose. Releasing here would free a box
	// that is still owned, which is a use-after-free rather than a leak, so the
	// conservative side is the correct one and the residual bytes are the proof
	// the credit was declined.

	// An ALIASED payload: the array is a live local the loop reads after the
	// match. Freeing its elements would dangle.
	t.Run("aliased_payload_refused", func(t *testing.T) {
		_, _, live := countsRC(t, "osa_alias", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var xs: string[] = ["a" + "b", "c"];
        var o: Option[string[]] = Some(xs);
        match (o) { Some(ys) => { acc = (acc + ys.len()) % 251; }, None => {} }
        acc = (acc + xs[0].len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return acc % 7;
}`)
		if live == 0 {
			t.Errorf("live_bytes=0 — want a nonzero remainder. The payload here is a live " +
				"local read after the match, so a per-element release would DANGLE. If a " +
				"later slice proves the alias dead, convert this case rather than delete it")
		}
	})

	// An ESCAPING arm binding: the payload outlives the arm through `held`.
	t.Run("escaping_arm_binding_refused", func(t *testing.T) {
		_, _, live := countsRC(t, "osa_escape", `function main(): i32 {
    var acc: i32 = 0;
    var held: string[] = [];
    var i: i32 = 0;
    while (i < 200) {
        var o: Option[string[]] = Some(["a" + "b", "c"]);
        match (o) { Some(ys) => { held = ys; }, None => {} }
        acc = (acc + held.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return acc % 7;
}`)
		if live == 0 {
			t.Errorf("live_bytes=0 — want a nonzero remainder. The arm binds the payload to " +
				"a name that outlives the match, so releasing it here would DANGLE")
		}
	})
}

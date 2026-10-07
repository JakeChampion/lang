package e2ecompiler

import (
	"testing"
)

// --- Option/Result whose payload is a `string[]` (#6495) ---------------------
//
// An Option/Result local holding a `string[]` payload must release it WHOLE:
// `__fern_str_arr_free` walks the element boxes and then frees the buffer,
// rc-guarded, on all three backends (wasm routes it to `$__fern_arr_dec_ptr`).
// A plain dec would free the buffer and strand every element box. Unreleased
// it is 51200 bytes over 400 iterations, frees=0.
//
// Freshness is per ELEMENT, and that is the whole soundness argument: each
// element box is freed too, so one aliased element would be released out from
// under its owner. Only literal and fresh-producer elements qualify, which is
// why the aliased rows below stay refused.

func TestSelfHostOptStrArrPayloadX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	// run compiles and runs a probe, returning its leak census. `want` is the
	// expected exit code: pass the interp oracle's for a plain probe, or -1 for
	// one that calls `__rc_underflow_count()`, which the interpreter does not implement
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
			t.Fatalf("%s: __rc_underflow_count() fired — the payload drop OVER-released", name)
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

	// countsRC is `counts` for a probe whose own body calls __rc_underflow_count().
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

	// The main row: Option[string[]], block-scoped in a loop. An exact balance
	// is what separates "freed the buffer and both boxes" from "freed the elements
	// too" — a bump-growth bound cannot tell those apart, and the element boxes are
	// most of the bytes.
	t.Run("option_string_array", func(t *testing.T) {
		balanced(t, "osa_option", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let o: Option[string[]] = Some(["a" + "b", "c"]);
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let o: Result[string[], string] = Ok(["a" + "b", "c"]);
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let o = Some(["ab", "cde"]);
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let o: Option[string[]] = Some(["ab", "cde"]);
        match (o) { Some(xs) => { acc = (acc + xs[0].len() + xs[1].len() + xs.len()) % 251; }, None => {} }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let o: Option[i32[]] = Some([i, i + 1]);
        match (o) { Some(xs) => { acc = (acc + xs[0]) % 251; }, None => {} }
        i = i + 1;
    }
    return acc % 7;
}`, "the i32[] payload was already flat and must stay flat")
	})

	// --- hazards --------------------------------------------------------------
	//
	// Releasing a payload that is still owned is a use-after-free rather than a
	// leak; the underflow sentinel (99) guards that. Both balance on the typed
	// lowering.

	// An ALIASED payload: the array is a live local the loop reads after the
	// match. Freeing its elements would dangle.
	t.Run("aliased_payload_refused", func(t *testing.T) {
		allocs, frees, live := countsRC(t, "osa_alias", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let xs: string[] = ["a" + "b", "c"];
        let o: Option[string[]] = Some(xs);
        match (o) { Some(ys) => { acc = (acc + ys.len()) % 251; }, None => {} }
        acc = (acc + xs[0].len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 7;
}`)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance", allocs, frees, live)
		}
	})

	// An ESCAPING arm binding: the payload outlives the arm through `held`.
	t.Run("escaping_arm_binding_refused", func(t *testing.T) {
		allocs, frees, live := countsRC(t, "osa_escape", `function main(): i32 {
    let acc: i32 = 0;
    let held: string[] = [];
    let i: i32 = 0;
    while (i < 200) {
        let o: Option[string[]] = Some(["a" + "b", "c"]);
        match (o) { Some(ys) => { held = ys; }, None => {} }
        acc = (acc + held.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 7;
}`)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance", allocs, frees, live)
		}
	})
}

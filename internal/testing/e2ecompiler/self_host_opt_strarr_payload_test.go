package e2ecompiler

import (
	"testing"
)

func TestSelfHostOptStrArrPayloadX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

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

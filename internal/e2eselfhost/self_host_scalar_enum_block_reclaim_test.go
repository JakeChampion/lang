package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Block-local scalar-payload enum reclaim (#6127) ------------------------
//
// consumed_scalar_enum_frees is run by lower_func over the fn's TOP-LEVEL
// statements only, so a scalar-payload enum declared inside a loop or an if was
// reclaimed nowhere and leaked its box per iteration — the same top-level-only
// gap #4357 closed for rc-payload options and rc-payload enums, never mirrored
// for the scalar half. lower_block now runs the same classifier over its own
// statement list and frees at the consuming match.
//
// Releasing at the match rather than at a scope exit is not a preference: a
// nested block retires its names to "!retired!" before the function-exit sweep
// runs, so a by-name lookup there can never resolve the local. A first attempt
// that swept at function exit reclaimed nothing for the if-block shape and only
// 3 of every 4 boxes for the loop shape, because only the re-declaration path
// was firing.
//
// The leak figures are asserted as live_bytes == 0 against a balanced churn, and
// the double-free shapes are asserted through allocs == frees plus behaviour.

const scalarEnumLoopSrc = `enum E { Box(i32, i32), Nil }

function round(i: i32): i32 {
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 4) {
        var e: E = Box(k, 2);
        match (e) { Box(a, b) => { acc = acc + a + b; }, Nil => {} }
        k = k + 1;
    }
    return acc;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`

// A function holding BOTH a top-level candidate (owned by lower_func's
// consumed_scalar_enum_frees) and a nested one (owned by lower_block's): the
// box counts must balance exactly. If the two analyses both claimed the
// top-level candidate, its box would be dec'd twice.
const scalarEnumMixedSrc = `enum E { Box(i32, i32), Nil }

function round(i: i32): i32 {
    var acc: i32 = 0;
    var top: E = Box(i, 1);
    match (top) { Box(a, b) => { acc = a + b; }, Nil => {} }
    var k: i32 = 0;
    while (k < 3) {
        var inner: E = Box(k, 2);
        match (inner) { Box(c, d) => { acc = acc + c + d; }, Nil => {} }
        k = k + 1;
    }
    return acc;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 101;
}`

// TestSelfHostScalarEnumBlockReclaimX86_64 — a scalar enum built and matched
// inside a nested block reclaims its box, and a function mixing a top-level and
// a nested candidate frees exactly what it allocates.
func TestSelfHostScalarEnumBlockReclaimX86_64(t *testing.T) {
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

	t.Run("loop_local", func(t *testing.T) {
		_, _, live := counts(t, "scalar_enum_loop", scalarEnumLoopSrc, 14)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — a scalar enum built and matched inside a "+
				"loop must reclaim its box each iteration; the leak scales with the "+
				"iteration count, so any nonzero here is unbounded", live)
		}
	})

	t.Run("if_block_local", func(t *testing.T) {
		// Single bind, no rebind at all — only the consuming-match free can
		// reclaim this one, which is exactly what a function-exit sweep missed.
		src := `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    var acc: i32 = 0;
    if (i > 0) {
        var e: E = Box(i, 3);
        match (e) { Box(a, b) => { acc = a + b; }, Nil => {} }
    }
    return acc;
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 83;
}`
		_, _, live := counts(t, "scalar_enum_ifblock", src, 38)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — a single-bind if-block candidate is "+
				"reclaimed only by the consuming-match free", live)
		}
	})

	t.Run("top_level_and_nested_balance", func(t *testing.T) {
		allocs, frees, live := counts(t, "scalar_enum_mixed", scalarEnumMixedSrc, 47)
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — a function holding a TOP-LEVEL and a nested "+
				"candidate must free exactly what it allocates; frees > allocs means "+
				"lower_func's and lower_block's analyses both claimed one box "+
				"(double free), frees < allocs means one is unclaimed", allocs, frees)
		}
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
	})

	// --- rc-PAYLOAD half (#6127) --------------------------------------------
	//
	// consumed_rcpayload_enum_frees was the last member of this family without a
	// block-level sibling (rc-payload options got one in #4357, scalar enums in
	// the commit above). Its failure mode differed from the scalar one and is
	// worth keeping distinct in the tests: the RCENUM loop-rebind credit was
	// already firing, so the nested shape freed its box on every iteration EXCEPT
	// the last and leaked partially — 7200 bytes over 100 rounds, one arr_dec and
	// one str_free short of the byte-identical top-level shape.

	t.Run("rc_payload_loop_local", func(t *testing.T) {
		src := `enum T { Text(string), Nil }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 4) {
        var t: T = Text("aa" + "bb");
        match (t) { Text(s) => { acc = acc + s.len(); }, Nil => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x / 100;
}`
		allocs, frees, live := counts(t, "rc_enum_loop", src, 16)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — the LAST iteration's box and its string "+
				"payload were the leak here; the loop-rebind reclaim covered the rest, "+
				"which is why this shape leaked partially rather than completely", live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
		}
	})

	t.Run("rc_payload_if_block_local", func(t *testing.T) {
		// Single bind, no rebind — the loop-rebind credit cannot help at all here,
		// so only the consuming-match free reclaims it.
		src := `enum T { Text(string), Nil }
function round(i: i32): i32 {
    var acc: i32 = 0;
    if (i > 0) {
        var t: T = Text("pq" + "rs");
        match (t) { Text(s) => { acc = s.len(); }, Nil => {} }
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 89;
}`
		_, _, live := counts(t, "rc_enum_ifblock", src, 58)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
	})

	t.Run("rc_payload_top_level_and_nested_balance", func(t *testing.T) {
		// The double-free guard for this half: lower_func's rcenumfrees owns the
		// top-level candidate, lower_block's owns the nested one. Zeroing the slot
		// after the block free is what also keeps it disjoint from the RCENUM
		// loop-rebind reclaim — without it the next iteration's
		// emit_enum_deep_reinit_store would deep-drop the box just released.
		src := `enum T { Text(string), Nil }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var top: T = Text("xy" + "z");
    match (top) { Text(s) => { acc = s.len(); }, Nil => {} }
    var k: i32 = 0;
    while (k < 3) {
        var inner: T = Text("aa" + "bb");
        match (inner) { Text(u) => { acc = acc + u.len(); }, Nil => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 101;
}`
		allocs, frees, live := counts(t, "rc_enum_mixed", src, 92)
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — frees > allocs means both analyses claimed "+
				"one box, or the block free and the loop-rebind reclaim both ran on it; "+
				"frees < allocs means one went unclaimed", allocs, frees)
		}
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
	})
}

// TestSelfHostScalarEnumBlockHazardsX86_64 — the block-local shapes the free must
// still REFUSE. A wrongly-granted free releases a box something else still reads,
// so these assert behaviour: the failure mode is a wrong answer or a crash.
func TestSelfHostScalarEnumBlockHazardsX86_64(t *testing.T) {
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
			// The enum ESCAPES into a call, so the callee may retain it.
			name: "call_escape",
			src: `enum E { Box(i32, i32), Nil }
function take(e: E): i32 { match (e) { Box(a, b) => { return a + b; }, Nil => { return 0; } } return 0; }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        var e: E = Box(k, i);
        acc = acc + take(e);
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 97;
}`,
			want: 58,
		},
		{
			// Used AFTER its match — not dead, so freeing at the first match
			// would read a released box in the second.
			name: "used_after_match",
			src: `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        var e: E = Box(k, i);
        match (e) { Box(a, b) => { acc = acc + a + b; }, Nil => {} }
        match (e) { Box(c, _) => { acc = acc + c; }, Nil => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 89;
}`,
			want: 63,
		},
		{
			// Returned out of the block — escapes the function entirely.
			name: "escaping_return",
			src: `enum E { Box(i32, i32), Nil }
function pick(i: i32): E {
    var k: i32 = 0;
    while (k < 2) {
        var e: E = Box(k, i);
        if (k == 1) { return e; }
        k = k + 1;
    }
    return Nil;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 60) {
        var got: E = pick(r);
        match (got) { Box(a, b) => { acc = acc + a + b; }, Nil => {} }
        r = r + 1;
    }
    return acc % 91;
}`,
			want: 10,
		},

		{
			// rc-PAYLOAD, reassigned: the classifier excludes reassigned names,
			// so this stays refused. It must still be behaviourally correct — the
			// value read after the loop is the last one assigned.
			name: "rc_payload_reassigned",
			src: `enum T { Text(string), Nil }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var t: T = Text("aa" + "bb");
    var k: i32 = 0;
    while (k < 3) { t = Text("cc" + "dd"); k = k + 1; }
    match (t) { Text(s) => { acc = s.len(); }, Nil => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 79;
}`,
			want: 3,
		},
		{
			// rc-PAYLOAD escaping into a call — the callee may retain the box, so
			// the free must not fire. This is the shape the #6127 sweep's
			// enum_str_payload probe actually has, which is why that row stays
			// non-zero and is a conservatism bound rather than a compiler gap.
			name: "rc_payload_call_escape",
			src: `enum T { Text(string), Nil }
function len_of(t: T): i32 { match (t) { Text(s) => { return s.len(); }, Nil => { return 0; } } return 0; }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        var t: T = Text("aa" + "bb");
        acc = acc + len_of(t);
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 73;
}`,
			want: 63,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "scalar_enum_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"consuming-match free was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}

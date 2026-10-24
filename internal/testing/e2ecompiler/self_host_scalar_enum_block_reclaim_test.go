package e2ecompiler

import (
	"strings"
	"testing"
)

const scalarEnumLoopSrc = `enum E { Box(i32, i32), Nil }

function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let e: E = Box(k, 2);
        match (e) { Box(a, b) => { acc = acc + a + b; }, Nil => {} }
        k = k + 1;
    }
    return acc;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`

const scalarEnumMixedSrc = `enum E { Box(i32, i32), Nil }

function round(i: i32): i32 {
    let acc: i32 = 0;
    let top: E = Box(i, 1);
    match (top) { Box(a, b) => { acc = a + b; }, Nil => {} }
    let k: i32 = 0;
    while (k < 3) {
        let inner: E = Box(k, 2);
        match (inner) { Box(c, d) => { acc = acc + c + d; }, Nil => {} }
        k = k + 1;
    }
    return acc;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 101;
}`

func TestSelfHostScalarEnumBlockReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
		src := `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let acc: i32 = 0;
    if (i > 0) {
        let e: E = Box(i, 3);
        match (e) { Box(a, b) => { acc = a + b; }, Nil => {} }
    }
    return acc;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 83;
}`
		_, _, live := counts(t, "scalar_enum_ifblock", src, 38)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — a single-bind if-block candidate is "+
				"reclaimed only by the consuming-match free", live)
		}
	})

	t.Run("rebound_local", func(t *testing.T) {
		src := `enum E { Box(i32, i32), Nil }
function round(): i32 {
    let e: E = Nil;
    let i: i32 = 0;
    while (i < 4) { e = Box(i, i); i = i + 1; }
    let t: i32 = 0;
    match (e) { Box(a, b) => { t = a + b; }, Nil => { t = 0; } }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`
		allocs, frees, live := counts(t, "scalar_enum_rebound", src, 5)
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — every rebind must release the box it "+
				"overwrites and the consuming match must free the last one", allocs, frees)
		}
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — a rebound scalar-enum local leaks one "+
				"box per assignment, so this scales with the loop count", live)
		}
	})

	t.Run("conditional_rebind", func(t *testing.T) {
		src := `enum E { Box(i32, i32), Nil }
function round(n: i32): i32 {
    let e: E = Box(0, 0);
    let i: i32 = 0;
    while (i < 4) { if (i % 2 == 0) { e = Box(i, n); } i = i + 1; }
    let t: i32 = 0;
    match (e) { Box(a, b) => { t = a + b; }, Nil => { t = 0; } }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t % 97;
}`
		allocs, frees, live := counts(t, "scalar_enum_condrebind", src, 9)
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d", allocs, frees)
		}
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
	})

	t.Run("top_level_and_nested_balance", func(t *testing.T) {
		allocs, frees, live := counts(t, "scalar_enum_mixed", scalarEnumMixedSrc, 47)
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — a function holding a TOP-LEVEL and a nested "+
				"candidate must free exactly what it allocates; frees > allocs means "+
				"one box was released twice (double free), frees < allocs means one "+
				"was never released", allocs, frees)
		}
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
	})

	t.Run("no_match_loop_local", func(t *testing.T) {
		src := `enum M { A(i32), B(i32) }
function tag(m: M): i32 {
    match (m) { A(_) => { return 1; }, B(_) => { return 2; } }
    return 0;
}
function round(n: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let m: M = A(k + n);
        acc = acc + tag(m);
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`
		allocs, frees, live := counts(t, "scalar_enum_nomatch_loop", src, 4)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — a scalar-enum local with NO consuming "+
				"match is claimed by neither the match free nor, before this, any "+
				"sweep; the leak scales with the iteration count", live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
		}
	})

	t.Run("no_match_never_read", func(t *testing.T) {
		src := `enum M { A(i32), B(i32) }
@noinline function keep(m: M): i32 { return 0; }
function round(n: i32): i32 {
    let k: i32 = 0;
    while (k < 4) {
        let m: M = A(k + n);
        k = k + 1 + keep(m);
    }
    return k;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`
		allocs, frees, live := counts(t, "scalar_enum_nomatch_unused", src, 4)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d", allocs, frees)
		}
	})

	t.Run("no_match_rebound", func(t *testing.T) {
		src := `enum M { A(i32), B(i32) }
function tag(m: M): i32 {
    match (m) { A(_) => { return 1; }, B(_) => { return 2; } }
    return 0;
}
function round(n: i32): i32 {
    let m: M = A(n);
    let i: i32 = 0;
    while (i < 4) { if (i % 2 == 0) { m = B(i); } else { m = A(i); } i = i + 1; }
    return tag(m);
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`
		allocs, frees, live := counts(t, "scalar_enum_nomatch_rebound", src, 1)
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — every rebind releases the box it overwrites "+
				"and the sweep takes the last one", allocs, frees)
		}
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
	})

	t.Run("escaping_local_is_refused", func(t *testing.T) {
		src := `enum M { A(i32), B(i32) }
function mk(n: i32): M {
    let m: M = A(n);
    return m;
}
function tag(m: M): i32 {
    match (m) { A(x) => { return x; }, B(y) => { return y + 1; } }
    return 0;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let e: M = mk(r); t = t + tag(e); r = r + 1; }
    return t / 100;
}`
		allocs, _, _ := counts(t, "scalar_enum_escape", src, 49)
		if allocs == 0 {
			t.Fatal("probe allocated nothing")
		}
	})

	t.Run("rc_payload_loop_local", func(t *testing.T) {
		src := `enum T { Text(string), Nil }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let t: T = Text("aa" + "bb");
        match (t) { Text(s) => { acc = acc + s.len(); }, Nil => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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
		src := `enum T { Text(string), Nil }
function round(i: i32): i32 {
    let acc: i32 = 0;
    if (i > 0) {
        let t: T = Text("pq" + "rs");
        match (t) { Text(s) => { acc = s.len(); }, Nil => {} }
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 89;
}`
		_, _, live := counts(t, "rc_enum_ifblock", src, 58)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0", live)
		}
	})

	t.Run("rc_payload_top_level_and_nested_balance", func(t *testing.T) {
		src := `enum T { Text(string), Nil }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let top: T = Text("xy" + "z");
    match (top) { Text(s) => { acc = s.len(); }, Nil => {} }
    let k: i32 = 0;
    while (k < 3) {
        let inner: T = Text("aa" + "bb");
        match (inner) { Text(u) => { acc = acc + u.len(); }, Nil => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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

func TestSelfHostScalarEnumBlockHazardsX86_64(t *testing.T) {
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
			name: "call_escape",
			src: `enum E { Box(i32, i32), Nil }
function take(e: E): i32 { match (e) { Box(a, b) => { return a + b; }, Nil => { return 0; } } return 0; }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 3) {
        let e: E = Box(k, i);
        acc = acc + take(e);
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 97;
}`,
			want: 58,
		},
		{
			name: "rebound_value_escapes_to_container",
			src: `enum E { Box(i32, i32), Nil }
function round(): i32 {
    let keep: E[] = [];
    let e: E = Nil;
    let i: i32 = 0;
    while (i < 4) { e = Box(i, i); keep = keep.append(e); i = i + 1; }
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < keep.len()) { match (keep[k]) { Box(a, b) => { t = t + a + b; }, Nil => {} } k = k + 1; }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 97;
}`,
			want: 36,
		},
		{
			name: "rebound_value_passed_to_call",
			src: `enum E { Box(i32, i32), Nil }
function sink(x: E): i32 { match (x) { Box(a, b) => { return a + b; }, Nil => { return 0; } } return 0; }
function round(): i32 {
    let e: E = Nil;
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { e = Box(i, i); t = t + sink(e); i = i + 1; }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 97;
}`,
			want: 36,
		},
		{
			name: "rebound_value_aliased_to_local",
			src: `enum E { Box(i32, i32), Nil }
function round(): i32 {
    let e: E = Box(1, 1);
    let keep: E = e;
    e = Box(2, 2);
    let t: i32 = 0;
    match (keep) { Box(a, b) => { t = t + a + b; }, Nil => {} }
    match (e) { Box(a, b) => { t = t + a + b; }, Nil => {} }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 97;
}`,
			want: 18,
		},
		{
			name: "used_after_match",
			src: `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 3) {
        let e: E = Box(k, i);
        match (e) { Box(a, b) => { acc = acc + a + b; }, Nil => {} }
        match (e) { Box(c, _) => { acc = acc + c; }, Nil => {} }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { t = t + round(r); r = r + 1; }
    return t % 89;
}`,
			want: 63,
		},
		{
			name: "escaping_return",
			src: `enum E { Box(i32, i32), Nil }
function pick(i: i32): E {
    let k: i32 = 0;
    while (k < 2) {
        let e: E = Box(k, i);
        if (k == 1) { return e; }
        k = k + 1;
    }
    return Nil;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 60) {
        let got: E = pick(r);
        match (got) { Box(a, b) => { acc = acc + a + b; }, Nil => {} }
        r = r + 1;
    }
    return acc % 91;
}`,
			want: 10,
		},

		{
			name: "rc_payload_reassigned",
			src: `enum T { Text(string), Nil }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let t: T = Text("aa" + "bb");
    let k: i32 = 0;
    while (k < 3) { t = Text("cc" + "dd"); k = k + 1; }
    match (t) { Text(s) => { acc = s.len(); }, Nil => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 79;
}`,
			want: 3,
		},
		{
			name: "rc_payload_call_escape",
			src: `enum T { Text(string), Nil }
function len_of(t: T): i32 { match (t) { Text(s) => { return s.len(); }, Nil => { return 0; } } return 0; }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 3) {
        let t: T = Text("aa" + "bb");
        acc = acc + len_of(t);
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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

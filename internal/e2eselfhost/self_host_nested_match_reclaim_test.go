package e2eselfhost

import (
	"strings"
	"testing"
)

// A consuming `match` one block deeper than the local's own statement list —
// inside an `if` branch or a `while` body — reclaims the same as the flat
// spelling (#6319's class).
//
// `sole_top_level_match_idx` scanned only the flat statement list, so every
// analysis built on it saw no consuming match at all and issued no credit. The
// flat spelling was flat at 0 while the indented one leaked the whole box and
// payload every iteration, doubling with the loop count.
//
// The widened lookup returns the ENCLOSING top-level statement — where the free
// lands and what the liveness and escape checks skip — and the match itself is
// re-derived for the arm analyses. Feeding the enclosing `if` to those instead is
// a use-after-free rather than a missed reclaim: `match_arms_use_name` and
// `opt_arm_binding_escapes` both answer "nothing escapes" for a statement they
// cannot parse, which reads as a proof when it is a blind spot. That is what
// `nested_escape_churn` below pins.

const nmFlatArrSrc = `import "core/int";
function make(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Result[i32[], string] = make(k);
            match (v) { Ok(a) => { acc = acc + a.len(); }, Err(e) => { acc = acc + e.len(); } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const nmIfArrSrc = `import "core/int";
function make(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Result[i32[], string] = make(k);
            if (k >= 0) {
                match (v) { Ok(a) => { acc = acc + a.len(); }, Err(e) => { acc = acc + e.len(); } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const nmFlatStrSrc = `import "core/int";
function make(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[string] = make(k);
            match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const nmIfStrSrc = `import "core/int";
function make(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[string] = make(k);
            if (k >= 0) {
                match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The match runs N times against ONE box and the free lands once, after the
// loop — the reason the `while` body is admitted alongside the `if` branches.
const nmWhileStrSrc = `import "core/int";
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var v: Option[string] = mk(r);
        var c: i32 = 0;
        while (c < 2) {
            match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
            c = c + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The DIRECT-ctor candidate, nested. Not redundant with the call-init rows: it
// reaches the same widened lookup by the other admission path, and it leaked
// 35200 on main before this change.
const nmIfDirectArrSrc = `import "core/int";
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[i32[]] = Some([k, k + 1, k + 2]);
            if (k >= 0) {
                match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// THE LOAD-BEARING NEGATIVE. The arm binding escapes to an outer local, so the
// payload must not be released. It needs the churn loop: with the box wrongly
// freed the shape still exits correctly unless same-shaped strings recycle it in
// between, which is why this class survived earlier passes (#6467).
const nmEscapeChurnSrc = `import "core/int";
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("ab" + "cd");
}
function main(): i32 {
    var held: string = "qqqq";
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 50) {
        var v: Option[string] = mk(r);
        if (r >= 0) {
            match (v) { Some(s) => { held = s; }, None => { acc = acc + 1; } }
        }
        var c: i32 = 0;
        while (c < 4) { var t: string = "xy" + "zw"; acc = acc + t.len(); c = c + 1; }
        acc = acc + held.len();
        r = r + 1;
    }
    return acc % 61;
}
`

// The sibling branch mentions the local. The caller skips the WHOLE enclosing
// statement in its escape check, so this mention is invisible to it and the
// shape has to be refused here.
const nmElseMentionsSrc = `import "core/int";
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var v: Option[string] = mk(r);
        if (r % 2 == 0) {
            match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        } else {
            match (v) { Some(s) => { acc = acc + 2; }, None => { acc = acc + 3; } }
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const nmUsedAfterSrc = `import "core/int";
function olen(o: Option[string]): i32 { match (o) { Some(s) => { return s.len(); }, None => { return 0; } } }
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var v: Option[string] = mk(r);
        if (r >= 0) {
            match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        }
        acc = acc + olen(v);
        r = r + 1;
    }
    return acc % 7;
}
`

const nmCondMentionsSrc = `import "core/int";
function olen(o: Option[string]): i32 { match (o) { Some(s) => { return s.len(); }, None => { return 0; } } }
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var v: Option[string] = mk(r);
        if (olen(v) > 0) {
            match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        }
        r = r + 1;
    }
    return acc % 7;
}
`

func TestSelfHostNestedMatchReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		// The oracle, not a constant. A released-but-live payload shows up as a
		// wrong answer, and that matters more than any byte count below.
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the drop reached a live value",
				name, exit, want)
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

	// An exact balance, not just live_bytes == 0: the free lands after a
	// statement the local outlives, so an over-release matters as much as a leak
	// and only allocs == frees catches both.
	for _, tc := range []struct{ name, src string }{
		{"flat_arr_control", nmFlatArrSrc},
		{"if_nested_arr", nmIfArrSrc},
		{"flat_str_control", nmFlatStrSrc},
		{"if_nested_str", nmIfStrSrc},
		{"while_nested_str", nmWhileStrSrc},
		{"if_nested_direct_ctor_arr", nmIfDirectArrSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. "+
					"The nested spelling leaked the whole box and payload (35200 for the "+
					"array shape, 25600 for the string) while the flat control was 0",
					tc.name, allocs, frees, live)
			}
		})
	}

	// Refused, so the correct outcome is a leak. Each mention below is one the
	// caller's own escape check cannot see, because it skips the whole enclosing
	// statement.
	for _, tc := range []struct{ name, src string }{
		{"else_branch_mentions", nmElseMentionsSrc},
		{"used_after_enclosing_if", nmUsedAfterSrc},
		{"if_cond_mentions", nmCondMentionsSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if frees != 0 || live == 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want frees=0 and a nonzero "+
					"remainder. The local is reachable outside the match, so reclaiming it "+
					"here is a dangle, not a fix", tc.name, allocs, frees, live)
			}
		})
	}

	// The escaping arm binding. Exit agreement inside counts() is the real
	// detector — freeing here returns a wrong answer once the churn loop recycles
	// the box (46 against the oracle's 24 when the arm analyses are fed the
	// enclosing `if` instead of the match).
	t.Run("nested_escape_churn", func(t *testing.T) {
		allocs, frees, live := counts(t, "nested_escape_churn", nmEscapeChurnSrc)
		if live == 0 || frees >= allocs {
			t.Errorf("nested_escape_churn: allocs=%d frees=%d live_bytes=%d — the arm "+
				"binding escapes to an outer local, so the payload must be stranded, not "+
				"released. A full balance here means the escape gate was bypassed and the "+
				"string freed under its other holder", allocs, frees, live)
		}
	})
}

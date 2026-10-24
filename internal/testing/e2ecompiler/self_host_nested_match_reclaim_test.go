package e2ecompiler

import (
	"strings"
	"testing"
)

const nmFlatArrSrc = `import "core/int";
function make(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let v: Result[i32[], string] = make(k);
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
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let v: Result[i32[], string] = make(k);
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
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let v: Option[string] = make(k);
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
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let v: Option[string] = make(k);
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

const nmWhileStrSrc = `import "core/int";
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let v: Option[string] = mk(r);
        let c: i32 = 0;
        while (c < 2) {
            match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
            c = c + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const nmEscapeChurnSrc = `import "core/int";
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("ab" + "cd");
}
function main(): i32 {
    let held: string = "qqqq";
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 50) {
        let v: Option[string] = mk(r);
        if (r >= 0) {
            match (v) { Some(s) => { held = s; }, None => { acc = acc + 1; } }
        }
        let c: i32 = 0;
        while (c < 4) { let t: string = "xy" + "zw"; acc = acc + t.len(); c = c + 1; }
        acc = acc + held.len();
        r = r + 1;
    }
    return acc % 61;
}
`

const nmElseMentionsSrc = `import "core/int";
function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let v: Option[string] = mk(r);
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
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let v: Option[string] = mk(r);
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
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let v: Option[string] = mk(r);
        if (olen(v) > 0) {
            match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        }
        r = r + 1;
    }
    return acc % 7;
}
`

func TestSelfHostNestedMatchReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
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

	for _, tc := range []struct{ name, src string }{
		{"flat_arr_control", nmFlatArrSrc},
		{"if_nested_arr", nmIfArrSrc},
		{"flat_str_control", nmFlatStrSrc},
		{"if_nested_str", nmIfStrSrc},
		{"while_nested_str", nmWhileStrSrc},
		{"else_branch_mentions", nmElseMentionsSrc},
		{"used_after_enclosing_if", nmUsedAfterSrc},
		{"if_cond_mentions", nmCondMentionsSrc},
		{"nested_escape_churn", nmEscapeChurnSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance",
					tc.name, allocs, frees, live)
			}
		})
	}
}

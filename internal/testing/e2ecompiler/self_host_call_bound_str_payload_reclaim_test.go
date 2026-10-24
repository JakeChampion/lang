package e2ecompiler

import (
	"strings"
	"testing"
)

const cbsOptStrCallSrc = `function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbsResultStrOkCallSrc = `function mk(i: i32): Result[string, i32] {
    if (i < 0) { return Err(1); }
    return Ok("abcd");
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[string, i32] = mk(i);
        match (v) { Ok(s) => { acc = acc + s.len(); }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbsOptStrConcatCallSrc = `function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("ab" + "cd");
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbsOptStrFnScopeSrc = `function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function round(r: i32): i32 {
    let v: Option[string] = mk(r);
    let acc: i32 = 0;
    match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbsOptStrDirectSrc = `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = Some("abcd");
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbsOptStrAliasLocalSrc = `function mk(i: i32): Option[string] {
    let pre: string = "abcd";
    if (i < 0) { return None; }
    return Some(pre);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbsOptStrParamAliasSrc = `function mk(s: string, i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some(s);
}
function round(r: i32): i32 {
    let keep: string = "abcd";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(keep, i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + keep.len() + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

func TestSelfHostCallBoundStrPayloadReclaimX86_64(t *testing.T) {
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
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the payload free reached a live string",
				name, exit, want)
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

	for _, tc := range []struct{ name, src string }{
		{"optstr_from_call", cbsOptStrCallSrc},
		{"result_str_ok_from_call", cbsResultStrOkCallSrc},
		{"optstr_concat_from_call", cbsOptStrConcatCallSrc},
		{"optstr_fnscope_from_call", cbsOptStrFnScopeSrc},
		{"optstr_direct_ctor", cbsOptStrDirectSrc},
		{"optstr_alias_local_from_call", cbsOptStrAliasLocalSrc},
		{"optstr_param_alias_from_call", cbsOptStrParamAliasSrc},
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

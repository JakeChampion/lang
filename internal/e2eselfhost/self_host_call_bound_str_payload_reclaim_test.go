package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Call-bound STRING-payload Option/Result locals reclaim ------------------
//
// `rcpayload_option_call_ptype` admitted a call-bound Option/Result only when
// the success payload was a leak-safe scalar array. A `string` success payload
// was refused, so `var v: Option[string] = mk(i)` emitted no free at all and the
// box AND its string leaked every iteration — `frees=0` — while the same shape
// with the constructor written inline was flat at 0.
//
// The freshness proof already existed. opt_fresh_ret_fns_of records, per
// producer, whether every success payload is a static literal or a
// syntactically-fresh string producer (`str_local_binding_is_fresh`: concat,
// the .to_upper family, a named producer) — the "f" flag. That is exactly the
// proof `__fern_str_free` needs, because op_opt_make stores the payload
// UNCOUNTED: a fresh payload is sole-owned, an aliased one is not. Only the
// flag was being dropped on the floor by the two name extractors, so lower_func
// could not see it; it is now seeded as "OPTFRESHF:<name>" beside the existing
// "OPTFRESH:<name>".
//
// The distinction this file exists to pin is REFUSAL, not reclaim. Freeing a
// non-fresh payload does not leak less — it DANGLES, which is the one outcome
// worse than the leak being fixed. So the aliased rows below are as load-bearing
// as the reclaiming ones, and each asserts the exit code against `fern -interp`
// so a dangle shows up as a wrong answer rather than a quiet corruption.

const cbsOptStrCallSrc = `function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The Result spelling: a string Ok, a scalar Err. The Err arm needs no drop, so
// the tag guard's else-branch stays empty.
const cbsResultStrOkCallSrc = `function mk(i: i32): Result[string, i32] {
    if (i < 0) { return Err(1); }
    return Ok("abcd");
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[string, i32] = mk(i);
        match (v) { Ok(s) => { acc = acc + s.len(); }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// A fresh string PRODUCER payload rather than a literal — the other half of what
// the "f" flag admits, and the one that actually allocates. The concat
// temporaries are freed by other machinery, so a double free here would tick the
// underflow detector rather than merely unbalance the count.
const cbsOptStrConcatCallSrc = `function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("ab" + "cd");
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// Function-scoped single bind — the fn-level emit site rather than lower_block's.
const cbsOptStrFnScopeSrc = `function mk(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}
function round(r: i32): i32 {
    var v: Option[string] = mk(r);
    var acc: i32 = 0;
    match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The direct-ctor sibling, which already reclaimed: a guard that the new
// admission does not disturb the path it sits beside.
const cbsOptStrDirectSrc = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = Some("abcd");
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// --- the rows that must NOT reclaim -----------------------------------------
//
// The producer returns a bare LOCAL, so opt_fresh_ret_fns_of flags it "a": the
// box is fresh, the payload is not. Freeing it would dangle the local.
const cbsOptStrAliasLocalSrc = `function mk(i: i32): Option[string] {
    var pre: string = "abcd";
    if (i < 0) { return None; }
    return Some(pre);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The same refusal where the alias reaches further: the payload is the CALLER's
// live string, passed as a parameter. `keep` is read after the loop, so a free
// here corrupts a value still in use rather than one merely out of scope.
const cbsOptStrParamAliasSrc = `function mk(s: string, i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some(s);
}
function round(r: i32): i32 {
    var keep: string = "abcd";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = mk(keep, i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + keep.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

func TestSelfHostCallBoundStrPayloadReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		// The oracle, not a hardcoded constant: a dangling payload shows up here
		// as a wrong answer, and that matters more than the byte counts below.
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

	// Fully reclaimed: box and string both released by the tag-guarded drop.
	for _, tc := range []struct{ name, src string }{
		{"optstr_from_call", cbsOptStrCallSrc},
		{"result_str_ok_from_call", cbsResultStrOkCallSrc},
		{"optstr_concat_from_call", cbsOptStrConcatCallSrc},
		{"optstr_fnscope_from_call", cbsOptStrFnScopeSrc},
		{"optstr_direct_ctor", cbsOptStrDirectSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance",
					tc.name, allocs, frees, live)
			}
		})
	}

	// Refused, and pinned AS refused. If one of these starts reclaiming, the
	// admission has widened past what the "f" flag proves and is freeing a
	// payload someone else still owns — re-derive the freshness proof before
	// moving the row up, and do not simply delete the case.
	for _, tc := range []struct{ name, src string }{
		{"optstr_alias_local_from_call", cbsOptStrAliasLocalSrc},
		{"optstr_param_alias_from_call", cbsOptStrParamAliasSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if frees != 0 || live == 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want frees=0 and a nonzero remainder. "+
					"This shape's payload ALIASES a live string (the \"a\" flag), so reclaiming it is a "+
					"dangle, not a fix", tc.name, allocs, frees, live)
			}
		})
	}
}

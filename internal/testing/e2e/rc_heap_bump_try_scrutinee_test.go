package e2e

import (
	"testing"
)

func tryScrutScalarBumpSrc(n string) string {
	return `function mk(pre: string): Result[i32, i32] { return Ok(pre.len()); }
function go(pre: string): Result[i32, i32] { let v: i32 = mk(pre)?; return Ok(v + 1); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab")) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func tryScrutOptionBumpSrc(n string) string {
	return `function mko(pre: string, k: i32): Option[i32] { if (k > 0) { return None; } return Some(pre.len()); }
function go(pre: string, k: i32): Option[i32] { let v: i32 = mko(pre, k)?; return Some(v + 1); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab", i % 2)) { Some(v) => { acc = acc + v; }, None => { acc = acc + 1; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func tryScrutStringBumpSrc(n string) string {
	return `function mk(pre: string, k: i32): Result[string, i32] { if (k > 2) { return Err(7); } return Ok(pre + "abc"); }
function go(pre: string, k: i32): Result[i32, i32] { let s: string = mk(pre, k)?; return Ok(s.len()); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab", i % 4)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func tryScrutStrLitBumpSrc(n string) string {
	return `function mk(pre: string, k: i32): Result[string, i32] { if (k > 2) { return Err(7); } return Ok("abcde"); }
function go(pre: string, k: i32): Result[i32, i32] { let s: string = mk(pre, k)?; return Ok(s.len()); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab", i % 4)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func tryScrutLiteralBumpSrc(n string) string {
	return `function go(pre: string, k: i32): Option[i32] {
    let f: f64 = Some(0.5 + (k as f64))?;
    let s: string = Some(pre + "abc")?;
    return Some((f as i32) + s.len());
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab", i % 3)) { Some(v) => { acc = acc + v; }, None => { acc = acc + 1; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const tryScrutStringWasmSound = `function mk(pre: string, k: i32): Result[string, i32] { if (k > 2) { return Err(7); } return Ok(pre + "abc"); }
function go(pre: string, k: i32): Result[i32, i32] { let s: string = mk(pre, k)?; return Ok(s.len()); }
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 2000) {
        match (go("ab", i % 4)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc != 11000) { return 99; }
    return __rc_underflow_count();
}`

const tryScrutAliasedBoxSafe = `function mk(pre: string): Result[string, i32] { return Ok(pre + "abc"); }
function id2(r: Result[string, i32]): Result[string, i32] { return r; }
function go(pre: string): Result[i32, i32] { let s: string = id2(mk(pre))?; return Ok(s.len()); }
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        match (go("ab")) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc != 1000) { return 99; }
    return __rc_underflow_count();
}`

const tryScrutAliasedPayloadSafe = `function mk(pre: string): Result[string, i32] { return Ok(pre); }
function go(pre: string): Result[i32, i32] { let s: string = mk(pre)?; return Ok(s.len()); }
function main(): i32 {
    let keep: string = "abc" + "def";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        match (go(keep)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc != 1200) { return 99; }
    if (keep.len() != 6) { return 88; }
    return __rc_underflow_count();
}`

const tryScrutLiteralAliasedPayloadSafe = `function go(pre: string): Option[i32] { let s: string = Some(pre)?; return Some(s.len()); }
function main(): i32 {
    let keep: string = "abc" + "def";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        match (go(keep)) { Some(v) => { acc = acc + v; }, None => {}, }
        i = i + 1;
    }
    if (acc != 1200) { return 99; }
    if (keep.len() != 6) { return 88; }
    return __rc_underflow_count();
}`

func checkTryScrutSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, tryScrutAliasedBoxSafe); code != 0 {
		t.Errorf("aliased-box safety: code=%d (99=value, >0=over-release/UAF)", code)
	}
	if _, code := run(t, tryScrutAliasedPayloadSafe); code != 0 {
		t.Errorf("aliased-payload safety: code=%d (99=value, 88=payload freed under caller, >0=over-release)", code)
	}
	if _, code := run(t, tryScrutLiteralAliasedPayloadSafe); code != 0 {
		t.Errorf("literal aliased-payload safety: code=%d (99=value, 88=payload freed under caller, >0=over-release)", code)
	}
}

func TestX86_64TryScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{tryScrutScalarBumpSrc, tryScrutOptionBumpSrc, tryScrutStringBumpSrc, tryScrutLiteralBumpSrc} {
		small := mustRunX86_64FreeOn(t, mk("50"))
		large := mustRunX86_64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("try-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkTryScrutSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64TryScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{tryScrutScalarBumpSrc, tryScrutOptionBumpSrc, tryScrutStringBumpSrc, tryScrutLiteralBumpSrc} {
		small := mustRunArm64FreeOn(t, mk("50"))
		large := mustRunArm64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("try-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkTryScrutSafe(t, compileAndRunArm64FreeOn)
}

func TestWASMTryScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{tryScrutScalarBumpSrc, tryScrutOptionBumpSrc, tryScrutStrLitBumpSrc, tryScrutLiteralBumpSrc} {
		small := runWasm(t, mk("50"))
		large := runWasm(t, mk("5000"))
		if small != large {
			t.Errorf("try-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	if code := runWasm(t, tryScrutStringWasmSound); code != 0 {
		t.Errorf("concat-payload soundness: code=%d (99=value, >0=over-release)", code)
	}
	checkTryScrutSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}

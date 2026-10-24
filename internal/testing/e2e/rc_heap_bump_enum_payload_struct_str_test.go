package e2e

import (
	"testing"
)

func enumPayloadStructStrLitBumpSrc(n string) string {
	return `struct S { name: string, n: i32 }
enum E { A(S), B }
function mk(n: i32): E { return A(S { name: "ab", n: n }); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) {
        let e: E = mk(i);
        match (e) { A(s) => { acc = acc + s.n; }, B => { acc = acc + 0; } }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    if (__rc_underflow_count() != 0) { return -2; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func enumPayloadStructStrConcatBumpSrc(n string) string {
	return `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) {
        let e: E = mk("a", i);
        match (e) { A(s, k) => { acc = acc + k + s.name.len(); }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    if (__rc_underflow_count() != 0) { return -2; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const enumPayloadStructStrParamEmbedSafe = `struct S { name: string, n: i32 }
enum E { A(S), B }
function mk(nm: string, n: i32): E { return A(S { name: nm, n: n }); }
function main(): i32 {
    let keep: string = "aa" + "bb";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let e: E = mk(keep, i);
        match (e) { A(s) => { acc = acc + s.name.len(); }, B => { acc = acc + 0; } }
        i = i + 1;
    }
    if (keep.len() != 4) { return 88; }
    if (acc != 2000) { return 99; }
    return __rc_underflow_count();
}`

const enumPayloadStructStrAliasedReadSafe = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "xy", n: n }, n); }
function main(): i32 {
    let last: string = "";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let e: E = mk("a", i);
        match (e) { A(s, k) => { last = s.name; acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (last.len() != 3) { return 88; }
    if (acc < 0) { return 99; }
    return __rc_underflow_count();
}`

const enumPayloadStructStrAliasedBoxSafe = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
@noinline function id2(e: E): E { return e; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let e: E = id2(mk("a", i));
        match (e) { A(s, k) => { acc = acc + s.name.len(); }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (acc != 400) { return 99; }
    return __rc_underflow_count();
}`

func checkEnumPayloadStructStrSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, enumPayloadStructStrParamEmbedSafe); code != 0 {
		t.Errorf("param-embed safety: code=%d (88=caller string freed, 99=value, >0=over-release)", code)
	}
	if _, code := run(t, enumPayloadStructStrAliasedReadSafe); code != 0 {
		t.Errorf("aliased-read safety: code=%d (88=read freed under alias, 99=value, >0=over-release)", code)
	}
	if _, code := run(t, enumPayloadStructStrAliasedBoxSafe); code != 0 {
		t.Errorf("aliased-box safety: code=%d (99=value, >0=over-release)", code)
	}
}

func TestX86_64EnumPayloadStructStrReclaim(t *testing.T) {
	for _, mk := range []func(string) string{enumPayloadStructStrLitBumpSrc, enumPayloadStructStrConcatBumpSrc} {
		small := mustRunX86_64FreeOn(t, mk("50"))
		large := mustRunX86_64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("enum-payload-struct string bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkEnumPayloadStructStrSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64EnumPayloadStructStrReclaim(t *testing.T) {
	for _, mk := range []func(string) string{enumPayloadStructStrLitBumpSrc, enumPayloadStructStrConcatBumpSrc} {
		small := mustRunArm64FreeOn(t, mk("50"))
		large := mustRunArm64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("enum-payload-struct string bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkEnumPayloadStructStrSafe(t, compileAndRunArm64FreeOn)
}

const enumPayloadStructStrConcatWasmSound = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 2000) {
        let e: E = mk("a", i);
        match (e) { A(s, k) => { acc = acc + s.name.len(); }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (acc != 4000) { return 99; }
    return __rc_underflow_count();
}`

func TestWASMEnumPayloadStructStrReclaim(t *testing.T) {
	small := runWasm(t, enumPayloadStructStrLitBumpSrc("50"))
	large := runWasm(t, enumPayloadStructStrLitBumpSrc("5000"))
	if small != large {
		t.Errorf("enum-payload-struct string bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if code := runWasm(t, enumPayloadStructStrConcatWasmSound); code != 0 {
		t.Errorf("concat-field soundness: code=%d (99=value, >0=over-release)", code)
	}
	checkEnumPayloadStructStrSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}

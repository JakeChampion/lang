package e2e

import (
	"testing"
)

const enumStrPayloadChurnSrc = `import "std/i32";
enum Msg { Text(string), Code(i32) }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function probe(k: i32): i32 {
    let m: Msg = Text(wide(k));
    let got: i32 = 0;
    match (m) { Text(t) => { got = t.len(); }, Code(c) => { got = c; } }
    return got;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t;
}
function main(): i32 {
    let warm: i32 = churn(200);
    let before: i64 = __heap_bump_bytes();
    let again: i32 = churn(200);
    let per: i64 = (__heap_bump_bytes() - before) / 200;
    if (warm != again) { return 98; }
    if (warm <= 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 96; }
    return (per as i32);
}`

const uniformEnumStrPayloadChurnSrc = `import "std/i32";
enum Line { Head(string), Tail(string) }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function probe(k: i32): i32 {
    let m: Line = Head(wide(k));
    let got: i32 = 0;
    match (m) { Head(t) => { got = t.len(); }, Tail(t) => { got = t.len() + 1; } }
    return got;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t;
}
function main(): i32 {
    let warm: i32 = churn(200);
    let before: i64 = __heap_bump_bytes();
    let again: i32 = churn(200);
    let per: i64 = (__heap_bump_bytes() - before) / 200;
    if (warm != again) { return 98; }
    if (warm <= 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 96; }
    return (per as i32);
}`

const enumStrPayloadAliasSrc = `import "std/i32";
enum Msg { Text(string), Code(i32) }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function eat(s: string): i32 { return s.len() + 1; }
function probe(k: i32): i32 {
    let s: string = wide(k);
    let m: Msg = Text(s);
    let out: string = "";
    match (m) { Text(t) => { out = t; }, Code(c) => { out = c.to_string(); } }
    return eat(out) + s.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + probe(i); i = i + 1; }
    if (acc <= 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 96; }
    return 0;
}`

func TestX86_64EnumStringPayloadReclaimed(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, enumStrPayloadChurnSrc); code != 0 {
		t.Errorf("enum string-payload churn leaked %d bytes/round on x86-64, want 0", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, uniformEnumStrPayloadChurnSrc); code != 0 {
		t.Errorf("uniform-enum string-payload churn leaked %d bytes/round on x86-64, want 0", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, enumStrPayloadAliasSrc); code != 0 {
		t.Errorf("aliased enum string payload: want 0, got %d on x86-64", code)
	}
}

func TestArm64EnumStringPayloadReclaimed(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, enumStrPayloadChurnSrc); code != 0 {
		t.Errorf("enum string-payload churn leaked %d bytes/round on arm64, want 0", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, uniformEnumStrPayloadChurnSrc); code != 0 {
		t.Errorf("uniform-enum string-payload churn leaked %d bytes/round on arm64, want 0", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, enumStrPayloadAliasSrc); code != 0 {
		t.Errorf("aliased enum string payload: want 0, got %d on arm64", code)
	}
}

func TestWASMEnumStringPayloadReclaimed(t *testing.T) {
	if got := runWasm(t, enumStrPayloadChurnSrc); got != 0 {
		t.Errorf("enum string-payload churn leaked %d bytes/round on wasm, want 0", got)
	}
	if got := runWasm(t, uniformEnumStrPayloadChurnSrc); got != 0 {
		t.Errorf("uniform-enum string-payload churn leaked %d bytes/round on wasm, want 0", got)
	}
	if got := runWasm(t, enumStrPayloadAliasSrc); got != 0 {
		t.Errorf("aliased enum string payload: want 0, got %d on wasm", got)
	}
}

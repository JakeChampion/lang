package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Enum string-payload reclamation (#6901) — the enum sibling of the tuple
// gate in rc_heap_bump_tuple_test.go. The exit sweep's INLINE enum arm
// released a native single-word string payload with a bare __fern_rc_dec,
// which decrements and never frees, so the buffer's count went 1 -> 0 and it
// was stranded — 64 B a round on x86-64 while arm64 and wasm (two-word ABIs,
// __fern_str_dec) were already flat.
//
// The binding has to be in a CALLEE. A loop-scoped `var m` re-declared in the
// body reclaims through emitVarReinitDropOld, which routes to the generated
// __drop_enum_<E> — and that fn has always called __fern_str_dec, so the loop
// spelling was flat throughout. Only the function-exit sweep was short.
//
// Both tiers of emitEnumSlotDrop are gated: `Msg` is non-uniform (variant-plan
// tag switch -> dropStructField) and `Line` is uniform (branchless payload
// decs -> decValueOnStack). Both measured 64 B/round pre-fix on x86-64.
//
// Each program reports the per-round bytes as its exit code (64 pre-fix on
// x86-64, 0 after) rather than a bound, so a partial regression is legible.

const enumStrPayloadChurnSrc = `import "std/i32";
enum Msg { Text(string), Code(i32) }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function probe(k: i32): i32 {
    var m: Msg = Text(wide(k));
    var got: i32 = 0;
    match (m) { Text(t) => { got = t.len(); }, Code(c) => { got = c; } }
    return got;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t;
}
function main(): i32 {
    var warm: i32 = churn(200);
    var before: i64 = __heap_bump_bytes();
    var again: i32 = churn(200);
    var per: i64 = (__heap_bump_bytes() - before) / 200;
    if (warm != again) { return 98; }
    if (warm <= 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 96; }
    return (per as i32);
}`

const uniformEnumStrPayloadChurnSrc = `import "std/i32";
enum Line { Head(string), Tail(string) }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function probe(k: i32): i32 {
    var m: Line = Head(wide(k));
    var got: i32 = 0;
    match (m) { Head(t) => { got = t.len(); }, Tail(t) => { got = t.len() + 1; } }
    return got;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t;
}
function main(): i32 {
    var warm: i32 = churn(200);
    var before: i64 = __heap_bump_bytes();
    var again: i32 = churn(200);
    var per: i64 = (__heap_bump_bytes() - before) / 200;
    if (warm != again) { return 98; }
    if (warm <= 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 96; }
    return (per as i32);
}`

// enumStrPayloadAliasSrc is the over-release half of the same gate: the
// payload aliases a live local that is read AFTER the enum's last reference,
// is bound out of the match, and is handed to a callee. The freeing drop
// short-circuits on rc > 1, so this must stay at exit 0 — a double free shows
// up here (and as a sanitizer report under FERN_RC_UNDERFLOW_TRAP=1) rather
// than as a byte count.
const enumStrPayloadAliasSrc = `import "std/i32";
enum Msg { Text(string), Code(i32) }
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function eat(s: string): i32 { return s.len() + 1; }
function probe(k: i32): i32 {
    var s: string = wide(k);
    var m: Msg = Text(s);
    var out: string = "";
    match (m) { Text(t) => { out = t; }, Code(c) => { out = c.to_string(); } }
    return eat(out) + s.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
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

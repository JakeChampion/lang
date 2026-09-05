package e2e

import "testing"

// The string builder on wasm — strbuf_reset / strbuf_append / strbuf_take
// (#7947). They are core builtins, so E066 lets them through on every
// target, and until wasmbin grew a lowering for them any wasm program that
// touched one died at codegen with `unknown callee "strbuf_reset"`.
//
// The wasm implementation differs from the natives' in two ways that these
// cases are built around. Its buffer starts at 256 bytes (doubling) where
// the natives start at 64 KiB, so a build of a few hundred bytes exercises
// an allocate-and-copy the natives reach only at scale. And its
// strings are two words with a short form packed INTO them, so an appended
// literal reaches the builder either as a memory address or as bytes inside
// the (data, len) pair depending only on its length.

// A short literal is inline-packed (at most 7 bytes, no address to copy
// from); a long one is a heap pointer. One build mixes both.
func TestWASMStrbufBuildMixesInlineAndHeapStrings(t *testing.T) {
	src := `function main(): i32 {
    strbuf_reset();
    strbuf_append("ab");
    strbuf_append("a literal well past the inline form");
    strbuf_append("cd");
    print(strbuf_take());
    return 0;
}`
	want := "aba literal well past the inline formcd"
	if got := runWasmCapturingStdout(t, src); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// Taking an empty builder yields an empty string, and the builder is still
// usable afterwards.
func TestWASMStrbufEmptyTake(t *testing.T) {
	src := `function main(): i32 {
    strbuf_reset();
    var empty: string = strbuf_take();
    if (empty.len() != 0) { return 1; }
    strbuf_append("after");
    if (strbuf_take() != "after") { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (1 = empty take was not empty, 2 = the builder did not survive it)", got)
	}
}

// A reset mid-build discards what came before it.
func TestWASMStrbufResetMidSequence(t *testing.T) {
	src := `function main(): i32 {
    strbuf_reset();
    strbuf_append("x");
    strbuf_reset();
    strbuf_append("y");
    print(strbuf_take());
    return 0;
}`
	if got := runWasmCapturingStdout(t, src); got != "y" {
		t.Errorf("output = %q, want \"y\"", got)
	}
}

// The taken string is an ordinary owned string: it survives into a local,
// answers .len(), indexes, and is not disturbed by the next build.
func TestWASMStrbufTakeIntoVar(t *testing.T) {
	src := `function main(): i32 {
    strbuf_reset();
    strbuf_append("hello");
    var s: string = strbuf_take();
    strbuf_append("overwrite");
    var t: string = strbuf_take();
    if (s != "hello") { return 1; }
    if (s.len() != 5) { return 2; }
    if (t != "overwrite") { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (1 = first take corrupted, 2 = wrong length, 3 = second take wrong)", got)
	}
}

// GROWTH past the 256-byte initial capacity: 100 appends of "xyz" is 300
// bytes, so the buffer reallocates and copies at least once. The assertions
// read a byte on either side of the 256 boundary, which a botched
// grow-copy moves or drops.
func TestWASMStrbufGrowsPastInitialCapacity(t *testing.T) {
	src := `function main(): i32 {
    strbuf_reset();
    var i: i32 = 0;
    while (i < 100) {
        strbuf_append("xyz");
        i = i + 1;
    }
    var s: string = strbuf_take();
    if (s.len() != 300) { return 1; }
    if (s[0] as i32 != 120) { return 2; }
    if (s[250] as i32 != 121) { return 3; }
    if (s[299] as i32 != 122) { return 4; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (1 = wrong length, 2/3/4 = wrong byte at 0/250/299)", got)
	}
}

// The buffer and its capacity persist across takes, so the second build
// runs on the block the first one grew. A grow that forgot to publish the
// new pointer or capacity shows up here rather than in a single build.
func TestWASMStrbufReusesGrownBufferAcrossTakes(t *testing.T) {
	src := `function main(): i32 {
    strbuf_reset();
    var i: i32 = 0;
    while (i < 200) {
        strbuf_append("ab");
        i = i + 1;
    }
    var first: string = strbuf_take();
    if (first.len() != 400) { return 1; }
    strbuf_append("second");
    var second: string = strbuf_take();
    if (second != "second") { return 2; }
    if (first[399] as i32 != 98) { return 3; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("got %d, want 0 (1 = wrong first length, 2 = second build wrong, 3 = first build clobbered)", got)
	}
}

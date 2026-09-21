package e2eselfhost

import (
	"path/filepath"
	"testing"
)

// `__free(p, n)` returns a raw block — one `__alloc` handed out with no rc
// header — to the size class `__fern_alloc` pops from. It used to be a no-op in
// every backend ("safe-leak mode"), so a program that allocated and freed in a
// loop grew without bound while native stayed flat: 5000 rounds of a 64-byte
// block moved the bump allocator, and every buffer `core/map` frees leaked.
//
// The bound is the gate, and there is no interpreter leg: `__alloc` / `__free`
// are the raw-memory floor `core/map` is written against, and the AST
// interpreter has no such heap — it answers `undefined function "__alloc"`. So
// each case is self-checking, returning a distinct code per way of being wrong
// rather than being compared against an oracle. Native, which has the same
// floor, answers 0 on all four.
var rawFreeCases = []struct {
	name, src string
}{
	// The block is bigger than the freelist link it has to hold, and small
	// enough to land in the word-indexed small tier.
	{"small-blocks-are-recycled", `function main(): i32 {
    var w: i32 = 0;
    while (w < 200) { var p: usize = __alloc(64); __free(p, 64); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var q: usize = __alloc(64); __free(q, 64); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 - b1 >= 1024) { return 98; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
	// A size that is not a multiple of 8 must round to the same class the
	// allocator rounded the request to, or the block returns to a class whose
	// next request is bigger than it is.
	{"an-unrounded-size-returns-to-its-own-class", `function main(): i32 {
    var w: i32 = 0;
    while (w < 200) { var p: usize = __alloc(52); __free(p, 52); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var q: usize = __alloc(52); __free(q, 52); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 - b1 >= 1024) { return 98; }
    return 0;
}`},
	// A recycled block must still be usable: write through it after the free
	// and read the value back, so a push that corrupted the block's own words
	// past the link shows up as a wrong answer rather than a quiet leak.
	{"a-recycled-block-still-stores", `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 500) {
        var p: usize = __alloc(32);
        __store_i32(p + 8, i);
        __store_i32(p + 16, i * 2);
        acc = acc + __load_i32(p + 8) + __load_i32(p + 16);
        __free(p, 32);
        i = i + 1;
    }
    if (acc != 374250) { return 1; }
    return 0;
}`},
	// The large tier (>= 512 KiB) has its own class array and its own push;
	// this is the only case that reaches it.
	{"large-blocks-are-recycled", `function main(): i32 {
    var w: i32 = 0;
    while (w < 4) { var p: usize = __alloc(600000); __free(p, 600000); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 64) { var q: usize = __alloc(600000); __free(q, 600000); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 - b1 >= 1048576) { return 98; }
    return 0;
}`},
}

func TestSelfHostRawFree(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, tc := range rawFreeCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					exit, stderr := selfHostCLIRun(t, fernBin, stdlibRoot, tc.src, target)
					if exit != 0 {
						t.Fatalf("exit = %d, want 0 (98 = the block was not recycled, 99 = over-release)\n%s", exit, stderr)
					}
				})
			}
		})
	}
}

package e2ecompiler

import (
	"path/filepath"
	"testing"
)

var rawFreeCases = []struct {
	name, src string
}{
	{"small-blocks-are-recycled", `function main(): i32 {
    let w: i32 = 0;
    while (w < 200) { let p: usize = __alloc(64); __free(p, 64); w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let q: usize = __alloc(64); __free(q, 64); i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 - b1 >= 1024) { return 98; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
	{"an-unrounded-size-returns-to-its-own-class", `function main(): i32 {
    let w: i32 = 0;
    while (w < 200) { let p: usize = __alloc(52); __free(p, 52); w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let q: usize = __alloc(52); __free(q, 52); i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 - b1 >= 1024) { return 98; }
    return 0;
}`},
	{"a-recycled-block-still-stores", `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 500) {
        let p: usize = __alloc(32);
        __store_i32(p + 8, i);
        __store_i32(p + 16, i * 2);
        acc = acc + __load_i32(p + 8) + __load_i32(p + 16);
        __free(p, 32);
        i = i + 1;
    }
    if (acc != 374250) { return 1; }
    return 0;
}`},
	{"large-blocks-are-recycled", `function main(): i32 {
    let w: i32 = 0;
    while (w < 4) { let p: usize = __alloc(600000); __free(p, 600000); w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 64) { let q: usize = __alloc(600000); __free(q, 600000); i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 - b1 >= 1048576) { return 98; }
    return 0;
}`},
}

func TestSelfHostRawFree(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
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

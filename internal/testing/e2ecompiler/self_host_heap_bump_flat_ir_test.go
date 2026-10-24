package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

type heapBumpFlatCase struct {
	name     string
	src      func(n string) string
	retained func(n string) string
}

var heapBumpFlatCases = []heapBumpFlatCase{
	{name: "nested-concat", src: func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "longer_string_one_here";
    let b: string = "longer_string_two_here";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + (a + b + a + b).len(); i = i + 1; }
    return ((__heap_bump_bytes() as i32) - before) + (acc - acc);
}`
	}, retained: func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "longer_string_one_here";
    let b: string = "longer_string_two_here";
    let keep: string[] = [];
    let i: i32 = 0;
    while (i < ` + n + `) { keep = keep.append(a + b + a + b); i = i + 1; }
    if (keep.len() != ` + n + `) { return 200; }
    if ((__heap_bump_bytes() as i32) > before) { return 1; }
    return 0;
}`
	}},
	{name: "len-receiver", src: func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "hello there friend, ";
    let b: string = "general kenobi!!!";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + (a + b).len(); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "stmt-temp", src: func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { [i, i + 1, i + 2]; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "discarded-call-struct", src: func(n string) string {
		return `struct P { x: i32, y: i32, a:i32, b:i32, c:i32 }
@noinline function mk(v: i32): P { return P { x: v, y: v, a:0, b:0, c:0 }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "discarded-call-arr", src: func(n string) string {
		return `function mk(v: i32): i32[] { return [v, v + 1, v + 2]; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "call-arg-temp", src: func(n string) string {
		return `function sum3(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + sum3([i, i + 1, i + 2]); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "literal-alloc", src: func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let b: u8[] = __alloc_u8(8);
        b = b.with(0, (i % 200) as u8);
        acc = acc + (b[0] as i32);
        i = i + 1;
    }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "replaced-field", src: func(n string) string {
		return `struct S { name: string, n: i32 }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let o: S = S { name: "seed", n: 0 };
    let i: i32 = 0;
    while (i < ` + n + `) { o = S { name: "ab" + "cd", n: i }; i = i + 1; }
    if (o.name.len() != 4) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "tuple-temp", src: func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { let t: (i32, i32[]) = (i, [i + 1]); acc = acc + t.0 + t.1[0]; i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "map-get", src: func(n string) string {
		return `import "core/map";
function main(): i32 {
    let index: Map[string, i32] = map_new(64);
    index = index.insert("alpha", 1);
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        match (index.get("alpha")) { Some(g) => { acc = acc + g; }, None => { acc = acc - 1; } }
        match (index.get("absent")) { Some(g) => { acc = acc + g; }, None => { acc = acc + 2; } }
        i = i + 1;
    }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{name: "nested-array", src: func(n string) string {
		return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { let g: i32[][] = [[i, i + 1], [i + 2]]; acc = acc + g[0].len() + g[1].len(); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
}

const (
	heapBumpFlatSmallN = "50"
	heapBumpFlatLargeN = "200"
)

func TestSelfHostHeapBumpFlatIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")
	compile := func(t *testing.T, src string) string {
		t.Helper()
		asm, stderr, err := loadCompile(t, runner, driverBin, src, nil)
		if err != nil {
			t.Fatalf("compile: %v\n%s", err, stderr)
		}
		return asm
	}

	for _, tc := range heapBumpFlatCases {
		t.Run(tc.name, func(t *testing.T) {
			run := func(n string) int {
				src := tc.src(n) + "\n"
				asm := compile(t, src)
				bin := buildBin(t, gcc, dir, fmt.Sprintf("hbf_%s_%s", strings.ReplaceAll(tc.name, "-", "_"), n), asm)
				_, exit := hevRun(t, runner, bin)
				return exit
			}
			small, large := run(heapBumpFlatSmallN), run(heapBumpFlatLargeN)
			if small == 0 {
				if tc.retained == nil {
					t.Fatalf("%s allocated nothing at N=%s — the probe is not exercising the path, so the "+
						"flatness below would hold vacuously", tc.name, heapBumpFlatSmallN)
				}
				src := tc.retained(heapBumpFlatSmallN) + "\n"
				asm := compile(t, src)
				bin := buildBin(t, gcc, dir, fmt.Sprintf("hbf_%s_retained", strings.ReplaceAll(tc.name, "-", "_")), asm)
				if _, exit := hevRun(t, runner, bin); exit != 1 {
					t.Fatalf("%s: retained control returned %d, want 1 — the shape must still allocate "+
						"(200=length mismatch, 0=the bump did not move, so flatness proves nothing)", tc.name, exit)
				}
			}
			if small != large {
				t.Errorf("%s is not flat: N=%s bumped %d bytes, N=%s bumped %d — the mark tracks the "+
					"iteration count, so the shape strands one allocation per round",
					tc.name, heapBumpFlatSmallN, small, heapBumpFlatLargeN, large)
			}
		})
	}
}

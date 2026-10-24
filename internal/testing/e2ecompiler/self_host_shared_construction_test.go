package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

const sharedConstructionProgram = `struct Range { lo: i32, hi: i32, name: string }
struct Tag { name: string, level: i32 }
function span(lo: i32, n: i32, name: string): Range {
    let hi: i32 = lo;
    let i: i32 = 0;
    while (i < n) { hi = hi + 2; i = i + 1; }
    return Range { lo: lo, hi: hi, name: name };
}
function keep(lo: i32, n: i32, name: string): Range {
    let hi: i32 = lo;
    let i: i32 = 0;
    while (i < n) { hi = hi + 3; i = i + 1; }
    return Range { lo: lo, hi: hi, name: name };
}
function tag(name: string, level: i32): Tag { return Tag { name: name, level: level }; }
function wide(lo: i32, n: i32, name: string): Range {
    let hi: i32 = lo;
    let i: i32 = 0;
    while (i < n) {
        hi = (hi * 3 + i + 1) % 9973;
        hi = (hi * 4 + i + 2) % 9973;
        hi = (hi * 5 + i + 3) % 9973;
        hi = (hi * 6 + i + 4) % 9973;
        hi = (hi * 7 + i + 5) % 9973;
        hi = (hi * 8 + i + 6) % 9973;
        hi = (hi * 9 + i + 7) % 9973;
        hi = (hi * 10 + i + 8) % 9973;
        hi = (hi * 11 + i + 9) % 9973;
        hi = (hi * 12 + i + 10) % 9973;
        hi = (hi * 13 + i + 11) % 9973;
        hi = (hi * 14 + i + 12) % 9973;
        hi = (hi * 15 + i + 13) % 9973;
        hi = (hi * 16 + i + 14) % 9973;
        hi = (hi * 17 + i + 15) % 9973;
        hi = (hi * 18 + i + 16) % 9973;
        hi = (hi * 19 + i + 17) % 9973;
        hi = (hi * 20 + i + 18) % 9973;
        hi = (hi * 21 + i + 19) % 9973;
        hi = (hi * 22 + i + 20) % 9973;
        hi = (hi * 23 + i + 21) % 9973;
        hi = (hi * 24 + i + 22) % 9973;
        hi = (hi * 25 + i + 23) % 9973;
        hi = (hi * 26 + i + 24) % 9973;
        i = i + 1;
    }
    return Range { lo: lo, hi: hi, name: name };
}
@noinline function make(i: i32): Range { return keep(i, 1, "k"); }
@noinline function apart_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = span(i, 3, "ab"); t = t + s.hi - s.lo + s.name.len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function apart_again(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = span(i, i % 4, "abc"); t = t + s.hi + s.name.len() - s.lo; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function apart_keep(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = keep(i, 2, "k"); t = t + s.hi - s.lo + s.name.len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function whole_keep(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = make(i); t = t + s.hi - s.lo; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function lit_tags(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let g: Tag = tag("prio", 3); t = t + g.level + g.name.len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function dyn_tags(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let g: Tag = tag("prio", i); t = t + g.level + g.name.len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function apart_wide(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = wide(i, 2, "w"); t = (t + s.hi - s.lo + s.name.len()) % 1000; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function apart_wide_again(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = wide(i, 1, "wv"); t = (t + s.hi + s.name.len() - s.lo) % 1000; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(apart_rounds()); print("");
    print_int(apart_again()); print("");
    print_int(apart_keep()); print("");
    print_int(whole_keep()); print("");
    print_int(lit_tags()); print("");
    print_int(dyn_tags()); print("");
    print_int(apart_wide()); print("");
    print_int(apart_wide_again()); print("");
    return 0;
}
`

var sharedConstructionProduced = []string{"span", "keep", "tag", "make", "apart_rounds", "apart_again", "apart_keep", "whole_keep", "lit_tags", "dyn_tags", "wide", "apart_wide", "apart_wide_again"}

func TestSelfHostSharedConstructions(t *testing.T) {
	runSemanticProgram(t, "sharedctor", sharedConstructionProgram, sharedConstructionProduced,
		semInlineWants("800000\n600000\n700000\n300100\n700000\n5350000\n25100\n719100\n"), "keep", "make", "wide")
}

func TestSelfHostSharedConstructionsOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "sharedctor-off", sharedConstructionProgram, sharedConstructionProduced,
		semInlineWants("800100\n600100\n700100\n300100\n700100\n5350100\n25100\n719100\n"))
}

func sharedBudgetProgram() (program string, chain int32) {
	const lines = 600
	var b strings.Builder
	b.WriteString(`struct Range { lo: i32, hi: i32, name: string }
function span(lo: i32, n: i32, name: string): Range {
    let hi: i32 = lo;
    let i: i32 = 0;
    while (i < n) { hi = hi + 2; i = i + 1; }
    return Range { lo: lo, hi: hi, name: name };
}
function span_once(lo: i32, n: i32, name: string): Range {
    let hi: i32 = lo;
    let i: i32 = 0;
    while (i < n) { hi = hi + 3; i = i + 1; }
    return Range { lo: lo, hi: hi, name: name };
}
@noinline function apart_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = span(i, 3, "ab"); t = t + s.hi - s.lo + s.name.len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function recur(lo: i32, n: i32, name: string): Range {
    if (n == 0) { return Range { lo: lo, hi: lo + 6, name: name }; }
    let r: Range = recur(lo, n - 1, name);
    return Range { lo: r.lo, hi: r.hi + 1, name: r.name };
}
@noinline function apart_recur(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = recur(i, 3, "ab"); t = t + s.hi - s.lo + s.name.len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function apart_recur_again(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let s: Range = recur(i, i % 4, "ab"); t = t + s.hi + s.name.len() - s.lo; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function apart_long(): (i32, i32) {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let k: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let s: Range = span(i, 3, "ab");
        let o: Range = span_once(i, 2, "ab");
        t = t + s.hi - s.lo + s.name.len() + o.hi - o.lo + o.name.len();
`)
	for i := 0; i < lines; i++ {
		b.WriteString("        k = (k * 3 + i + 1) % 9973;\n")
	}
	b.WriteString(`        i = i + 1;
    }
    return (t * 1000 + ((__heap_alloc_count() - before) as i32), k);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(apart_rounds()); print("");
    let long: (i32, i32) = apart_long();
    print_int(long.0); print("");
    print_int(long.1); print("");
    print_int(apart_recur()); print("");
    print_int(apart_recur_again()); print("");
    return 0;
}
`)
	for i := int32(0); i < 100; i++ {
		for j := 0; j < lines; j++ {
			chain = (chain*3 + i + 1) % 9973
		}
	}
	return b.String(), chain
}

var sharedBudgetProduced = []string{"span", "span_once", "apart_rounds", "apart_long", "recur", "apart_recur", "apart_recur_again"}

func TestSelfHostSharedSplicePastCallerBudget(t *testing.T) {
	program, chain := sharedBudgetProgram()
	runSemanticProgram(t, "sharedbudget", program, sharedBudgetProduced,
		semInlineWants(fmt.Sprintf("800000\n1600000\n%d\n1100100\n950100\n", chain)), "recur")
}

func TestSelfHostSharedSplicePastCallerBudgetOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	program, chain := sharedBudgetProgram()
	runSemanticProgram(t, "sharedbudget-off", program, sharedBudgetProduced,
		semInlineWants(fmt.Sprintf("800100\n1600200\n%d\n1100100\n950100\n", chain)), "recur")
}

package e2ecompiler

import "testing"

// A function called from two places whose every return is a construction,
// one of them a box the pair return keeps, is spliced into each caller that
// takes the result apart once the functions called once have folded
// (seminline's shared functions), so the box is never built there. span is
// called from apart_rounds and apart_again, which read its record's fields,
// and neither round allocates; keep is called from apart_keep, which reads
// it apart and allocates nothing, and from make, which returns it whole, so
// keep retains its body and whole_keep pays the record each round. tag is a
// constructor leaf called with literals from lit_tags, a static record
// there, and with a level computed in the loop from dyn_tags, where it is
// spliced as a shared function and allocates nothing either. wide has the
// shape of span but a body past the shared budget, so it stays a call from
// apart_wide and apart_wide_again and each of their rounds builds the record.
// Each probe prints its result times 1000 plus the allocations the rounds
// made.
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

// With the pass off, every call stays and every round builds its record.
func TestSelfHostSharedConstructionsOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "sharedctor-off", sharedConstructionProgram, sharedConstructionProduced,
		semInlineWants("800100\n600100\n700100\n300100\n700100\n5350100\n25100\n719100\n"))
}

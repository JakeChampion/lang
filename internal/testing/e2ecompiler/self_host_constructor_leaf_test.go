package e2ecompiler

import "testing"

const constructorLeafProgram = `struct Tag { name: string, level: i32 }
enum Note { Text(string), Blank }
struct Card { title: string, tag: Tag, note: Note, tags: string[] }
function tag(name: string, level: i32): Tag { return Tag { name: name, level: level }; }
function text(s: string): Note { return Text(s); }
function card(title: string, name: string, level: i32): Card {
    return Card { title: title, tag: tag(name, level), note: text(title), tags: [] };
}
function weigh(c: Card): i32 {
    let t: i32 = c.tag.level + c.title.len() + c.tags.len();
    match (c.note) { Text(s) => { t = t + s.len(); }, Blank => {} }
    return t;
}
@noinline function lit_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let c: Card = card("hello", "prio", 3); t = t + weigh(c); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function dyn_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let c: Card = card("hello", "prio", i); t = t + weigh(c); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(lit_rounds()); print("");
    print_int(dyn_rounds()); print("");
    return 0;
}
`

var constructorLeafProduced = []string{"tag", "text", "card", "weigh", "lit_rounds", "dyn_rounds"}

func TestSelfHostConstructorLeaves(t *testing.T) {
	runSemanticProgram(t, "ctorleaf", constructorLeafProgram, constructorLeafProduced,
		semInlineWants("1300000\n5950300\n"), "card")
}

func TestSelfHostConstructorLeavesOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "ctorleaf-off", constructorLeafProgram, constructorLeafProduced,
		semInlineWants("1300300\n5950300\n"))
}

const unionConstructorLeafProgram = `struct Known { width: i32 }
struct Unknown { reason: string }
type Ty = Known | Unknown;
function unknown(reason: string): Ty { return Unknown { reason: reason }; }
function weigh(t: Ty): i32 {
    if let Unknown(u) = t { return u.reason.len(); }
    return 1;
}
@noinline function lit_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + weigh(unknown("no type")); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function dyn_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let r: string = "a";
        if (i % 2 == 1) { r = "bb"; }
        t = t + weigh(unknown(r));
        i = i + 1;
    }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(lit_rounds()); print("");
    print_int(dyn_rounds()); print("");
    return 0;
}
`

var unionConstructorLeafProduced = []string{"unknown", "weigh", "lit_rounds", "dyn_rounds"}

func TestSelfHostConstructorLeafUnionMember(t *testing.T) {
	runSemanticProgram(t, "ctorleaf-union", unionConstructorLeafProgram, unionConstructorLeafProduced,
		semInlineWants("700000\n150100\n"), "unknown")
}

func TestSelfHostConstructorLeafUnionMemberOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "ctorleaf-union-off", unionConstructorLeafProgram, unionConstructorLeafProduced,
		semInlineWants("700100\n150100\n"))
}

const wideConstructorLeafProgram = `struct D { sec: i64, nsec: i64 }
struct T { a: D, b: D }
function secs(s: i64): D { return D { sec: s, nsec: 0 }; }
function bounds(): T { return T { a: secs(10 as i64), b: secs(30 as i64) }; }
function weigh(t: T): i32 { return (t.a.sec + t.b.sec + t.a.nsec) as i32; }
@noinline function lit_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + weigh(bounds()); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(lit_rounds()); print("");
    return 0;
}
`

var wideConstructorLeafProduced = []string{"secs", "bounds", "weigh", "lit_rounds"}

func TestSelfHostConstructorLeafWide(t *testing.T) {
	runSemanticProgram(t, "ctorleaf-wide", wideConstructorLeafProgram, wideConstructorLeafProduced,
		semInlineWants("4000000\n"))
}

func TestSelfHostConstructorLeafWideOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "ctorleaf-wide-off", wideConstructorLeafProgram, wideConstructorLeafProduced,
		semInlineWants("4000300\n"))
}

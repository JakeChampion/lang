package e2ecompiler

import "testing"

// A constructor leaf, a one-block function that builds its result from its
// parameters and literals alone, is spliced into the calls whose arguments
// are all literals (seminline.constructor_leaves), so the construction is of
// literals there and lands in the constant pool as a static box. card builds
// a record holding a record and a variant through two more constructor
// leaves, and an empty array; lit_rounds calls it with literals a hundred
// times and allocates nothing, where the same call with a level computed in
// the loop stays a call, and dyn_rounds pays the card, its tag and its note
// each round. Each probe prints its result times 1000 plus the allocations
// the rounds made. tag and text are called once, from card, so they are
// spliced there whatever their arguments; card keeps its body for the
// dynamic calls.
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

// With the pass off, every call stays and every round builds its three boxes.
func TestSelfHostConstructorLeavesOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "ctorleaf-off", constructorLeafProgram, constructorLeafProduced,
		semInlineWants("1300300\n5950300\n"))
}

// A constructor leaf may return a record as a member of a union of records,
// as the checker's `t_unknown(reason)` returns a `TypeUnknown` as a `Type`.
// Called with a literal it is a static box too: lit_rounds allocates nothing,
// while dyn_rounds, whose reason is a phi of two literals, pays one box a
// round.
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

// A constructor leaf with a wide field, called with an integer literal cast
// to its width, as std/fetch's `timeouts` calls std/time's
// `duration_seconds(10 as i64)`: the cast literal is a literal of the field's
// type, so the call is spliced and the record of records is static. Each
// round weighs the bounds and allocates nothing; with the pass off, a round
// builds its three boxes.
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

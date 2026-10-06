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
// the rounds made. The three constructors keep their bodies for the dynamic
// calls.
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
		semInlineWants("1300000\n5950300\n"), "tag", "text", "card")
}

// With the pass off, every call stays and every round builds its three boxes.
func TestSelfHostConstructorLeavesOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "ctorleaf-off", constructorLeafProgram, constructorLeafProduced,
		semInlineWants("1300300\n5950300\n"))
}

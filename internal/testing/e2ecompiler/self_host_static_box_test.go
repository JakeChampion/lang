package e2ecompiler

import "testing"

// A record or variant whose every field is a constant is one static box
// (#8920), as the AST lowering has placed struct literals since #6149. A
// string literal is a static box, so a field or element holding one is a
// constant too. Each probe runs 100 rounds and prints its result times 1000
// plus the heap allocations the rounds made. origin, the TInt and TVoid
// members, the Shade payload, named's string field and names' string elements
// allocate nothing; fresh holds a parameter and rebuild's first record a loop
// counter, so each still allocates once a round; rebuild hands that record to
// px, since one only read would never be built. rebuild's second record is
// constant, so the first one's box is
// released where it dies rather than held for a construction that no longer
// takes it. An empty array literal is one static box too: leaf's record
// allocates and its args do not, and later's push onto those args makes the
// allocation the literal no longer does. later's update counts its base, so
// it writes into leaf's fresh box rather than taking another. An unsigned
// literal arrives as its source text: u_small's 12 is placed, and u_big's
// 3000000000, past 2^31, is left to the heap, where its word is written at
// the field's width.
const staticBoxProgram = `struct P { x: i32, y: i32, on: boolean }
struct TInt { width: i32, signed: boolean }
struct TVoid {}
struct TName { n: string }
type Ty = TInt | TVoid | TName;
enum Shade { Dark, Light(i32), Mixed(i32, boolean) }
struct Inst { kind: i32, args: i32[] }
struct U { x: u32 }
@noinline function origin(): P { return P { x: 0 - 3, y: 7, on: true }; }
@noinline function t_int(): Ty { return TInt { width: 32, signed: true }; }
@noinline function t_void(): Ty { return TVoid {}; }
@noinline function named(): Ty { return TName { n: "q" }; }
@noinline function mixed(): Shade { return Shade.Mixed(0 - 2, true); }
@noinline function fresh(i: i32): P { return P { x: i, y: 1, on: false }; }
@noinline function width(t: Ty): i32 {
    if let TInt(i) = t { if (i.signed) { return i.width; } return 0 - i.width; }
    if let TName(nm) = t { return nm.n.len() + 100; }
    return 1;
}
@noinline function shade(s: Shade): i32 {
    match (s) {
        Shade.Dark => { return 0; },
        Shade.Light(n) => { return n; },
        Shade.Mixed(n, b) => { if (b) { return n * 10; } return n; }
    }
}
@noinline function px(p: P): i32 { return p.x; }
@noinline function rebuild(i: i32): i32 {
    let a: P = P { x: i, y: i, on: true };
    let n: i32 = px(a);
    let b: P = P { x: 4, y: 5, on: false };
    return n + b.y;
}
@noinline function leaf(k: i32): Inst { return Inst { kind: k, args: [] }; }
@noinline function later(i: Inst, v: i32): Inst { return Inst { ...i, args: i.args.append(v) }; }
@noinline function u_small(): U { return U { x: 12 }; }
@noinline function u_big(): U { return U { x: 3000000000 }; }
@noinline function unsigned_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + (u_small().x as i32) + ((u_big().x / 1000000) as i32); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function leaf_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let x: Inst = leaf(i); t = t + x.args.len() + x.kind; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function later_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let x: Inst = later(leaf(i), 7); t = t + x.args.len() + x.args[0]; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function origin_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let p: P = origin(); t = t + p.x + p.y; if (p.on) { t = t + 1; } i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function member_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + width(t_int()) + width(t_void()); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function shade_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + shade(mixed()); i = i + 1; }
    return (0 - t) * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function named_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + width(named()); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function fresh_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + fresh(i).x; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function rebuild_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + rebuild(i); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function lookup(k: i32): i32 {
    let below: i32[] = [6, 11, 0 - 12];
    for e in below { if (k == e) { return 1; } }
    return 0;
}
@noinline function table(): i32[] { return [4, 5, 6]; }
function seven(): i32 { return 7; }
@noinline function spliced(): i32[] { return [seven(), 8]; }
@noinline function flags(): boolean[] { return [true, false, true]; }
@noinline function names(): string[] { return ["a", "b"]; }
@noinline function lookup_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + lookup(i) + lookup(0 - i); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function pushed_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let xs: i32[] = table().append(i); t = t + xs.len() + xs[0]; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function set_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let xs: i32[] = table().with(0, 100); t = t + xs[0] + table()[0]; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function flag_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let fs: boolean[] = flags(); if (fs[0] && !fs[1]) { t = t + fs.len(); } i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function spliced_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + spliced()[0]; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function name_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + names().len(); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(origin_rounds()); print("");
    print_int(member_rounds()); print("");
    print_int(shade_rounds()); print("");
    print_int(named_rounds()); print("");
    print_int(fresh_rounds()); print("");
    print_int(rebuild_rounds()); print("");
    print_int(leaf_rounds()); print("");
    print_int(later_rounds()); print("");
    print_int(unsigned_rounds()); print("");
    print_int(lookup_rounds()); print("");
    print_int(pushed_rounds()); print("");
    print_int(set_rounds()); print("");
    print_int(flag_rounds()); print("");
    print_int(name_rounds()); print("");
    print_int(spliced_rounds()); print("");
    return 0;
}
`

func TestSelfHostStaticBoxes(t *testing.T) {
	// Before static boxes the first three lines ended 100, 200 and 100, and
	// leaf_rounds ended 200.
	want := "500000\n3300000\n2000000\n10100000\n4950100\n5450100\n4950100\n800200\n301200100\n3000\n800100\n10400100\n300000\n200000\n700000\n"
	runSemanticProgram(t, "staticbox", staticBoxProgram,
		[]string{"origin", "t_int", "t_void", "named", "mixed", "fresh", "width", "shade", "rebuild",
			"origin_rounds", "member_rounds", "shade_rounds", "named_rounds", "fresh_rounds", "rebuild_rounds",
			"leaf", "later", "leaf_rounds", "later_rounds", "u_small", "u_big", "unsigned_rounds",
			"lookup", "table", "flags", "names", "lookup_rounds", "pushed_rounds", "set_rounds", "flag_rounds", "name_rounds",
			"seven", "spliced", "spliced_rounds"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}

// A literal whose storage the body hands to the raw floor is built fresh
// rather than placed: the floor writes through a data pointer with no count,
// so a shared box would carry the write into every evaluation of an equal
// literal. `bytes_rounds` overwrites a `u8[]` literal's length through its data
// pointer each round and reads it before and after. `poke_len` does the same
// to an `i32[]` literal with `__store_i32`, and `fresh_len`, which never
// touches the floor, must still read its own equal literals' lengths.
// `shorten_lit` and `raw_ptr_len` reach the floor on an equal literal through
// `__arr_set_len` and `__raw_arr_ptr`; if either were placed, `fresh_len` would
// read something other than 33. `shorten` calls `__arr_set_len` on a heap
// buffer, which lends it, so its body is produced. Wasm is left out: it has no
// emit arm for the raw byte store.
const byteLiteralProgram = `@noinline function shorten(n: i32): i32 {
    let b: u8[] = __alloc_u8(4);
    __arr_set_len(b, n);
    return b.len();
}
@noinline function poke_len(): i32 {
    let a: i32[] = [7, 8, 9];
    __store_i32(a as usize, 99);
    return a.len();
}
@noinline function shorten_lit(): i32 {
    let a: u8[] = [7u8, 8u8, 9u8];
    __arr_set_len(a, 2);
    return a.len();
}
@noinline function raw_ptr_len(): i32 {
    let a: i32[] = [7, 8, 9];
    __store_i32(__raw_arr_ptr(a), 99);
    return a.len();
}
@noinline function fresh_len(): i32 {
    let v: i32[] = [7, 8, 9];
    let w: u8[] = [7u8, 8u8, 9u8];
    return v.len() * 10 + w.len();
}
@noinline function bytes_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let b: u8[] = [104u8, 105u8, 106u8];
        t = t + b.len();
        __store_u8(b as usize, 1);
        t = t + b.len();
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
    print_int(bytes_rounds()); print("");
    print_int(shorten(2)); print("");
    print_int(poke_len()); print("");
    print_int(shorten_lit()); print("");
    print_int(raw_ptr_len()); print("");
    print_int(fresh_len()); print("");
    return 0;
}
`

func TestSelfHostRawReachedLiteralIsFresh(t *testing.T) {
	want := "400100\n2\n99\n2\n99\n33\n"
	runSemanticProgram(t, "rawliteral", byteLiteralProgram, []string{"bytes_rounds", "shorten", "poke_len", "shorten_lit", "raw_ptr_len", "fresh_len"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want})
}

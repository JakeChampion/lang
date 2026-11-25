package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A record of a `@secret` struct zeroes each numeric array that it holds
// alone when it is released (SEC-025). The arrays here are 48 bytes, which
// is 72 bytes with the header, and a raw allocation of 72 bytes takes a
// freed block of that class back without a zero-fill. The control is the same
// record without the attribute: it must read the secret bytes back. "shared"
// returns the byte array from the key, so that array must keep its bytes.
const secretSrc = `@secret
struct Key { bytes: u8[], words: u64[], n: i32 }

struct Plain { bytes: u8[], words: u64[], n: i32 }

function fill(n: i32): u8[] {
  let b: u8[] = __alloc_u8(n);
  for i in 0..n {
    b = b.with(i, 65 as u8);
  }
  return b;
}

function words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}

@noinline
function key(v: u64): i32 {
  let k = Key { bytes: fill(48), words: words(v), n: 1 };
  return k.bytes.len() + k.words.len();
}

@noinline
function plain(v: u64): i32 {
  let k = Plain { bytes: fill(48), words: words(v), n: 1 };
  return k.bytes.len() + k.words.len();
}

@noinline
function shared(v: u64): u8[] {
  let b = fill(48);
  let k = Key { bytes: b, words: words(v), n: 1 };
  let n = k.n;
  return b;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for k in 0..8 {
      let p: usize = __alloc(72);
      for i in 0..72 {
        if (__load_u8(p + (i as usize)) == 65 as u8) { found += 1; }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983744u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "shared") {
    let b = shared(word);
    print(b[0].to_string() + " " + b[47].to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

// A record of a `@secret` struct also zeroes its scalar fields when it is
// released. The record has seven fields, and a raw allocation of 64 bytes
// takes its freed box back. The fields `a`, `b`, `e` and `f` and the four
// bytes of `c` hold the byte 65, which is 36 bytes. The control is the same
// record without the attribute.
const secretScalarsSrc = `@secret
struct Key { a: u64, b: i64, c: i32, d: f64, e: u64, f: u64, g: boolean }

struct Plain { a: u64, b: i64, c: i32, d: f64, e: u64, f: u64, g: boolean }

@noinline
function make_key(v: u64): Key {
  return Key { a: v, b: v as i64, c: 1094795585, d: 1.5, e: v, f: v, g: true };
}

@noinline
function make_plain(v: u64): Plain {
  return Plain { a: v, b: v as i64, c: 1094795585, d: 1.5, e: v, f: v, g: true };
}

@noinline
function sum_key(k: Key): u64 {
  return k.a + k.e + k.f + (k.b as u64) + (k.c as u64);
}

@noinline
function sum_plain(k: Plain): u64 {
  return k.a + k.e + k.f + (k.b as u64) + (k.c as u64);
}

@noinline
function key(v: u64): u64 {
  return sum_key(make_key(v));
}

@noinline
function plain(v: u64): u64 {
  return sum_plain(make_plain(v));
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for k in 0..8 {
      let p: usize = __alloc(64);
      for i in 0..64 {
        if (__load_u8(p + (i as usize)) == 65 as u8) { found += 1; }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983745u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

// A record of a `@secret` struct also zeroes the scalars and the numeric
// arrays of a nested record and of a tuple that it holds alone. The scan
// takes raw blocks of each size class from 24 to 72 bytes, so it finds the
// box of the nested record, the box of the tuple and their arrays. The
// control is the same record without the attribute. "shared" returns the
// nested record from the key, so that record must keep its fields.
const secretNestedSrc = `struct Half { a: u64, b: u64, c: u64, words: u64[] }

@secret
struct Key { half: Half, pair: (u64, u64, u64[]), n: i32 }

struct Plain { half: Half, pair: (u64, u64, u64[]), n: i32 }

@noinline
function words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}

@noinline
function half(v: u64): Half {
  return Half { a: v, b: v, c: v, words: words(v) };
}

@noinline
function pair(v: u64): (u64, u64, u64[]) {
  return (v, v, words(v));
}

@noinline
function key(v: u64): u64 {
  let k = Key { half: half(v), pair: pair(v), n: 1 };
  return k.half.a + k.pair.0 + k.pair.2[5] + k.half.words[5];
}

@noinline
function plain(v: u64): u64 {
  let k = Plain { half: half(v), pair: pair(v), n: 1 };
  return k.half.a + k.pair.0 + k.pair.2[5] + k.half.words[5];
}

@noinline
function shared(v: u64): Half {
  let h = half(v);
  let k = Key { half: h, pair: pair(v), n: 1 };
  let n = k.n;
  return h;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 3..10 {
      for k in 0..8 {
        let p: usize = __alloc(size * 8);
        for i in 0..size * 8 {
          if (__load_u8(p + (i as usize)) == 65 as u8) { found += 1; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983745u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "shared") {
    let h = shared(word);
    print((h.a == word && h.c == word && h.words[5] == word).to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

// A record of a `@secret` struct also zeroes the fields of an enum value
// that it holds alone: the scalars and the numeric arrays of a variant, and
// the payload of an `Option`, which can be an array or any scalar. A field of
// the record that is `None` points to a static box, and a byte of that
// address can be 65, so the scan counts a byte only when the other byte of
// its pair is also 65. The control is the same record without the
// attribute. "shared" returns the enum value from the key, so that value
// must keep its fields.
const secretEnumSrc = `import "std/option";

enum Part { Words(u64, u64, u64[]), Small(i32, u64), Empty }

@secret
struct Key { part: Part, other: Part, spare: Option[u64[]], small: Option[i32], none: Option[u64[]], wide: Option[u64], real: Option[f64], n: i32 }

struct Plain { part: Part, other: Part, spare: Option[u64[]], small: Option[i32], none: Option[u64[]], wide: Option[u64], real: Option[f64], n: i32 }

@noinline
function words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}

@noinline
function part(v: u64): Part {
  return Words(v, v, words(v));
}

@noinline
function small(v: u64): Part {
  return Small(1094795585, v);
}

@noinline
function spare(v: u64): Option[u64[]] {
  return Some(words(v));
}

@noinline
function few(v: u64): Option[i32] {
  return if (v > 0u64) { Some(1094795585) } else { None };
}

@noinline
function wide(v: u64): Option[u64] {
  return Some(v);
}

@noinline
function real(v: u64): Option[f64] {
  return Some(f64_from_bits(v as i64));
}

function weigh(p: Part): u64 {
  return match (p) {
    Words(a, b, ws) => a + b + ws[5],
    Small(a, b) => (a as u64) + b,
    Empty => 0u64,
  };
}

// The record goes to a call, so it stays a box that holds its two options.
@noinline
function key_words(k: Key): u64 {
  return k.wide.unwrap_or(0u64) + (k.real.unwrap_or(0.0) as u64);
}

@noinline
function plain_words(k: Plain): u64 {
  return k.wide.unwrap_or(0u64) + (k.real.unwrap_or(0.0) as u64);
}

@noinline
function key(v: u64): u64 {
  let k = Key { part: part(v), other: small(v), spare: spare(v), small: few(v), none: None, wide: wide(v), real: real(v), n: 1 };
  return weigh(k.part) + weigh(k.other) + k.spare.map((ws: u64[]): u64 => ws[5]).unwrap_or(0u64) + (k.small.unwrap_or(0) as u64) + key_words(k);
}

@noinline
function plain(v: u64): u64 {
  let k = Plain { part: part(v), other: small(v), spare: spare(v), small: few(v), none: None, wide: wide(v), real: real(v), n: 1 };
  return weigh(k.part) + weigh(k.other) + k.spare.map((ws: u64[]): u64 => ws[5]).unwrap_or(0u64) + (k.small.unwrap_or(0) as u64) + plain_words(k);
}

@noinline
function shared(v: u64): Part {
  let p = part(v);
  let k = Key { part: p, other: Empty, spare: None, small: None, none: None, wide: None, real: None, n: 1 };
  let n = k.n;
  return p;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 2..24 {
      for k in 0..8 {
        let p: usize = __alloc(size * 8);
        for i in 0..size * 8 {
          if (__load_u8(p + (i as usize)) == 65 as u8 && __load_u8(p + ((i ^ 1) as usize)) == 65 as u8) { found += 1; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983745u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "shared") {
    let p = shared(word);
    print((weigh(p) == word + word + word).to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

// A record of a `@secret` struct also zeroes the bytes of a string that it
// holds alone. The string is built at run time, so its 48 bytes follow its
// box in one block. The bytes it is built from are zeroed, so only the
// string can show them. "literal" puts a string literal in the field, which is
// read-only and must keep its bytes. "shared" returns the string from the
// key, so that string must keep its bytes. The control is the same record
// without the attribute.
const secretStringSrc = `@secret
struct Key { text: string, label: string, n: i32 }

struct Plain { text: string, label: string, n: i32 }

@noinline
function text(n: i32): string {
  let b: u8[] = __alloc_u8(n);
  for i in 0..n {
    b = b.with(i, 65 as u8);
  }
  let s = unsafe { string_from_bytes_unchecked(b) };
  for i in 0..n {
    b = b.with(i, 0 as u8);
  }
  return s;
}

@noinline
function key(n: i32): i32 {
  let k = Key { text: text(n), label: "AAAA", n: 1 };
  return k.text.len() + k.label.len();
}

@noinline
function plain(n: i32): i32 {
  let k = Plain { text: text(n), label: "AAAA", n: 1 };
  return k.text.len() + k.label.len();
}

@noinline
function shared(n: i32): string {
  let s = text(n);
  let k = Key { text: s, label: "AAAA", n: 1 };
  let m = k.n;
  return s;
}

@noinline
function literal(): i32 {
  let k = Key { text: "AAAAAAAA", label: "AAAA", n: 1 };
  return k.text.len() + k.label.len();
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 2..24 {
      for k in 0..8 {
        let p: usize = __alloc(size * 8);
        for i in 0..size * 8 {
          if (__load_u8(p + (i as usize)) == 65 as u8 && __load_u8(p + ((i ^ 1) as usize)) == 65 as u8) { found += 1; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let n: i32 = 46 + args().len();
  if (mode == "secret") {
    let m = key(n);
  } else if (mode == "shared") {
    let s = shared(n);
    print((s.len() == 48 && s == text(48)).to_string());
  } else if (mode == "literal") {
    let m = literal();
    print(("AAAAAAAA".len() + m).to_string());
  } else {
    let m = plain(n);
  }
  print(stale().to_string());
  return 0;
}
`

// The strings that the runtime builds keep their bytes in the block of the
// string, so the drop of a `@secret` record wipes them: a file that
// read_file reads, a repeat, a concatenation, and a string that an append
// grows into a block with spare capacity. No step releases a secret byte outside the record, so the secret
// side must read none back. A mapped file is a read-only mapping of the
// file: the drop must leave it, and the file keeps its bytes. KEYFILE holds
// 56 bytes "A".
const secretBuiltStringSrc = `import "std/string";

@secret
struct Key { text: string, n: i32 }

struct Plain { text: string, n: i32 }

@noinline
function join2(a: string, b: string): string {
  return a + b;
}

@noinline
function build(how: string, n: i32): string {
  if (how == "file") {
    match (read_file("KEYFILE")) {
      Ok(s) => { return s; },
      Err(e) => { return ""; },
    }
  }
  if (how == "repeat") {
    return "A".repeat(n);
  }
  if (how == "concat") {
    return join2("AAAAAAAAAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAAAAAAAA");
  }
  let s = join2("BBBBBBBBBBBBBB", "BBBBBBBBBBBBBB");
  s = s + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
  return s;
}

@noinline
function key(how: string, n: i32): i32 {
  let k = Key { text: build(how, n), n: 1 };
  return k.text.len();
}

@noinline
function plain(how: string, n: i32): i32 {
  let k = Plain { text: build(how, n), n: 1 };
  return k.text.len();
}

@noinline
function mapped(): i32 {
  let file = unsafe { map_file_unchecked("KEYFILE") };
  match (file) {
    Ok(s) => {
      let k = Key { text: s, n: 1 };
      return k.text.len();
    },
    Err(e) => { return -1; },
  }
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 2..24 {
      for k in 0..8 {
        let p: usize = __alloc(size * 8);
        for i in 0..size * 8 {
          if (__load_u8(p + (i as usize)) == 65 as u8 && __load_u8(p + ((i ^ 1) as usize)) == 65 as u8) { found += 1; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let n: i32 = 54 + args().len();
  if (mode == "mapped") {
    let m = mapped();
    match (read_file("KEYFILE")) {
      Ok(s) => { print((m == 56 && s == "A".repeat(56)).to_string()); },
      Err(e) => { print("false"); },
    }
    return 0;
  }
  if (mode.starts_with("secret-")) {
    let m = key(mode.drop(7), n);
  } else {
    let m = plain(mode, n);
  }
  print(stale().to_string());
  return 0;
}
`

// A record of a `@secret` struct also wipes each element of an array of
// boxes that it holds alone: here the rows of a `u8[][]`, and the byte array
// and the scalar of each record in a `Sub[]`. The probe reads back the
// released blocks of each size class. "shared" returns one row from the key,
// so that row must keep its bytes. The control is the same record without
// the attribute.
const secretElementsSrc = `struct Sub { bytes: u8[], tag: u64 }

@secret
struct Key { rows: u8[][], subs: Sub[], n: i32 }

struct Plain { rows: u8[][], subs: Sub[], n: i32 }

function fill(n: i32): u8[] {
  let b: u8[] = __alloc_u8(n);
  for i in 0..n {
    b = b.with(i, 65 as u8);
  }
  return b;
}

@noinline
function key(v: u64): i32 {
  let k = Key { rows: [fill(48), fill(48)], subs: [Sub { bytes: fill(48), tag: v }], n: 1 };
  return k.rows.len() + k.subs.len();
}

@noinline
function plain(v: u64): i32 {
  let k = Plain { rows: [fill(48), fill(48)], subs: [Sub { bytes: fill(48), tag: v }], n: 1 };
  return k.rows.len() + k.subs.len();
}

@noinline
function shared(v: u64): u8[] {
  let b = fill(48);
  let k = Key { rows: [b, fill(48)], subs: [Sub { bytes: fill(48), tag: v }], n: 1 };
  let n = k.n;
  return b;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 2..24 {
      for k in 0..8 {
        let p: usize = __alloc(size * 8);
        for i in 0..size * 8 {
          if (__load_u8(p + (i as usize)) == 65 as u8 && __load_u8(p + ((i ^ 1) as usize)) == 65 as u8) { found += 1; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983745u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "shared") {
    let b = shared(word);
    print(b[0].to_string() + " " + b[47].to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

// secretRun compiles src for target and gives a function that runs the
// program in one mode and splits its output into fields.
// A record of a `@secret` struct also wipes a value of a `type A | B` union
// that it holds alone: the scalars and the array of a Pass, and the scalars
// of a Token. "held" keeps a `@secret` Seed in a union inside a plain record,
// so the drop of the union must run the wipe of Seed. "shared" returns the
// Pass from the key, so the Pass must keep its fields. The control is the
// same record without the attribute.
const secretUnionSrc = `struct Pass { a: u64, b: u64, ws: u64[] }

struct Token { t: u64, n: i32 }

@secret
struct Seed { s: u64, ws: u64[] }

type Cred = Pass | Token;

type Held = Seed | Token;

@secret
struct Key { cred: Cred, other: Cred, n: i32 }

struct Plain { cred: Cred, other: Cred, n: i32 }

struct Box { held: Held, n: i32 }

@noinline
function words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}

@noinline
function pass(v: u64): Cred {
  return Pass { a: v, b: v, ws: words(v) };
}

@noinline
function token(v: u64): Cred {
  return Token { t: v, n: 1094795585 };
}

@noinline
function seed(v: u64): Held {
  return Seed { s: v, ws: words(v) };
}

function weigh(c: Cred): u64 {
  return match (c) {
    Pass(p) => p.a + p.b + p.ws[5],
    Token(t) => t.t + (t.n as u64),
  };
}

function weigh_held(h: Held): u64 {
  return match (h) {
    Seed(s) => s.s + s.ws[5],
    Token(t) => t.t,
  };
}

@noinline
function key(v: u64): u64 {
  let k = Key { cred: pass(v), other: token(v), n: 1 };
  return weigh(k.cred) + weigh(k.other);
}

@noinline
function plain(v: u64): u64 {
  let k = Plain { cred: pass(v), other: token(v), n: 1 };
  return weigh(k.cred) + weigh(k.other);
}

@noinline
function held(v: u64): u64 {
  let b = Box { held: seed(v), n: 1 };
  return weigh_held(b.held);
}

@noinline
function shared(v: u64): Cred {
  let c = pass(v);
  let k = Key { cred: c, other: token(0u64), n: 1 };
  let n = k.n;
  return c;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 2..24 {
      for k in 0..8 {
        let p: usize = __alloc(size * 8);
        for i in 0..size * 8 {
          if (__load_u8(p + (i as usize)) == 65 as u8 && __load_u8(p + ((i ^ 1) as usize)) == 65 as u8) { found += 1; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983745u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "held") {
    let n = held(word);
  } else if (mode == "shared") {
    let c = shared(word);
    print((weigh(c) == word + word + word).to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

// The drop of a `type A | B` union value runs the finalizer of its member
// type: Res holds a string, and Bare holds only a scalar. Alt has no
// finalizer.
const unionMemberDropSrc = `import "core/mem";

struct Res { id: i32, name: string }

struct Alt { v: i32 }

struct Bare { id: i32 }

impl mem.Drop for Res {
    @noinline function drop(self: Self): void { print("res " + self.name); }
}

impl mem.Drop for Bare {
    @noinline function drop(self: Self): void { print("bare " + self.id.to_string()); }
}

type Any = Res | Alt | Bare;

@noinline function make(n: i32): Any {
    if (n % 3 == 1) {
        return Res { id: n, name: "r" + n.to_string() };
    }
    if (n % 3 == 2) {
        return Bare { id: n };
    }
    return Alt { v: n };
}

@noinline function weigh(a: Any): i32 {
    return match (a) { Res(r) => r.id, Alt(x) => x.v, Bare(b) => b.id };
}

function main(): i32 {
    let total = 0;
    for i in 0..4 {
        let a = make(i);
        total += weigh(a);
    }
    let kept: Any[] = [make(7), make(8), make(9)];
    total += kept.len();
    print("end " + total.to_string());
    return 0;
}
`

// A record of a `@secret` struct also wipes a field of a record or an enum
// type with a finalizer when it holds the field alone. The finalizer runs
// first and prints whether the fields are still whole, and the wipe follows.
// The control is the same record without the attribute. "shared" returns the
// record from the key, so that record must keep its fields.
const secretFinalizedSrc = `import "core/mem";

struct Res { a: u64, b: u64, words: u64[] }

enum Part { Words(u64, u64[]), Empty }

impl mem.Drop for Res {
  @noinline function drop(self: Self): void {
    print("res " + (self.a == self.b && self.words[5] == self.a).to_string());
  }
}

impl mem.Drop for Part {
  @noinline function drop(self: Self): void {
    let whole = match (self) { Words(v, w) => w[5] == v, Empty => false };
    print("part " + whole.to_string());
  }
}

@secret
struct Key { res: Res, part: Part, n: i32 }

struct Plain { res: Res, part: Part, n: i32 }

@noinline
function words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}

@noinline
function res(v: u64): Res {
  return Res { a: v, b: v, words: words(v) };
}

@noinline
function part(v: u64): Part {
  return Words(v, words(v));
}

@noinline
function key(v: u64): i32 {
  let k = Key { res: res(v), part: part(v), n: 1 };
  return k.n;
}

@noinline
function plain(v: u64): i32 {
  let k = Plain { res: res(v), part: part(v), n: 1 };
  return k.n;
}

@noinline
function shared(v: u64): Res {
  let r = res(v);
  let k = Key { res: r, part: part(v), n: 1 };
  let n = k.n;
  return r;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 3..10 {
      for k in 0..8 {
        let p: usize = __alloc(size * 8);
        for i in 0..size * 8 {
          if (__load_u8(p + (i as usize)) == 65 as u8) { found += 1; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983745u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "shared") {
    let r = shared(word);
    print((r.a == word && r.words[5] == word).to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

// secretDeepFinalizedSrc holds a record with a finalizer inside a nested
// record, a tuple, an array, an enum and a recursive enum.
const secretDeepFinalizedSrc = `import "core/mem";

struct Res { a: u64, b: u64, words: u64[] }

impl mem.Drop for Res {
  @noinline function drop(self: Self): void {
    print("res " + (self.a == self.b && self.words[5] == self.a).to_string());
  }
}

struct Outer { res: Res, tag: u64 }

enum Holder { Has(Res, u64), Nothing }

enum Chain { Link(Res, Chain), End }

@secret
struct Key { outer: Outer, pair: (Res, u64), list: Res[], holder: Holder, chain: Chain, n: i32 }

struct Plain { outer: Outer, pair: (Res, u64), list: Res[], holder: Holder, chain: Chain, n: i32 }

@noinline
function words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}

@noinline
function res(v: u64): Res {
  return Res { a: v, b: v, words: words(v) };
}

@noinline
function key(v: u64): i32 {
  let k = Key { outer: Outer { res: res(v), tag: v }, pair: (res(v), v), list: [res(v), res(v)], holder: Has(res(v), v), chain: Link(res(v), End), n: 1 };
  return k.n;
}

@noinline
function plain(v: u64): i32 {
  let k = Plain { outer: Outer { res: res(v), tag: v }, pair: (res(v), v), list: [res(v), res(v)], holder: Has(res(v), v), chain: Link(res(v), End), n: 1 };
  return k.n;
}

@noinline
function shared(v: u64): Outer {
  let o = Outer { res: res(v), tag: v };
  let k = Key { outer: o, pair: (res(v), v), list: [], holder: Nothing, chain: End, n: 1 };
  let n = k.n;
  return o;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for size in 3..10 {
      for k in 0..12 {
        let p: usize = __alloc(size * 8);
        for i in 0..size {
          if (__load_i64(p + ((i * 8) as usize)) == 4702111234474983745) { found += 8; }
        }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983745u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "shared") {
    let o = shared(word);
    print((o.tag == word && o.res.words[5] == word).to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

func secretRun(t *testing.T, target, src string) func(mode string) []string {
	t.Helper()
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "main")
	args := []string{"-target", target, "-o", bin, path, cli.stdlib}
	cmd := runX86_64Bin(cli.runner, cli.bin, args...)
	cmd.Env = childEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
	}
	return func(mode string) []string {
		t.Helper()
		var c *exec.Cmd
		if target == "arm64-linux" {
			c = runArm64Bin(qemu, bin, mode)
		} else {
			c = runX86_64Bin(cli.runner, bin, mode)
		}
		var stdout, stderr bytes.Buffer
		c.Stdout, c.Stderr = &stdout, &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%s: %v\nstdout: %s\nstderr: %s", mode, err, stdout.String(), stderr.String())
		}
		return strings.Fields(stdout.String())
	}
}

// secretCount is the count of secret bytes that a mode read back, which is
// the last field of its output.
func secretCount(t *testing.T, mode string, fields []string) int {
	t.Helper()
	if len(fields) == 0 {
		t.Fatalf("%s printed nothing", mode)
	}
	n, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		t.Fatalf("%s printed %q", mode, fields)
	}
	return n
}

func TestSelfHostSecretRecordWipesItsArrays(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretSrc)
			if got := secretCount(t, "plain", run("plain")); got < 48 {
				t.Fatalf("plain read %d secret bytes back; the raw allocation no longer observes the released block", got)
			}
			if got := secretCount(t, "secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocation read %d secret bytes back, want 0", got)
			}
			if got := run("shared"); len(got) != 3 || got[0] != "65" || got[1] != "65" {
				t.Fatalf("shared printed %q; the array that the caller still holds must keep its bytes", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsScalars(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretScalarsSrc)
			if got := secretCount(t, "plain", run("plain")); got < 36 {
				t.Fatalf("plain read %d secret bytes back, want 36; the raw allocation no longer observes the released record", got)
			}
			if got := secretCount(t, "secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocation read %d bytes of its scalar fields back, want 0", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsNestedBoxes(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretNestedSrc)
			if got := secretCount(t, "plain", run("plain")); got != 136 {
				t.Fatalf("plain read %d secret bytes back, want 136; the raw allocations no longer observe the released boxes", got)
			}
			if got := secretCount(t, "secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its nested boxes back, want 0", got)
			}
			if got := run("shared"); len(got) != 2 || got[0] != "true" {
				t.Fatalf("shared printed %q; the nested record that the caller still holds must keep its fields", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsEnums(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretEnumSrc)
			if got := secretCount(t, "plain", run("plain")); got != 148 {
				t.Fatalf("plain read %d secret bytes back, want 148; the raw allocations no longer observe the released boxes", got)
			}
			if got := secretCount(t, "secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its enum values back, want 0", got)
			}
			if got := run("shared"); len(got) != 2 || got[0] != "true" {
				t.Fatalf("shared printed %q; the enum value that the caller still holds must keep its fields", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsStrings(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretStringSrc)
			if got := secretCount(t, "plain", run("plain")); got < 48 {
				t.Fatalf("plain read %d secret bytes back, want at least 48; the raw allocations no longer observe the released string", got)
			}
			if got := secretCount(t, "secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its string back, want 0", got)
			}
			if got := run("shared"); len(got) != 2 || got[0] != "true" {
				t.Fatalf("shared printed %q; the string that the caller still holds must keep its bytes", got)
			}
			if got := run("literal"); len(got) != 2 || got[0] != "20" {
				t.Fatalf("literal printed %q; a string literal in a @secret record must keep its bytes", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsBuiltStrings(t *testing.T) {
	keyfile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyfile, bytes.Repeat([]byte("A"), 56), 0o644); err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(secretBuiltStringSrc, "KEYFILE", keyfile)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, src)
			for _, c := range []struct {
				how  string
				want int
			}{{"file", 56}, {"repeat", 56}, {"concat", 56}, {"grow", 40}} {
				if got := secretCount(t, c.how, run(c.how)); got < c.want {
					t.Fatalf("plain %s read %d secret bytes back, want at least %d; the raw allocations no longer observe the released string", c.how, got, c.want)
				}
				if got := secretCount(t, c.how, run("secret-"+c.how)); got != 0 {
					t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its %s string back, want 0", got, c.how)
				}
			}
			if got := run("mapped"); len(got) != 1 || got[0] != "true" {
				t.Fatalf("mapped printed %q; the drop must leave a mapped file, and the file must keep its bytes", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsArraysOfBoxes(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretElementsSrc)
			if got := secretCount(t, "plain", run("plain")); got < 152 {
				t.Fatalf("plain read %d secret bytes back, want at least 152; the raw allocations no longer observe the released elements", got)
			}
			if got := secretCount(t, "secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its array elements back, want 0", got)
			}
			if got := run("shared"); len(got) != 3 || got[0] != "65" || got[1] != "65" {
				t.Fatalf("shared printed %q; the row that the caller still holds must keep its bytes", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsUnions(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretUnionSrc)
			if got := secretCount(t, "plain", run("plain")); got < 76 {
				t.Fatalf("plain read %d secret bytes back, want at least 76; the raw allocations no longer observe the released union values", got)
			}
			if got := secretCount(t, "secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its union values back, want 0", got)
			}
			if got := secretCount(t, "held", run("held")); got != 0 {
				t.Fatalf("after the release of a union that holds a @secret record the raw allocations read %d bytes of the record back, want 0", got)
			}
			if got := run("shared"); len(got) != 2 || got[0] != "true" {
				t.Fatalf("shared printed %q; the union value that the caller still holds must keep its fields", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsFinalizedFields(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretFinalizedSrc)
			plain := run("plain")
			if got := secretCount(t, "plain", plain); got < 72 {
				t.Fatalf("plain read %d secret bytes back, want at least 72; the raw allocations no longer observe the released boxes", got)
			}
			secret := run("secret")
			if got := strings.Join(secret[:len(secret)-1], " "); got != "res true part true" {
				t.Fatalf("secret printed %q, want each finalizer to run once and see whole fields", secret)
			}
			if got := secretCount(t, "secret", secret); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its finalized fields back, want 0", got)
			}
			if got := run("shared"); strings.Join(got[:len(got)-1], " ") != "part true res true true" {
				t.Fatalf("shared printed %q; the record that the caller still holds must keep its fields, and its finalizer must run once", got)
			}
		})
	}
}

func TestSelfHostSecretRecordWipesItsDeepFinalizedValues(t *testing.T) {
	want := "res true res true res true res true res true res true"
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := secretRun(t, target, secretDeepFinalizedSrc)
			plain := run("plain")
			if got := strings.Join(plain[:len(plain)-1], " "); got != want {
				t.Fatalf("plain printed %q, want %q", plain, want)
			}
			if got := secretCount(t, "plain", plain); got < 112 {
				t.Fatalf("plain read %d secret bytes back, want at least 112; the raw allocations no longer observe the released boxes", got)
			}
			secret := run("secret")
			if got := strings.Join(secret[:len(secret)-1], " "); got != want {
				t.Fatalf("secret printed %q, want each finalizer to run once and see whole fields", secret)
			}
			if got := secretCount(t, "secret", secret); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocations read %d bytes of its nested finalized values back, want 0", got)
			}
			if got := run("shared"); strings.Join(got[:len(got)-1], " ") != "res true res true true" {
				t.Fatalf("shared printed %q; the record that the caller still holds must keep its fields, and each finalizer must run once", got)
			}
		})
	}
}

func TestSelfHostUnionDropRunsMemberFinalizer(t *testing.T) {
	want := []string{"res", "r1", "bare", "2", "res", "r7", "bare", "8", "end", "9"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			got := secretRun(t, target, unionMemberDropSrc)("run")
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("printed %q, want %q", got, want)
			}
		})
	}
}

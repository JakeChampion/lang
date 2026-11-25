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

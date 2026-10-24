package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var interpStdlibModloadCases = []struct {
	name string
	src  string
}{
	{"generic-record-and-union-receivers", `struct Box[T] { value: T }
function (b: Box[T]) get(): T { return b.value; }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { value: f(b.value) }; }
struct Foo { value: i32 }
function (b: Box[Foo]) deep(): i32 { return b.value.value; }
enum Opt[T] { Missing, Present(T) }
function (o: Opt[T]) get_or(other: T): T {
  match (o) { Present(x) => { return x; }, Missing => { return other; } }
}
function main(): i32 {
  let a: Box[i32] = Box { value: 7 };
  let b: Box[string] = Box { value: "text" };
  if (a.get() != 7 || b.get() != "text") { return 1; }
  if (!a.map((x: i32): boolean => x == 7).get()) { return 2; }
  let c: Box[Foo] = Box { value: Foo { value: 9 } };
  if (c.deep() != 9) { return 3; }
  let some: Opt[i32] = Present(12);
  let none: Opt[string] = Missing;
  if (some.get_or(0) != 12 || none.get_or("empty") != "empty") { return 4; }
  return 7;
}`},
	{"generic-ndarray-receiver-identity", `import "std/ndarray";
struct NdArray[T] { value: T }
function (a: NdArray[T]) to_flat(): T[] { return [a.value]; }
function main(): i32 {
  let local: NdArray[i32] = NdArray { value: 99 };
  let a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4], [2, 2]);
  if (local.to_flat()[0] != 99 || a.to_flat()[3] != 4) { return 1; }
  let t: ndarray.NdArray[i32] = a.transpose();
  let flat: i32[] = t.to_flat();
  if (flat[0] != 1 || flat[1] != 3 || flat[2] != 2 || flat[3] != 4) { return 2; }
  return 7;
}`},
	{"char-unicode-methods", `import "std/unicode";
import "std/string";
struct Scalar { value: char }
function identity[T](x: T): T { return x; }
function upper(c: char): char { return c.to_upper(); }
function main(): i32 {
  if ((97 as char).to_upper() != 'A') { return 1; }
  if ('Σ'.to_lower() != 'σ') { return 2; }
  let inferred = 'é';
  if (inferred.to_upper() != 'É') { return 3; }
  let scalars: char[] = ['a', '\u{10428}'];
  if (scalars[1].to_upper() != '\u{10400}') { return 4; }
  let pair = ('a', 'b');
  if (pair.1.to_upper() != 'B') { return 5; }
  let record = Scalar { value: 'é' };
  if (record.value.to_upper() != 'É') { return 6; }
  if (upper(identity('a')).to_lower() != 'a') { return 8; }
  let make = (): char => { return 'é'; };
  if (make().to_upper() != 'É') { return 9; }
  if (!'\u{0669}'.is_digit() || !'\u{4E2D}'.is_letter()) { return 10; }
  if ('ß'.to_upper() != 'ß' || "ß".to_upper() != "SS") { return 11; }
  let wide: i64 = 0x10428;
  if ((wide as char).to_upper() != '\u{10400}') { return 12; }
  let unsigned: u32 = 233;
  if ((unsigned as char).to_upper() != 'É') { return 13; }
  let wide_unsigned: u64 = 97;
  if ((wide_unsigned as char).to_upper() != 'A') { return 14; }
  return 7;
}`},
	{"char-distinct-method-dispatch", `function (c: char) marker(): i32 { return 1; }
function (n: i32) marker(): i32 { return 2; }
function (b: u8) byte_marker(): i32 { return 3; }
function main(): i32 {
  let c = 'a';
  if (c.marker() != 1 || (97 as char).marker() != 1) { return 1; }
  if ((c as i32).marker() != 2 || (97).marker() != 2) { return 2; }
  if ((c as u8).byte_marker() != 3) { return 3; }
  return 7;
}`},
	{"char-captures", `import "std/unicode";
function main(): i32 {
  let c = 'a';
  let read = (): char => { return c; };
  c = 'z';
  if (read().to_upper() != 'Z') { return 1; }
  let change = (): char => { c = 'é'; return c; };
  if (change().to_upper() != 'É' || read().to_upper() != 'É') { return 2; }
  return 7;
}`},
	{"ascii-case-fold", "import \"std/string\";\nfunction main(): i32 {\n  let s: string = \"AbC\";\n  if (s.to_ascii_lower() == \"abc\" && s.to_ascii_upper() == \"ABC\") { return 7; }\n  return 1;\n}\n"},
	{"int-to-string", "import \"std/i32\";\nfunction main(): i32 {\n  let n: i32 = -1234;\n  if (n.to_string() == \"-1234\") { return 7; }\n  return 1;\n}\n"},
	{"string-bytes", "import \"std/string\";\nfunction main(): i32 {\n  let bs: u8[] = \"hey\".bytes();\n  if (bs.len() == 3 && (bs[0] as i32) == 104) { return 7; }\n  return 1;\n}\n"},
	{"split-find", "import \"std/string\";\nfunction main(): i32 {\n  let parts: string[] = \"a,b,c\".split(\",\");\n  if (parts.len() == 3 && parts[1] == \"b\") { return 7; }\n  return 1;\n}\n"},
	{"trim-replace-repeat", "import \"std/string\";\nfunction main(): i32 {\n  let t: string = \"  hello world  \".trim().to_owned();\n  if (t != \"hello world\") { return 1; }\n  if (t.replace(\"world\", \"there\") != \"hello there\") { return 2; }\n  if (\"ab\".repeat(3) != \"ababab\") { return 3; }\n  return 7;\n}\n"},
	{"hex-encode", "import \"std/hex\";\nimport \"std/string\";\nfunction main(): i32 {\n  if (hex.hex_encode(\"AB\".bytes()) == \"4142\") { return 7; }\n  return 1;\n}\n"},
	{"base64-encode", "import \"std/base64\";\nimport \"std/string\";\nfunction main(): i32 {\n  if (base64.base64_encode(\"hi\".bytes()) == \"aGk=\") { return 7; }\n  return 1;\n}\n"},
	{"map-insert-get", "import \"core/map\";\nfunction main(): i32 {\n  let m: Map[string, i32] = map_new(8);\n  m = m.insert(\"a\", 1);\n  m = m.insert(\"b\", 2);\n  m = m.insert(\"a\", 3);\n  let total: i32 = m.get_or(\"a\", 0) + m.get_or(\"b\", 0);\n  if (m.len() == 2 && total == 5) { return 7; }\n  return 1;\n}\n"},
	{"map-keys", "import \"core/map\";\nfunction main(): i32 {\n  let m: Map[i32, i32] = map_new(4);\n  let i: i32 = 0;\n  while (i < 5) { m = m.insert(i, i * i); i = i + 1; }\n  let sum: i32 = 0;\n  let ks: i32[] = m.keys();\n  let j: i32 = 0;\n  while (j < ks.len()) { sum = sum + m.get_or(ks[j], 0); j = j + 1; }\n  if (sum == 30 && m.len() == 5) { return 7; }\n  return 1;\n}\n"},
	{"imported-enum", "import \"std/json\";\nfunction main(): i32 {\n  let v: json.JsonValue = JNumber(\"42\");\n  match (v) {\n    JNumber(s) => { if (s == \"42\") { return 7; } return 1; },\n    JNull => { return 2; },\n    _ => { return 3; }\n  }\n}\n"},
	{"alloc-fill-pack", "function main(): i32 {\n  let buf: u8[] = __alloc_u8(3);\n  buf = buf.with(0, 102 as u8);\n  buf = buf.with(1, 111 as u8);\n  buf = buf.with(2, 111 as u8);\n  if (string_from_bytes_unchecked(buf) == \"foo\") { return 7; }\n  return 1;\n}\n"},
	{"memchr-ascii-run", "function main(): i32 {\n  if (__memchr(\"abcb\", 98, 2) != 3) { return 1; }\n  if (__memchr(\"abc\", 122, 0) != 0 - 1) { return 2; }\n  if (__ascii_run(\"abé\", 0) != 2) { return 3; }\n  return 7;\n}\n"},
	{"scan-set", "function main(): i32 {\n  let set: u8[] = __alloc_u8(256);\n  set = set.with(32, 1 as u8);\n  if (__scan_set(\"ab cd\", 0, set) != 2) { return 1; }\n  if (__scan_set(\"ab cd\", 3, set) != 5) { return 2; }\n  let short: u8[] = [0 as u8, 1 as u8];\n  if (__scan_set(\"ab\\x01\", 0, short) != 2) { return 3; }\n  return 7;\n}\n"},
	{"count-runs", "function main(): i32 {\n  let set: u8[] = __alloc_u8(256);\n  set = set.with(32, 1 as u8);\n  if (__count_runs(\"a b  c\", 0, set) != 2) { return 1; }\n  if (__count_runs(\" a\", 1, set) != 0) { return 2; }\n  return 7;\n}\n"},
	{"bsd-sum", "function main(): i32 {\n  if (__bsd_sum(\"ab\", 0) != 32914) { return 1; }\n  if (__bsd_sum(\"\", 70000) != 4464) { return 2; }\n  return 7;\n}\n"},
	{"str-hash", "function main(): i32 {\n  if (__str_hash(\"abcdefgh\", 0) != 939000468) { return 1; }\n  if (__str_hash(\"abcdefghi\", 0) != 0 - 2056207145) { return 2; }\n  if (__str_hash(\"a\", 0) != 694301555) { return 3; }\n  if (__str_hash(\"hello, world\", 0 - 1) != 0 - 194608413) { return 4; }\n  return 7;\n}\n"},
	{"buf-push-mapped", "function main(): i32 {\n  let b: usize = buf_new(4);\n  let t: u8[] = [65 as u8, 66 as u8];\n  buf_push_mapped(b, \"\\x00\\x01z\", t);\n  if (buf_take(b) != \"ABz\") { return 1; }\n  buf_free(b);\n  return 7;\n}\n"},
	{"buf-push-filtered", "function main(): i32 {\n  let b: usize = buf_new(4);\n  let d: u8[] = [0 as u8, 1 as u8];\n  buf_push_filtered(b, \"\\x00\\x01z\\x01\", d);\n  if (buf_take(b) != \"\\x00z\") { return 1; }\n  buf_free(b);\n  return 7;\n}\n"},
	{"buf-push-expanded", "function main(): i32 {\n  let b: usize = buf_new(4);\n  let t: u8[] = [2 as u8, 94 as u8, 64 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8];\n  buf_push_expanded(b, \"\\x00z\", t);\n  if (buf_take(b) != \"^@z\") { return 1; }\n  buf_free(b);\n  return 7;\n}\n"},
	{"crc32-cksum", "function main(): i32 {\n  if (__crc32_cksum(0, \"\") != 0) { return 1; }\n  if (__crc32_cksum(12345, \"\") != 12345) { return 2; }\n  if (__crc32_cksum(0, \"hello world\") != 1937437358) { return 3; }\n  if (__crc32_cksum(0 - 1, \"x\") != 0 - 2001448981) { return 4; }\n  if (__crc32_cksum(__crc32_cksum(0, \"hello \"), \"world\") != 1937437358) { return 5; }\n  return 7;\n}\n"},
	{"mismatch", "function main(): i32 {\n  if (__mismatch(\"hello\", 0, \"hello\", 0, 5) != 5) { return 1; }\n  if (__mismatch(\"abc\", 0, \"abd\", 0, 3) != 2) { return 2; }\n  if (__mismatch(\"zzabc\", 2, \"qqqabd\", 3, 3) != 2) { return 3; }\n  if (__mismatch(\"ab\", 0, \"abcdef\", 0, 6) != 2) { return 4; }\n  if (__mismatch(\"abc\", 0 - 4, \"abc\", 0, 3) != 3) { return 5; }\n  if (__mismatch(\"abc\", 99, \"abc\", 0, 3) != 0) { return 6; }\n  return 7;\n}\n"},
	{"buf-builder", "struct Acc { h: usize }\nfunction push_twice(a: Acc, s: string): Acc { buf_push(a.h, s); buf_push(a.h, s); return a; }\nfunction main(): i32 {\n  let b: usize = buf_new(4);\n  if (buf_len(b) != 0) { return 1; }\n  buf_push(b, \"ab\");\n  buf_push_byte(b, 99);\n  buf_push_range(b, \"xdefy\", 1, 4);\n  if (buf_len(b) != 6) { return 2; }\n  if (buf_take(b) != \"abcdef\") { return 3; }\n  if (buf_len(b) != 0) { return 4; }\n  let acc: Acc = Acc { h: b };\n  let copied: Acc = push_twice(acc, \"z\");\n  if (buf_len(acc.h) != 2) { return 5; }\n  if (buf_take(copied.h) != \"zz\") { return 6; }\n  buf_push_byte(b, 321);\n  if (buf_take(b) != \"A\") { return 8; }\n  buf_free(b);\n  return 7;\n}\n"},
	{"float-bits-roundtrip", "function main(): i32 {\n  if (f64_from_bits(f64_bits(1.5)) != 1.5) { return 1; }\n  if (f32_from_bits(f32_bits(0.5 as f32)) != (0.5 as f32)) { return 2; }\n  return 7;\n}\n"},
	{"tuple-destructure", "function pair(): (i32, i32) { return (3, 4); }\nfunction main(): i32 {\n  let (a, b) = pair();\n  return a + b;\n}\n"},
	{"json-parse-array", "import \"std/json\";\nfunction main(): i32 {\n  match (json.json_parse(\"[1,2,3]\")) {\n    Some(v) => {\n      match (v) {\n        JArray(items) => {\n          match (items[1]) {\n            JNumber(s) => { if (s == \"2\") { return 7; } return 1; },\n            _ => { return 2; }\n          }\n        },\n        _ => { return 3; }\n      }\n    },\n    None => { return 4; }\n  }\n}\n"},
	{"json-parse-object", "import \"std/json\";\nfunction main(): i32 {\n  match (json.json_parse(\"{\\\"a\\\":[1,2,3]}\")) {\n    Some(v) => {\n      match (v) {\n        JObject(m) => {\n          match (m.get(\"a\")) {\n            Some(arr) => {\n              match (arr) {\n                JArray(items) => {\n                  match (items[1]) {\n                    JNumber(s) => { if (s == \"2\") { return 7; } return 1; },\n                    _ => { return 2; }\n                  }\n                },\n                _ => { return 3; }\n              }\n            },\n            None => { return 4; }\n          }\n        },\n        _ => { return 5; }\n      }\n    },\n    None => { return 6; }\n  }\n}\n"},
	{"json-encode-roundtrip", "import \"std/json\";\nfunction main(): i32 {\n  let doc: json.JsonValue = JArray([JBool(true), JString(\"hi\"), JNull]);\n  if (json.json_encode(doc) != \"[true,\\\"hi\\\",null]\") { return 1; }\n  match (json.json_parse(json.json_encode(doc))) {\n    Some(JArray(items)) => { if (items.len() == 3) { return 7; } return 2; },\n    _ => { return 3; }\n  }\n}\n"},
	{"user-enum-with-stdlib", "import \"std/string\";\nenum Tok { Word(string), Num(i32) }\nfunction render(t: Tok): string {\n  match (t) { Word(w) => { return w.to_ascii_upper(); }, Num(_) => { return \"#\"; } }\n}\nfunction main(): i32 {\n  if (render(Word(\"ok\")) == \"OK\" && render(Num(3)) == \"#\") { return 7; }\n  return 1;\n}\n"},
	{"u64-hex-literals", "import \"std/u64\";\nconst T: u64[] = [0x8000000000000001u64, 0x7fffffffffffffffu64];\nfunction main(): i32 {\n  let a: u64 = 0x8000000000000001u64;\n  if (a.to_string() != \"9223372036854775809\") { return 1; }\n  if (T[1].to_string() != \"9223372036854775807\") { return 2; }\n  if (__mulhi_u64(a, T[1]).to_string() != \"4611686018427387903\") { return 3; }\n  return 7;\n}\n"},
	{"float-to-string-shortest", "import \"std/f64\";\nfunction main(): i32 {\n  if ((1.5).to_string() != \"1.5\") { return 1; }\n  if ((0.1).to_string() != \"0.1\") { return 2; }\n  if ((1234.5678).to_string() != \"1234.5678\") { return 3; }\n  if ((0.0 - 2.5).to_string() != \"-2.5\") { return 4; }\n  if ((1e21).to_string() != \"1e+21\") { return 5; }\n  if ((100.0).to_string() != \"100\") { return 6; }\n  if ((0.5).to_string() != \"0.5\") { return 8; }\n  return 7;\n}\n"},
	{"float-to-string-wide-range", "import \"std/f64\";\nfunction main(): i32 {\n  if ((1e30).to_string() != \"1e+30\") { return 1; }\n  if ((1e100).to_string() != \"1e+100\") { return 2; }\n  if ((1e-100).to_string() != \"1e-100\") { return 3; }\n  if ((2.5e-10).to_string() != \"2.5e-10\") { return 4; }\n  if ((1e-7).to_string() != \"1e-7\") { return 5; }\n  if ((8.98846567431158e307).to_string() != \"8.98846567431158e+307\") { return 6; }\n  if ((1.7976931348623157e308).to_string() != \"1.7976931348623157e+308\") { return 8; }\n  if ((5e-324).to_string() != \"5e-324\") { return 9; }\n  if ((1.2345678901234567e-5).to_string() != \"1.2345678901234568e-5\") { return 10; }\n  return 7;\n}\n"},
	{"u64-to-string", "import \"std/u64\";\nfunction main(): i32 {\n  let big: u64 = 9223372036854775807;\n  big = big + (1 as u64);\n  if (big.to_string() != \"9223372036854775808\") { return 1; }\n  let maxv: u64 = (0 as u64) - (1 as u64);\n  if (maxv.to_string() != \"18446744073709551615\") { return 2; }\n  return 7;\n}\n"},
	{"u64-receiver-methods", "import \"std/u64\";\nfunction main(): i32 {\n  let big: u64 = (0 as u64) - (2 as u64);\n  if (big.min(1 as u64) != (1 as u64)) { return 1; }\n  if (big.max(1 as u64) != big) { return 2; }\n  if (!big.is_even()) { return 3; }\n  if (big.saturating_add(9 as u64) != (0 as u64) - (1 as u64)) { return 4; }\n  if ((2 as u64).pow(63) != 9223372036854775808 as u64) { return 5; }\n  return 7;\n}\n"},
	{"array-reverse", "import \"std/array\";\nfunction main(): i32 {\n  let xs: i32[] = [3, 1, 2];\n  let ys: i32[] = xs.reverse();\n  if (ys[0] == 2) { return 7; }\n  return 1;\n}\n"},
	{"array-closure-methods", "import \"std/array\";\nfunction main(): i32 {\n  let xs: i32[] = [3, 1, 2];\n  let ys: i32[] = xs.map((v: i32): i32 => { return v * 2; });\n  if (ys.len() != 3 || ys[0] != 6) { return 1; }\n  let big: i32[] = ys.filter((v: i32): boolean => { return v > 3; });\n  if (big.len() != 2) { return 2; }\n  let total: i32 = xs.fold(0, (acc: i32, v: i32): i32 => { return acc + v; });\n  if (total != 6) { return 3; }\n  return 7;\n}\n"},
	{"array-predicate-methods", "import \"std/array\";\nfunction main(): i32 {\n  let xs: i32[] = [3, 1, 2];\n  if (xs.is_empty()) { return 1; }\n  if (!xs.any((v: i32): boolean => { return v == 2; })) { return 2; }\n  if (xs.all((v: i32): boolean => { return v > 2; })) { return 3; }\n  if (xs.count_where((v: i32): boolean => { return v > 1; }) != 2) { return 4; }\n  match (xs.first()) { Some(v) => { if (v != 3) { return 5; } }, None => { return 6; } }\n  match (xs.find((v: i32): boolean => { return v == 1; })) { Some(v) => { if (v != 1) { return 8; } }, None => { return 9; } }\n  return 7;\n}\n"},
	{"array-concrete-helpers", "import \"std/array\";\nfunction main(): i32 {\n  let xs: i32[] = [3, 1, 2];\n  if (xs.sum() != 6) { return 1; }\n  if (xs.product() != 6) { return 2; }\n  match (xs.max()) { Some(v) => { if (v != 3) { return 3; } }, None => { return 4; } }\n  if (xs.reverse()[0] != 2) { return 5; }\n  if (xs.cumsum()[2] != 6) { return 6; }\n  let ws: string[] = [\"a\", \"b\", \"a\"];\n  if (ws.join(\"-\") != \"a-b-a\") { return 8; }\n  if (!ws.contains(\"b\")) { return 9; }\n  if (ws.distinct().len() != 2) { return 10; }\n  return 7;\n}\n"},
	{"array-index-of-generic", "import \"std/array\";\nfunction main(): i32 {\n  let xs: i32[] = [3, 1, 2];\n  match (xs.index_of(1)) { Some(i) => { if (i != 1) { return 1; } }, None => { return 1; } }\n  match (xs.index_of(9)) { Some(_) => { return 2; }, None => {} }\n  if (!xs.contains(2)) { return 3; }\n  if (xs.contains(9)) { return 4; }\n  let bs: u8[] = [7 as u8, 8 as u8];\n  match (bs.index_of(8 as u8)) { Some(i) => { if (i != 1) { return 5; } }, None => { return 5; } }\n  if (!bs.contains(7 as u8)) { return 6; }\n  let ws: string[] = [\"a\", \"bb\"];\n  match (ws.index_of(\"bb\")) { Some(i) => { if (i != 1) { return 8; } }, None => { return 8; } }\n  match (ws.index_of(\"zz\")) { Some(_) => { return 9; }, None => {} }\n  return 7;\n}\n"},
	{"num-identity-generics", "import \"std/num\";\nfunction main(): i32 {\n  let xs: i32[] = [1, 2, 3];\n  if (num.sum(xs) != 6) { return 1; }\n  if (num.product(xs) != 6) { return 2; }\n  if (num.cumsum(xs)[2] != 6) { return 3; }\n  let ws: i64[] = [1, 2, 3];\n  if (num.sum(ws) != 6) { return 4; }\n  let fs: f64[] = [1.5, 2.5];\n  if (num.sum(fs) != 4.0) { return 5; }\n  let none: i32[] = [];\n  if (num.sum(none) != 0) { return 6; }\n  if (num.product(none) != 1) { return 8; }\n  return 7;\n}\n"},
	{"assoc-fn-user-impl", "import \"std/num\";\nstruct P { x: i32, y: i32 }\nimpl num.Add for P { function add(self: Self, o: Self): Self { return P { x: self.x + o.x, y: self.y + o.y }; } }\nimpl num.Zero for P { function zero(): Self { return P { x: 0, y: 0 }; } }\nfunction main(): i32 {\n  if (i32.zero() != 0) { return 1; }\n  if (P.zero().x != 0) { return 2; }\n  let ps: P[] = [P { x: 1, y: 2 }, P { x: 3, y: 4 }];\n  let t: P = num.sum(ps);\n  if (t.x != 4 || t.y != 6) { return 3; }\n  let none: P[] = [];\n  if (num.sum(none).y != 0) { return 4; }\n  return 7;\n}\n"},
	{"print-display-arg", "import \"std/i32\";\nstruct P { x: i32 }\nfunction (p: P) to_string(): string { return \"P(\" + p.x.to_string() + \")\"; }\nfunction main(): i32 {\n  let n: i32 = 42;\n  print(n);\n  write(P { x: 3 });\n  print(\"\");\n  eprint(n + 1);\n  print(\"done\");\n  return 7;\n}\n"},
}

func TestSelfHostInterpStdlibModload(t *testing.T) {
	fernBin := e2eharness.SelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range interpStdlibModloadCases {
		t.Run(tc.name, func(t *testing.T) {
			mainPath := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			wantOut, wantErr, want := runInterpCLI(t, exec.Command(interpBin, "-interp", mainPath), "")
			if want != 7 {
				t.Fatalf("native interp oracle exited %d, want 7 — the case itself is wrong, not the self-host engine\n%s", want, wantErr)
			}
			out, errOut, code := runInterpCLI(t, exec.Command(fernBin, "-interp", mainPath, stdlibRoot), "")
			if code != want {
				t.Errorf("self-host -interp exited %d, want %d (native interp oracle)\n%s", code, want, errOut)
			}
			if out != wantOut {
				t.Errorf("self-host -interp stdout = %q, native interp oracle = %q", out, wantOut)
			}
			if errOut != wantErr {
				t.Errorf("self-host -interp stderr = %q, native interp oracle = %q", errOut, wantErr)
			}
		})
	}
}

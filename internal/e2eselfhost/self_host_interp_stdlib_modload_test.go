package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// interpStdlibModloadCases are stdlib-importing programs run through the
// self-hosted CLI's `-interp`, the only self-host engine that resolves
// imports. Every one of them reaches the raw-memory intrinsics the stdlib's
// string builders are made of — `__alloc_u8` to stage a byte buffer, `.with`
// to fill it, `string_from_bytes_unchecked` to pack it back into a string —
// so before #5998 they all stopped at `undefined function: __alloc_u8`.
//
// Each case is oracle-checked against the native interpreter, which resolves
// the same imports, rather than against a hardcoded exit code.
var interpStdlibModloadCases = []struct {
	name string
	src  string
}{
	// The issue's repro: `__string_case_fold` — alloc a buffer, fill it byte
	// by byte, pack it back.
	{"ascii-case-fold", "import \"std/string\";\nfunction main(): i32 {\n  var s: string = \"AbC\";\n  if (s.to_ascii_lower() == \"abc\" && s.to_ascii_upper() == \"ABC\") { return 7; }\n  return 1;\n}\n"},
	// The same shape reached through core/int's digit buffer.
	{"int-to-string", "import \"std/i32\";\nfunction main(): i32 {\n  var n: i32 = -1234;\n  if (n.to_string() == \"-1234\") { return 7; }\n  return 1;\n}\n"},
	// `s.bytes()`, whose compiled body is a `__memcpy` out of the string
	// payload and is answered directly against the boxed value instead.
	{"string-bytes", "import \"std/string\";\nfunction main(): i32 {\n  var bs: u8[] = \"hey\".bytes();\n  if (bs.len() == 3 && (bs[0] as i32) == 104) { return 7; }\n  return 1;\n}\n"},
	// std/string's search family bottoms out in the __memchr scan kernel.
	{"split-find", "import \"std/string\";\nfunction main(): i32 {\n  var parts: string[] = \"a,b,c\".split(\",\");\n  if (parts.len() == 3 && parts[1] == \"b\") { return 7; }\n  return 1;\n}\n"},
	{"trim-replace-repeat", "import \"std/string\";\nfunction main(): i32 {\n  var t: string = \"  hello world  \".trim().to_owned();\n  if (t != \"hello world\") { return 1; }\n  if (t.replace(\"world\", \"there\") != \"hello there\") { return 2; }\n  if (\"ab\".repeat(3) != \"ababab\") { return 3; }\n  return 7;\n}\n"},
	{"hex-encode", "import \"std/hex\";\nimport \"std/string\";\nfunction main(): i32 {\n  if (hex.hex_encode(\"AB\".bytes()) == \"4142\") { return 7; }\n  return 1;\n}\n"},
	{"base64-encode", "import \"std/base64\";\nimport \"std/string\";\nfunction main(): i32 {\n  if (base64.base64_encode(\"hi\".bytes()) == \"aGk=\") { return 7; }\n  return 1;\n}\n"},
	// core/map is the stdlib's heaviest raw-memory consumer — its kv buffer
	// is hand-laid-out through __load_i32 / __store_ptr / __ptr_width. Both
	// engines answer Map from a native map value instead of running those
	// bodies, and this pins that the modload path keeps doing so.
	{"map-insert-get", "import \"core/map\";\nfunction main(): i32 {\n  var m: Map[string, i32] = map_new(8);\n  m = m.insert(\"a\", 1);\n  m = m.insert(\"b\", 2);\n  m = m.insert(\"a\", 3);\n  var total: i32 = m.get_or(\"a\", 0) + m.get_or(\"b\", 0);\n  if (m.len() == 2 && total == 5) { return 7; }\n  return 1;\n}\n"},
	{"map-keys", "import \"core/map\";\nfunction main(): i32 {\n  var m: Map[i32, i32] = map_new(4);\n  var i: i32 = 0;\n  while (i < 5) { m = m.insert(i, i * i); i = i + 1; }\n  var sum: i32 = 0;\n  var ks: i32[] = m.keys();\n  var j: i32 = 0;\n  while (j < ks.len()) { sum = sum + m.get_or(ks[j], 0); j = j + 1; }\n  if (sum == 30 && m.len() == 5) { return 7; }\n  return 1;\n}\n"},
	// The intrinsics called directly, without a stdlib body in between.
	{"alloc-fill-pack", "function main(): i32 {\n  var buf: u8[] = __alloc_u8(3);\n  buf = buf.with(0, 102 as u8);\n  buf = buf.with(1, 111 as u8);\n  buf = buf.with(2, 111 as u8);\n  if (string_from_bytes_unchecked(buf) == \"foo\") { return 7; }\n  return 1;\n}\n"},
	{"memchr-ascii-run", "function main(): i32 {\n  if (__memchr(\"abcb\", 98, 2) != 3) { return 1; }\n  if (__memchr(\"abc\", 122, 0) != 0 - 1) { return 2; }\n  if (__ascii_run(\"abé\", 0) != 2) { return 3; }\n  return 7;\n}\n"},
	{"float-bits-roundtrip", "function main(): i32 {\n  if (f64_from_bits(f64_bits(1.5)) != 1.5) { return 1; }\n  if (f32_from_bits(f32_bits(0.5 as f32)) != (0.5 as f32)) { return 2; }\n  return 7;\n}\n"},
	// `var (a, b) = tuple` is one StmtVar carrying a comma-joined name; the
	// interpreter used to bind that name whole, so std/json's parser (which
	// destructures on every step) died on an undefined identifier.
	{"tuple-destructure", "function pair(): (i32, i32) { return (3, 4); }\nfunction main(): i32 {\n  var (a, b) = pair();\n  return a + b;\n}\n"},
	// std/json is the stdlib module #6808 was reported against: `JsonValue` is
	// injected by the front end, so its variants have no declaration anywhere
	// in the parsed AST and the parser ran to completion only to stop at
	// `undefined function: JNumber` when it went to build a value.
	{"json-parse-array", "import \"std/json\";\nfunction main(): i32 {\n  match (json.json_parse(\"[1,2,3]\")) {\n    Some(v) => {\n      match (v) {\n        JArray(items) => {\n          match (items[1]) {\n            JNumber(s) => { if (s == \"2\") { return 7; } return 1; },\n            _ => { return 2; }\n          }\n        },\n        _ => { return 3; }\n      }\n    },\n    None => { return 4; }\n  }\n}\n"},
	// The object shape, which reaches JNumber through a `Map[string, JsonValue]`
	// payload and a `.get` returning `Option[JsonValue]`.
	{"json-parse-object", "import \"std/json\";\nfunction main(): i32 {\n  match (json.json_parse(\"{\\\"a\\\":[1,2,3]}\")) {\n    Some(v) => {\n      match (v) {\n        JObject(m) => {\n          match (m.get(\"a\")) {\n            Some(arr) => {\n              match (arr) {\n                JArray(items) => {\n                  match (items[1]) {\n                    JNumber(s) => { if (s == \"2\") { return 7; } return 1; },\n                    _ => { return 2; }\n                  }\n                },\n                _ => { return 3; }\n              }\n            },\n            None => { return 4; }\n          }\n        },\n        _ => { return 5; }\n      }\n    },\n    None => { return 6; }\n  }\n}\n"},
	// Round-tripping a hand-built document through the encoder: construction on
	// the way in, variant matching on the way out.
	{"json-encode-roundtrip", "import \"std/json\";\nfunction main(): i32 {\n  var doc: JsonValue = JArray([JBool(true), JString(\"hi\"), JNull]);\n  if (json.json_encode(doc) != \"[true,\\\"hi\\\",null]\") { return 1; }\n  match (json.json_parse(json.json_encode(doc))) {\n    Some(JArray(items)) => { if (items.len() == 3) { return 7; } return 2; },\n    _ => { return 3; }\n  }\n}\n"},
	// A user enum through the CLI's module loader, which merges every imported
	// module's decls before the interpreter sees them.
	{"user-enum-with-stdlib", "import \"std/string\";\nenum Tok { Word(string), Num(i32) }\nfunction render(t: Tok): string {\n  match (t) { Word(w) => { return w.to_ascii_upper(); }, Num(_) => { return \"#\"; } }\n}\nfunction main(): i32 {\n  if (render(Word(\"ok\")) == \"OK\" && render(Num(3)) == \"#\") { return 7; }\n  return 1;\n}\n"},
	// std/float's shortest-round-trip formatter is written almost entirely in
	// u64, so #6810's signed 64-bit carrier showed up here as a WRONG ANSWER:
	// the significand went negative inside __db_shortest64 and `1.5` rendered
	// starting with `-`. The values are ones both engines parse to the same
	// f64: the self-host interpreter's decimal literal reader accumulates
	// rounding error past ~1e22 and on negative exponents, which is a separate
	// gap from the formatter's arithmetic and would make a wider set compare
	// two different numbers rather than two renderings.
	{"float-to-string-shortest", "import \"std/float\";\nfunction main(): i32 {\n  if ((1.5).to_string() != \"1.5\") { return 1; }\n  if ((0.1).to_string() != \"0.1\") { return 2; }\n  if ((1234.5678).to_string() != \"1234.5678\") { return 3; }\n  if ((0.0 - 2.5).to_string() != \"-2.5\") { return 4; }\n  if ((1e21).to_string() != \"1e+21\") { return 5; }\n  if ((100.0).to_string() != \"100\") { return 6; }\n  if ((0.5).to_string() != \"0.5\") { return 8; }\n  return 7;\n}\n"},
	// core/int's u64 stringifier is the other u64-heavy stdlib body, and the
	// two values here straddle the sign boundary the carrier was losing.
	{"u64-to-string", "import \"std/u64\";\nfunction main(): i32 {\n  var big: u64 = 9223372036854775807;\n  big = big + (1 as u64);\n  if (big.to_string() != \"9223372036854775808\") { return 1; }\n  var maxv: u64 = (0 as u64) - (1 as u64);\n  if (maxv.to_string() != \"18446744073709551615\") { return 2; }\n  return 7;\n}\n"},
	// std/u64's methods, which only come within reach once a u64 value
	// dispatches on `u64` rather than `i64`.
	{"u64-receiver-methods", "import \"std/u64\";\nfunction main(): i32 {\n  var big: u64 = (0 as u64) - (2 as u64);\n  if (big.min(1 as u64) != (1 as u64)) { return 1; }\n  if (big.max(1 as u64) != big) { return 2; }\n  if (!big.is_even()) { return 3; }\n  if (big.saturating_add(9 as u64) != (0 as u64) - (1 as u64)) { return 4; }\n  if ((2 as u64).pow(63) != 9223372036854775808 as u64) { return 5; }\n  return 7;\n}\n"},
}

// TestSelfHostInterpStdlibModload drives `fern -interp <prog> <stdlib-root>`
// on the self-hosted CLI — the one self-host engine with a module loader, and
// so the only place a stdlib-importing program can be interpreted at all. The
// bare `interp_run.fern` driver (TestSelfHostInterpDriver*) reads raw stdin
// through parser.parse_module with no loader, so no `std/` or `core/` import
// resolves there for any engine.
//
// Host modes mirror TestSelfHostArm64DarwinBuilds: on Apple Silicon the CLI is
// built for arm64-darwin through the driver's own in-process Mach-O path; off
// it, with the Go x86-64 backend. Either way the CLI runs on the host, since
// it takes host filesystem paths as argv.
func TestSelfHostInterpStdlibModload(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")

	var fernBin string
	if native {
		fernBin = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	} else {
		gcc, runner := x86_64Tooling(t)
		if len(runner) != 0 {
			t.Skip("self-host CLI driver runs only natively (argv paths)")
		}
		fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	}

	interpBin := buildLangBinForInterp(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range interpStdlibModloadCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 7 {
				t.Fatalf("native interp oracle exited %d, want 7 — the case itself is wrong, not the self-host engine", want)
			}
			mainPath := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			cmd := exec.Command(fernBin, "-interp", mainPath, stdlibRoot)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("self-host -interp exited %d, want %d (native interp oracle)\n%s", code, want, out)
			}
		})
	}
}

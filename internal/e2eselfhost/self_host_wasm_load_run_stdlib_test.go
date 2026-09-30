package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// wasmLoadRunStdlibCases import the standard library, so they compile through
// asm_load_run's wasm32-wasi leg with the stdlib root rather than wasm_run,
// which loads no imports.
var wasmLoadRunStdlibCases = []struct {
	name   string
	src    string
	want   int
	stdout string // "" means don't check
}{
	{"ir-map-i32-len3", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }", 3, ""},
	{"ir-map-str-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"a\", 1); m = m.insert(\"bb\", 2); m = m.insert(\"a\", 9); return m.len(); }", 2, ""},
	{"ir-map-get-hit", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(7)) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 42, ""},
	{"ir-map-get-miss", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(999)) { Some(v) => { return v; }, None => { return 5; } } return 9; }", 5, ""},
	{"ir-map-has", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 1); var r = 0; if (m.has(1)) { r = r + 1; } if (m.has(2)) { r = r + 10; } return r; }", 1, ""},
	{"ir-map-get-or-hit", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(7, 0); }", 42, ""},
	{"ir-map-get-or-miss", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(999, 5); }", 5, ""},
	{"ir-map-get-or-strhit", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"hi\", 0); }", 11, ""},
	{"ir-map-get-or-strmiss", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"no\", 7); }", 7, ""},
	{"ir-map-keys-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var ks: i32[] = m.keys(); var s = 0; var i = 0; while (i < ks.len()) { s = s + ks[i]; i = i + 1; } return s; }", 6, ""},
	{"ir-map-values-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var vs: i32[] = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s; }", 60, ""},
	{"ir-map-forkv-values", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + v; } return s; }", 60, ""},
	{"ir-map-forkv-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + k; } return s; }", 6, ""},
	{"ir-map-forkv-pair", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); m = m.insert(2, 3); m = m.insert(3, 4); var s = 0; for (k, v) in m { s = s + k * v; } return s; }", 20, ""},
	{"ir-map-forkv-strkey", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"ab\", 1); m = m.insert(\"cde\", 2); var s = 0; for (k, v) in m { s = s + k.len() + v; } return s; }", 8, ""},
	{"map-get-or", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; print_int(m.get_or(1, 0)); return 0; }", 0, "10"},
	{"map-get-or-second", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; print_int(m.get_or(2, 0)); return 0; }", 0, "20"},
	{"map-get-or-missing", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10 }; print_int(m.get_or(2, 99)); return 0; }", 0, "99"},
	{"map-has", "import \"core/map\";\nfunction main(): i32 { var m = Map { 5: 1 }; if (m.has(5)) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
	{"map-has-missing", "import \"core/map\";\nfunction main(): i32 { var m = Map { 5: 1 }; if (m.has(6)) { print_int(1); } else { print_int(0); } return 0; }", 0, "0"},
	{"map-len", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3 }; print_int(m.len()); return 0; }", 0, "3"},
	{"map-update-value", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10 }; m = m.insert(1, 99); print_int(m.get_or(1, 0)); return 0; }", 0, "99"},
	{"map-update-keeps-len", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10 }; m = m.insert(1, 99); print_int(m.len()); return 0; }", 0, "1"},
	{"map-get-some", "import \"core/map\";\nfunction main(): i32 { var m = Map { 7: 42 }; match (m.get(7)) { Some(v) => { print_int(v); }, None => { print_int(0); } } return 0; }", 0, "42"},
	{"map-get-none", "import \"core/map\";\nfunction main(): i32 { var m = Map { 7: 42 }; match (m.get(8)) { Some(v) => { print_int(v); }, None => { print_int(0); print_int(1); } } return 0; }", 0, "01"},
	{"map-empty-then-set", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(3, 30); print_int(m.get_or(3, 0)); return 0; }", 0, "30"},
	{"map-zero-key", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(0, 123); print_int(m.get_or(0, -1)); return 0; }", 0, "123"},
	{"map-negative-key", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 5 }; m = m.insert(-7, 88); print_int(m.get_or(-7, 0)); return 0; }", 0, "88"},
	{"map-grow-get", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var i: i32 = 0; while (i < 50) { m = m.insert(i, i * 2); i = i + 1; } print_int(m.get_or(37, -1)); return 0; }", 0, "74"},
	{"map-grow-len", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var i: i32 = 0; while (i < 50) { m = m.insert(i, i * 2); i = i + 1; } print_int(m.len()); return 0; }", 0, "50"},
	{"map-overwrite-loop", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var i: i32 = 0; while (i < 10) { m = m.insert(1, i); i = i + 1; } print_int(m.get_or(1, -1)); print_int(m.len()); return 0; }", 0, "91"},
	{"strmap-get-or", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1, \"b\": 2 }; print_int(m.get_or(\"a\", 0)); return 0; }", 0, "1"},
	{"strmap-get-or-second", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1, \"b\": 2 }; print_int(m.get_or(\"b\", 0)); return 0; }", 0, "2"},
	{"strmap-missing", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1 }; print_int(m.get_or(\"z\", 99)); return 0; }", 0, "99"},
	{"strmap-has", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"hello\": 1 }; if (m.has(\"hello\")) { print_int(1); } else { print_int(0); } return 0; }", 0, "1"},
	{"strmap-has-missing", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"hello\": 1 }; if (m.has(\"world\")) { print_int(1); } else { print_int(0); } return 0; }", 0, "0"},
	{"strmap-len", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1, \"b\": 2, \"c\": 3 }; print_int(m.len()); return 0; }", 0, "3"},
	{"strmap-update", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1 }; m = m.insert(\"a\", 50); print_int(m.get_or(\"a\", 0)); print_int(m.len()); return 0; }", 0, "501"},
	{"strmap-get-some", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"k\": 42 }; match (m.get(\"k\")) { Some(v) => { print_int(v); }, None => { print_int(0); } } return 0; }", 0, "42"},
	{"strmap-get-none", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"k\": 42 }; match (m.get(\"x\")) { Some(v) => { print_int(v); }, None => { print_int(7); } } return 0; }", 0, "7"},
	{"strmap-content-equality", "import \"core/map\";\nfunction main(): i32 { var k = \"h\" + \"i\"; var m: Map[string, i32] = map_new(8); m = m.insert(k, 7); print_int(m.get_or(\"hi\", 0)); return 0; }", 0, "7"},
	{"strmap-prefix-distinct", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"ab\": 1, \"abc\": 2 }; print_int(m.get_or(\"ab\", 0)); print_int(m.get_or(\"abc\", 0)); return 0; }", 0, "12"},
	{"strmap-grow", "import \"core/map\";\nimport \"std/string\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); var i: i32 = 1; while (i <= 20) { m = m.insert(\"x\".repeat(i), i); i = i + 1; } print_int(m.get_or(\"x\".repeat(5), -1)); print_int(m.len()); return 0; }", 0, "520"},
	{"strval-get-or", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"one\", 2: \"two\" }; write(m.get_or(1, \"?\")); return 0; }", 0, "one"},
	{"strval-get-or-missing", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"one\" }; write(m.get_or(3, \"none\")); return 0; }", 0, "none"},
	{"strval-string-key", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"x\": \"hello\" }; write(m.get_or(\"x\", \"?\")); return 0; }", 0, "hello"},
	{"strval-get-some", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"one\", 2: \"two\" }; match (m.get(2)) { Some(v) => { write(v); }, None => { write(\"none\"); } } return 0; }", 0, "two"},
	{"strval-get-none", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"one\" }; match (m.get(9)) { Some(v) => { write(v); }, None => { write(\"none\"); } } return 0; }", 0, "none"},
	{"strval-concat", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"one\" }; write(m.get_or(1, \"?\") + \"!\"); return 0; }", 0, "one!"},
	{"strval-update", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"a\" }; m = m.insert(1, \"b\"); write(m.get_or(1, \"?\")); return 0; }", 0, "b"},
	{"strval-built-value", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, string] = map_new(8); m = m.insert(1, \"x\" + \"y\"); write(m.get_or(1, \"?\")); return 0; }", 0, "xy"},
	{"strval-len", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"a\", 2: \"b\" }; print_int(m.len()); return 0; }", 0, "2"},
	{"map-delete-has", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; var (mw, we) = m.without(1); m = mw; if (m.has(1)) { print_int(1); } else { print_int(0); } return 0; }", 0, "0"},
	{"map-delete-keeps-other", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; var (mw, we) = m.without(1); m = mw; print_int(m.get_or(2, -1)); return 0; }", 0, "20"},
	{"map-delete-len", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3 }; var (mw, we) = m.without(2); m = mw; print_int(m.len()); return 0; }", 0, "2"},
	{"map-delete-missing-noop", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 1 }; var (mw, we) = m.without(99); m = mw; print_int(m.len()); print_int(m.get_or(1, -1)); return 0; }", 0, "11"},
	{"map-delete-get-none", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10 }; var (mw, we) = m.without(1); m = mw; match (m.get(1)) { Some(v) => { print_int(v); }, None => { print_int(7); } } return 0; }", 0, "7"},
	{"map-delete-reinsert", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10 }; var (mw, we) = m.without(1); m = mw; m = m.insert(1, 99); print_int(m.get_or(1, -1)); print_int(m.len()); return 0; }", 0, "991"},
	{"map-delete-mid-chain", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var i: i32 = 0; while (i < 10) { m = m.insert(i, i); i = i + 1; } var (mw, we) = m.without(5); m = mw; print_int(m.get_or(4, -1)); print_int(m.get_or(6, -1)); if (m.has(5)) { print_int(1); } else { print_int(0); } print_int(m.len()); return 0; }", 0, "4609"},
	{"map-delete-all-then-reuse", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var i: i32 = 0; while (i < 30) { m = m.insert(i, i); i = i + 1; } i = 0; while (i < 30) { var (mw, we) = m.without(i); m = mw; i = i + 1; } print_int(m.len()); m = m.insert(100, 7); print_int(m.get_or(100, -1)); return 0; }", 0, "07"},
	{"strmap-delete", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1, \"b\": 2 }; var (mw, we) = m.without(\"a\"); m = mw; if (m.has(\"a\")) { print_int(1); } else { print_int(0); } print_int(m.get_or(\"b\", -1)); return 0; }", 0, "02"},
	{"map-keys-len", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; print_int(m.keys().len()); return 0; }", 0, "3"},
	{"map-values-len", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; print_int(m.values().len()); return 0; }", 0, "2"},
	{"map-values-sum", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var s: i32 = 0; for v in m.values() { s = s + v; } print_int(s); return 0; }", 0, "60"},
	{"map-keys-sum", "import \"core/map\";\nfunction main(): i32 { var m = Map { 4: 1, 5: 1, 6: 1 }; var s: i32 = 0; for k in m.keys() { s = s + k; } print_int(s); return 0; }", 0, "15"},
	{"map-values-sum-after-delete", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var (mw, we) = m.without(2); m = mw; var s: i32 = 0; for v in m.values() { s = s + v; } print_int(s); return 0; }", 0, "40"},
	{"map-empty-keys-len", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); print_int(m.keys().len()); return 0; }", 0, "0"},
	{"map-keys-sum-grow", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var i: i32 = 1; while (i <= 20) { m = m.insert(i, i); i = i + 1; } var s: i32 = 0; for k in m.keys() { s = s + k; } print_int(s); return 0; }", 0, "210"},
	{"strmap-keys-charcount", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"ab\": 1, \"cde\": 2 }; var n: i32 = 0; for k in m.keys() { n = n + k.len(); } print_int(n); return 0; }", 0, "5"},
	{"strval-values-charcount", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"ab\", 2: \"cde\" }; var n: i32 = 0; for v in m.values() { n = n + v.len(); } print_int(n); return 0; }", 0, "5"},
	{"map-forkv-sum-both", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var s: i32 = 0; for (k, v) in m { s = s + k + v; } print_int(s); return 0; }", 0, "66"},
	{"map-forkv-keys-only", "import \"core/map\";\nfunction main(): i32 { var m = Map { 4: 100, 5: 100, 6: 100 }; var s: i32 = 0; for (k, v) in m { s = s + k; } print_int(s); return 0; }", 0, "15"},
	{"map-forkv-count", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3, 4: 4 }; var n: i32 = 0; for (k, v) in m { n = n + 1; } print_int(n); return 0; }", 0, "4"},
	{"map-forkv-after-delete", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20, 3: 30 }; var (mw, we) = m.without(2); m = mw; var s: i32 = 0; for (k, v) in m { s = s + v; } print_int(s); return 0; }", 0, "40"},
	{"map-forkv-empty", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var n: i32 = 0; for (k, v) in m { n = n + 1; } print_int(n); return 0; }", 0, "0"},
	{"map-forkv-grow", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); var i: i32 = 1; while (i <= 20) { m = m.insert(i, i * 2); i = i + 1; } var s: i32 = 0; for (k, v) in m { s = s + v; } print_int(s); return 0; }", 0, "420"},
	{"map-forkv-break", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3 }; var n: i32 = 0; for (k, v) in m { n = n + 1; if (n == 2) { break; } } print_int(n); return 0; }", 0, "2"},
	{"strmap-forkv-keylen", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"ab\": 1, \"cde\": 2 }; var n: i32 = 0; for (k, v) in m { n = n + k.len() + v; } print_int(n); return 0; }", 0, "8"},
	{"strval-forkv-vallen", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: \"ab\", 2: \"cde\" }; var n: i32 = 0; for (k, v) in m { n = n + k + v.len(); } print_int(n); return 0; }", 0, "8"},
	{"tostring-i32", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 42; write(n.to_string()); return 0; }", 0, "42"},
	{"tostring-zero", "import \"std/i32\";\nfunction main(): i32 { write((0).to_string()); return 0; }", 0, "0"},
	{"tostring-negative", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0 - 17; write(n.to_string()); return 0; }", 0, "-17"},
	{"tostring-concat", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 5; write(\"n=\" + n.to_string()); return 0; }", 0, "n=5"},
	{"tostring-string-identity", "import \"std/i32\";\nimport \"std/string\";\nfunction main(): i32 { var s: string = \"hi\"; write(s.to_string()); return 0; }", 0, "hi"},
	{"tostring-i64", "import \"std/i32\";\nimport \"std/i64\";\nfunction main(): i32 { var b: i64 = 5000000000; write(b.to_string()); return 0; }", 0, "5000000000"},
	{"derive-display-struct", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 3, y: 7 }; write(p.to_string()); return 0; }", 0, "P { x: 3, y: 7 }"},
	{"derive-display-nested", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) struct Inner { n: i32 } @derive(cmp.Display) struct Outer { a: Inner, tag: string } function main(): i32 { var p: Outer = Outer { a: Inner { n: 5 }, tag: \"hi\" }; write(p.to_string()); return 0; }", 0, "Outer { a: Inner { n: 5 }, tag: hi }"},
	{"enum-derive-display-payload", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) enum Opt { Has(i32), Nil } function main(): i32 { var h: Opt = Has(7); write(h.to_string()); return 0; }", 0, "Has(7)"},
	{"enum-derive-display-unit", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) enum Opt { Has(i32), Nil } function main(): i32 { var n: Opt = Nil; write(n.to_string()); return 0; }", 0, "Nil"},
	{"inline-variant-display", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) enum Opt { Has(i32), Nil } function main(): i32 { write(Has(7).to_string()); write(\"|\"); write(Nil.to_string()); return 0; }", 0, "Has(7)|Nil"},
	{"generic-struct-display-i32", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box { v: 5 }; write(b.to_string()); return 0; }", 0, "Box { v: 5 }"},
	{"generic-struct-display-string", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) struct Box[T] { v: T } function main(): i32 { var b: Box[string] = Box { v: \"hi\" }; write(b.to_string()); return 0; }", 0, "Box { v: hi }"},
	{"generic-struct-display-both", "import \"core/cmp\";\nimport \"std/i32\";\n@derive(cmp.Display) struct Box[T] { v: T } function main(): i32 { var a: Box[i32] = Box { v: 5 }; var b: Box[string] = Box { v: \"hi\" }; write(a.to_string()); write(\"|\"); write(b.to_string()); return 0; }", 0, "Box { v: 5 }|Box { v: hi }"},
	{"generic-struct-parametric-impl", "import \"std/i32\";\ntrait Show { function show(self: Self): string; } impl Show for i32 { function show(self: Self): string { return self.to_string(); } } impl Show for string { function show(self: Self): string { return self; } } struct Box[T] { v: T } impl[T: Show] Show for Box[T] { function show(self: Self): string { return \"Box(\" + self.v.show() + \")\"; } } function main(): i32 { var a: Box[i32] = Box { v: 7 }; var b: Box[string] = Box { v: \"hi\" }; write(a.show()); write(\"|\"); write(b.show()); return 0; }", 0, "Box(7)|Box(hi)"},
	{"tostring-expr", "import \"std/i32\";\nfunction main(): i32 { write((3 * 14).to_string()); return 0; }", 0, "42"},
	{"fstring-int", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 42; write(f\"n is {n}!\"); return 0; }", 0, "n is 42!"},
	{"fstring-two", "import \"std/i32\";\nfunction main(): i32 { var a: i32 = 3; var b: i32 = 4; write(f\"{a}+{b}={a + b}\"); return 0; }", 0, "3+4=7"},
	{"fstring-string-interp", "import \"std/i32\";\nfunction main(): i32 { var who: string = \"world\"; write(f\"hello {who}\"); return 0; }", 0, "hello world"},
	{"fstring-only-interp", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 9; write(f\"{n}\"); return 0; }", 0, "9"},
	{"integration-word-count", "import \"core/map\";\nimport \"std/i32\";\nimport \"std/string\";\nfunction main(): i32 { var text: string = \"the cat sat on the mat the cat ran\"; var words: string[] = text.split(\" \"); var counts: Map[string, i32] = map_new(8); var i: i32 = 0; while (i < words.len()) { var w: string = words[i]; counts = counts.insert(w, counts.get_or(w, 0) + 1); i = i + 1; } print_int(counts.get_or(\"the\", 0)); print_int(counts.get_or(\"cat\", 0)); print_int(counts.get_or(\"mat\", 0)); write(f\" total={counts.len()}\"); return 0; }", 0, "321 total=6"},
	{"integration-struct-method", "import \"std/i32\";\nstruct Pt { x: i32, y: i32 } function (p: Pt) dist2(): i32 { return p.x * p.x + p.y * p.y; } function main(): i32 { var pts = [Pt { x: 3, y: 4 }, Pt { x: 1, y: 1 }]; var total: i32 = 0; for p in pts { total = total + p.dist2(); } write(f\"total={total}\"); return 0; }", 0, "total=27"},
	{"struct-string-field-fstring", "import \"std/i32\";\nstruct P { name: string, age: i32 } function main(): i32 { var p = P { name: \"sam\", age: 30 }; write(f\"{p.name} is {p.age}\"); return 0; }", 0, "sam is 30"},
	{"string-builder-loop", "import \"std/i32\";\nimport \"std/string\";\nfunction main(): i32 { var s: string = \"\"; var i: i32 = 0; while (i < 4) { s = s + f\"[{i}]\"; i = i + 1; } write(s); print_int(s.split(\"]\").len()); return 0; }", 0, "[0][1][2][3]5"},
	{"const-string-fstring", "import \"std/i32\";\nconst NAME: string = \"bob\"; function main(): i32 { write(f\"hello {NAME}\"); return 0; }", 0, "hello bob"},
	{"wildcard-match", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10 }; match (m.get(2)) { Some(v) => { print_int(v); }, _ => { print_int(99); } } return 0; }", 0, "99"},
	{"fstring-method-interp", "import \"std/i32\";\nimport \"std/string\";\nfunction main(): i32 { var s: string = \"hello\"; write(f\"upper={s.to_ascii_upper()}\"); return 0; }", 0, "upper=HELLO"},
	{"hex-escape-fstring", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 7; write(f\"\\x41{n}\\x5a\"); return 0; }", 0, "A7Z"},
	{"integration-reduce-closure", "import \"std/i32\";\nfunction reduce(xs: i32[], init: i32, f: (i32, i32) => i32): i32 { var acc: i32 = init; var i: i32 = 0; while (i < xs.len()) { acc = f(acc, xs[i]); i = i + 1; } return acc; } function main(): i32 { var xs = [1, 2, 3, 4, 5]; var factor: i32 = 10; var sum = reduce(xs, 0, (a: i32, b: i32): i32 => { return a + b; }); var scaled = reduce(xs, 0, (a: i32, b: i32): i32 => { return a + b * factor; }); write(f\"sum={sum} scaled={scaled}\"); return 0; }", 0, "sum=15 scaled=150"},
}

// TestSelfHostWasmLoadRunStdlib compiles each case with asm_load_run -target
// wasm32-wasi and runs the module under wasmtime, checking its exit code and
// stdout.
func TestSelfHostWasmLoadRunStdlib(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "asm_load_run")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range wasmLoadRunStdlibCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tdir := t.TempDir()
			mainPath := filepath.Join(tdir, "main.fern")
			if err := os.WriteFile(mainPath, []byte(withPrintInt(tc.src)), 0o644); err != nil {
				t.Fatal(err)
			}
			wat := runDriverFile(t, runner, bin, mainPath, root, "-target", "wasm32-wasi")
			watPath := filepath.Join(tdir, "main.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit %d, want %d\n--- source ---\n%s", code, tc.want, tc.src)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("stdout %q, want %q\n--- source ---\n%s", out, tc.stdout, tc.src)
			}
		})
	}
}

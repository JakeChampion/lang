package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
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
	// An IoError keeps a reference to the path it carries. When it did not,
	// dropping the error released the caller's path, and once() released it
	// again on return: the underflow count, not block reuse, is the check.
	{"io-error-keeps-caller-path-read", "function fails(p: string): i32 { match (read_file(p)) { Ok(_) => { return 0; }, Err(e) => { return 1; } } return 0; }\nfunction path_for(dir: string): string { return dir + \"/unit__x.wu\"; }\nfunction once(): i32 { var p: string = path_for(\"no-such-dir\"); return fails(p); }\nfunction main(): i32 { var n: i32 = once(); if (__rc_underflow_count() != 0) { return 99; } return n; }", 1, ""},
	{"io-error-keeps-caller-path-write", "function fails(p: string): i32 { match (write_file(p, \"x\")) { Ok(_) => { return 0; }, Err(e) => { return 1; } } return 0; }\nfunction path_for(dir: string): string { return dir + \"/unit__x.wu\"; }\nfunction once(): i32 { var p: string = path_for(\"no-such-dir\"); return fails(p); }\nfunction main(): i32 { var n: i32 = once(); if (__rc_underflow_count() != 0) { return 99; } return n; }", 1, ""},
	{"io-error-keeps-caller-path-stat", "function fails(p: string): i32 { match (stat(p)) { Ok(_) => { return 0; }, Err(e) => { return 1; } } return 0; }\nfunction path_for(dir: string): string { return dir + \"/unit__x.wu\"; }\nfunction once(): i32 { var p: string = path_for(\"no-such-dir\"); return fails(p); }\nfunction main(): i32 { var n: i32 = once(); if (__rc_underflow_count() != 0) { return 99; } return n; }", 1, ""},
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
	{"map-grow-str-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(2); m = m.insert(\"a\", 10); m = m.insert(\"b\", 20); m = m.insert(\"c\", 30); m = m.insert(\"d\", 13); m = m.insert(\"e\", 21); return m.get_or(\"c\", -1) + m.get_or(\"d\", -1) + __rc_underflow_count(); }", 43, ""},
	{"map-grow-i32-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(2); var k = 0; while (k < 100) { m = m.insert(k, k); k = k + 1; } return m.get_or(50, -1) + m.get_or(70, -1) + __rc_underflow_count(); }", 120, ""},
	{"map-grow-churn", "import \"core/map\";\nfunction mk(): i32 { var m: Map[i32, i32] = map_new(2); var k = 0; while (k < 30) { m = m.insert(k, k); k = k + 1; } return m.get_or(25, -1); } function main(): i32 { var n = 0; var k = 0; while (k < 5000) { n = mk(); k = k + 1; } return (n % 100) + __rc_underflow_count(); }", 25, ""},
	{"map-grow-str-vals", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(2); m = m.insert(\"x\", 1); m = m.insert(\"y\", 2); m = m.insert(\"z\", 3); m = m.insert(\"w\", 4); return m.get_or(\"x\", -1) + m.get_or(\"w\", -1) + m.len() + __rc_underflow_count(); }", 9, ""},
	{"map-swept-clean", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(3, 40); return m.get_or(3, -1) + 2 + __rc_underflow_count(); }", 42, ""},
	{"map-str-swept-clean", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"k\", 40); return m.get_or(\"k\", -1) + 2 + __rc_underflow_count(); }", 42, ""},
	{"map-counting-churn-clean", "import \"core/map\";\nfunction mk(): i32 { var m: Map[string, i32] = map_new(2); m = m.insert(\"a\", 1); m = m.insert(\"b\", 2); m = m.insert(\"c\", 3); m = m.insert(\"d\", 4); return m.get_or(\"c\", -1); } function main(): i32 { var k = 0; var s = 0; while (k < 5000) { s = mk(); k = k + 1; } return (s % 100) + __rc_underflow_count(); }", 3, ""},
	{"map-free-i32", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(3, 40); return m.get_or(3, -1) + 2 + __rc_underflow_count(); }", 42, ""},
	{"map-free-str", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 40); return m.get_or(\"a\", -1) + 2 + __rc_underflow_count(); }", 42, ""},
	{"map-free-churn-clean", "import \"core/map\";\nfunction mk(): i32 { var m: Map[i32, i32] = map_new(2); var k = 0; while (k < 30) { m = m.insert(k, k); k = k + 1; } return m.get_or(25, -1); } function main(): i32 { var n = 0; var k = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 100) + __rc_underflow_count(); }", 25, ""},
	{"map-heap-key-released", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); var k: string = \"ab\" + \"cd\"; m = m.insert(k, 5); return m.get_or(k, -1) + 37 + __rc_underflow_count(); }", 42, ""},
	{"map-literal-key-clean", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"x\", 40); return m.get_or(\"x\", -1) + 2 + __rc_underflow_count(); }", 42, ""},
	{"map-heap-key-churn", "import \"core/map\";\nfunction mk(): i32 { var m: Map[string, i32] = map_new(2); var a: string = \"k\" + \"1\"; var b: string = \"k\" + \"2\"; m = m.insert(a, 3); m = m.insert(b, 4); return m.get_or(a, -1) + m.get_or(b, -1); } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 100) + __rc_underflow_count(); }", 7, ""},
	{"map-str-value-released", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, string] = map_new(8); var v: string = \"ab\" + \"cd\"; m = m.insert(1, v); return m.get_or(1, \"\").len() + 38 + __rc_underflow_count(); }", 42, ""},
	{"map-str-value-literal-clean", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, string] = map_new(8); m = m.insert(5, \"hello\"); return m.get_or(5, \"\").len() + 37 + __rc_underflow_count(); }", 42, ""},
	{"map-str-value-overwrite", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, string] = map_new(8); var a: string = \"x\" + \"x\"; var b: string = \"yy\" + \"zz\"; m = m.insert(7, a); m = m.insert(7, b); return m.get_or(7, \"\").len() + 38 + __rc_underflow_count(); }", 42, ""},
	{"map-str-key-and-value", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, string] = map_new(8); var v: string = \"ab\" + \"cd\"; m = m.insert(\"key\", v); return m.get_or(\"key\", \"\").len() + 38 + __rc_underflow_count(); }", 42, ""},
	{"map-str-value-churn", "import \"core/map\";\nfunction mk(): i32 { var m: Map[i32, string] = map_new(2); var a: string = \"k\" + \"1\"; var b: string = \"k\" + \"2\"; m = m.insert(1, a); m = m.insert(2, b); return m.get_or(1, \"\").len() + m.get_or(2, \"\").len(); } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 4, ""},
	{"map-arrval-get", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32[]] = map_new(8); m = m.insert(1, [40, 2]); var xs = m.get_or(1, [0, 0]); return xs[0] + xs[1] + __rc_underflow_count(); }", 42, ""},
	{"map-arrval-churn", "import \"core/map\";\nfunction mk(): i32 { var m: Map[i32, i32[]] = map_new(2); m = m.insert(1, [1, 2, 3, 4]); m = m.insert(2, [5, 6, 7, 8]); var xs = m.get_or(1, [0]); return xs[3]; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 4, ""},
	{"map-strkey-arrval-churn", "import \"core/map\";\nfunction mk(): i32 { var m: Map[string, i32[]] = map_new(2); var a: string = \"k\" + \"1\"; m = m.insert(a, [9, 8, 7]); var xs = m.get_or(a, [0]); return xs[0]; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2, ""},
	{"map-struct-val", "import \"core/map\";\nstruct P { x: i32, y: i32 } function main(): i32 { var m: Map[i32, P] = map_new(8); m = m.insert(1, P { x: 40, y: 2 }); var p = m.get_or(1, P { x: 0, y: 0 }); return p.x + p.y + __rc_underflow_count(); }", 42, ""},
	{"map-struct-deep-churn", "import \"core/map\";\nstruct Inner { xs: i32[], n: i32 } function mk(): i32 { var m: Map[i32, Inner] = map_new(2); m = m.insert(1, Inner { xs: [1, 2, 3, 4], n: 5 }); m = m.insert(2, Inner { xs: [6, 7, 8, 9], n: 1 }); var p = m.get_or(1, Inner { xs: [0], n: 0 }); return p.n; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5, ""},
	{"map-strkey-structval", "import \"core/map\";\nstruct P { x: i32, y: i32 } function main(): i32 { var m: Map[string, P] = map_new(8); var k: string = \"a\" + \"b\"; m = m.insert(k, P { x: 40, y: 2 }); var p = m.get_or(k, P { x: 0, y: 0 }); return p.x + p.y + __rc_underflow_count(); }", 42, ""},
	{"map-enum-deep-churn", "import \"core/map\";\nenum Shape { Circle(string), Square(i32) } function mk(): i32 { var m: Map[i32, Shape] = map_new(2); m = m.insert(1, Circle(\"a\" + \"b\")); m = m.insert(2, Circle(\"c\" + \"d\")); return 2; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2, ""},
	{"without-len", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = Map { \"a\": 1, \"b\": 2 }; var (m2, e) = m.without(\"a\"); return m2.len(); }", 1, ""},
	{"without-get-or", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = Map { \"a\": 1, \"b\": 2 }; var (m2, e) = m.without(\"a\"); return m2.get_or(\"b\", -1); }", 2, ""},
	{"without-existed-true", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = Map { \"a\": 1, \"b\": 2 }; var (m2, e) = m.without(\"a\"); if (e) { return 7; } return 0; }", 7, ""},
	{"without-absent", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = Map { \"a\": 1, \"b\": 2 }; var (m2, e) = m.without(\"zzz\"); if (e) { return 99; } return m2.len(); }", 2, ""},
	{"without-i32-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = Map { 1: 10, 2: 20 }; var (m2, e) = m.without(1); return m2.len() * 100 + m2.get_or(2, -1); }", 120, ""},
	{"without-to-empty", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = Map { \"k\": 5 }; var (m2, e) = m.without(\"k\"); if (m2.len() == 0) { return 42 - m2.get_or(\"k\", 0); } return 0; }", 42, ""},
	{"map-literal-no-closure", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; var ks: i32[] = m.keys(); if (ks.len() != 2) { return 1; } if (m.get_or(1, 0) != 10) { return 2; } if (m.get_or(2, 0) != 20) { return 3; } return 0; }", 0, ""},
	{"map-new-no-closure", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); if (m.len() != 2) { return 1; } if (!m.has(1)) { return 2; } if (m.get_or(2, 0) != 20) { return 3; } return 0; }", 0, ""},
	{"map-plus-closure", "import \"core/map\";\nfunction apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var m = Map { 5: 50, 6: 60 }; var g: (i32) => i32 = (n: i32) => n + 1; var r = apply(g, 7); if (r != 8) { return 1; } if (m.len() != 2) { return 2; } if (m.get_or(5, 0) != 50) { return 3; } return 0; }", 0, ""},
	{"freshbuiltin-mapkeys-swept", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; var ks: i32[] = m.keys(); return ks.len() + __rc_underflow_count(); }", 2, ""},
	{"freshbuiltin-values-loop", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; var s = 0; var k = 0; while (k < 100) { var vs: i32[] = m.values(); s = s + vs[0]; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6, ""},
	{"map-value-closure-captured", "import \"core/map\"; function main(): i32 { var n = 10; var m: Map[i32, () => i32] = map_new(4); m = m.insert(1, (): i32 => { return n + 7; }); match (m.get(1)) { Some(f) => { return f(); }, None => { return 0; } } }", 17, ""},
	{"map-i32-len3", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }", 3, ""},
	{"map-i32-overwrite", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 40); m = m.insert(11, 99); m = m.insert(7, 42); return m.len(); }", 2, ""},
	{"map-i32-loop", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); var i = 0; while (i < 5) { m = m.insert(i, i*10); i = i + 1; } return m.len(); }", 5, ""},
	{"map-str-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"a\", 1); m = m.insert(\"bb\", 2); m = m.insert(\"a\", 9); return m.len(); }", 2, ""},
	{"map-get-hit", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(7)) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 42, ""},
	{"map-get-miss", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(999)) { Some(v) => { return v; }, None => { return 5; } } return 9; }", 5, ""},
	{"irpath-map-has", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 1); var r = 0; if (m.has(1)) { r = r + 1; } if (m.has(2)) { r = r + 10; } return r; }", 1, ""},
	{"map-get-strkey", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); match (m.get(\"hi\")) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 11, ""},
	{"map-get-or-hit", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(7, 0); }", 42, ""},
	{"map-get-or-miss", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(999, 5); }", 5, ""},
	{"map-get-or-strhit", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"hi\", 0); }", 11, ""},
	{"map-get-or-strmiss", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"no\", 7); }", 7, ""},
	{"irpath-map-keys-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var ks: i32[] = m.keys(); var s = 0; var i = 0; while (i < ks.len()) { s = s + ks[i]; i = i + 1; } return s; }", 6, ""},
	{"irpath-map-values-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var vs: i32[] = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s; }", 60, ""},
	{"map-forkv-values", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + v; } return s; }", 60, ""},
	{"map-forkv-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + k; } return s; }", 6, ""},
	{"map-forkv-pair", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); m = m.insert(2, 3); m = m.insert(3, 4); var s = 0; for (k, v) in m { s = s + k * v; } return s; }", 20, ""},
	{"map-forkv-strkey", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"ab\", 1); m = m.insert(\"cde\", 2); var s = 0; for (k, v) in m { s = s + k.len() + v; } return s; }", 8, ""},
	{"map-insert-i32-len", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }", 3, ""},
	{"map-insert-str-getor", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"a\", 1); m = m.insert(\"bb\", 2); return m.get_or(\"bb\", 0) + m.len(); }", 4, ""},
	{"map-insert-chained", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"x\", 5).insert(\"y\", 7); return m.get_or(\"y\", 0) + m.len(); }", 9, ""},
	{"map-insert-keyword-literal", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = Map { \"a\": 1, \"b\": 2 }; return m.get_or(\"b\", 0) + m.len(); }", 4, ""},
	{"derive-debug-struct", "import \"core/cmp\";\n@derive(cmp.Debug) struct P { x: i32, name: string } function main(): i32 { return P { x: 7, name: \"hi\" }.to_debug().len(); }", 22, ""},
	{"derive-debug-enum", "import \"core/cmp\";\n@derive(cmp.Debug) enum E { Dot, Circle(i32), Tag(string) } function main(): i32 { return Dot.to_debug().len() + Circle(5).to_debug().len() + Tag(\"ab\").to_debug().len(); }", 21, ""},
	{"split-freecall", "import \"std/string\";\nfunction main(): i32 { var p = \"a,b,c\".split(\",\"); return p.len(); }", 3, ""},
	{"fstring-i32", "import \"std/i32\";\nfunction main(): i32 { var n = 7; var s = f\"n={n}!\"; return s.len(); }", 4, ""},
	{"fstring-i32-char", "import \"std/i32\";\nfunction main(): i32 { var n = 7; var s = f\"n={n}!\"; return s[2] as i32; }", 55, ""},
	{"fstring-str", "import \"std/i32\";\nfunction main(): i32 { var w = \"xy\"; var s = f\"[{w}]\"; return s.len(); }", 4, ""},
	{"fstring-expr", "import \"std/i32\";\nfunction main(): i32 { var a = 10; var s = f\"v={a * 2}\"; return s[2] as i32; }", 50, ""},
	{"fstring-method", "import \"std/i32\";\nfunction main(): i32 { var w = \"hi\"; return f\"v={w.len()}\".len(); }", 3, ""},
	{"fstring-multi", "import \"std/i32\";\nfunction main(): i32 { var a = 1; var b = 2; return f\"{a}{b}\".len(); }", 2, ""},
	{"to-upper-byte", "import \"std/string\";\nfunction main(): i32 { var s = \"abc\"; var u = s.to_ascii_upper(); return (u[0] as i32); }", 65, ""},
	{"to-lower-byte", "import \"std/string\";\nfunction main(): i32 { var s = \"ABC\"; var l = s.to_ascii_lower(); return (l[2] as i32); }", 99, ""},
	{"to-upper-mixed", "import \"std/string\";\nfunction main(): i32 { var u = \"aB9z\".to_ascii_upper(); return ((u[0] as i32) + (u[1] as i32) + (u[2] as i32) + (u[3] as i32)) % 100; }", 78, ""},
	{"case-param", "import \"std/string\";\nfunction up(s: string): i32 { return s.to_ascii_upper()[0] as i32; } function main(): i32 { return up(\"xyz\"); }", 88, ""},
	{"repeat-byte", "import \"std/string\";\nfunction main(): i32 { var r = \"xy\".repeat(4); return (r[0] as i32) + (r[7] as i32) - 200; }", 41, ""},
	{"map-field-get_or", "import \"core/map\";\nstruct Cache { m: Map[i32, i32], hits: i32 } function main(): i32 { var c = Cache{m: Map { 5: 50, 7: 70 }, hits: 1}; return c.m.get_or(5, 0) + c.m.get_or(7, 0) + c.hits; }", 121, ""},
	{"map-field-method", "import \"core/map\";\nstruct Cfg { table: Map[string, i32] } function (c: Cfg) lookup(k: string): i32 { return c.table.get_or(k, 0); } function main(): i32 { var c = Cfg{table: Map { \"a\": 3, \"b\": 4 }}; return c.lookup(\"a\") + c.lookup(\"b\"); }", 7, ""},
	{"map-field-has-len", "import \"core/map\";\nstruct Cache { m: Map[string, i32] } function main(): i32 { var c = Cache{m: Map { \"a\": 1, \"b\": 2 }}; var t = 0; if (c.m.has(\"a\")) { t = t + c.m.len(); } return t; }", 2, ""},
	{"map-param-get_or", "import \"core/map\";\nfunction total(m: Map[i32, i32]): i32 { return m.get_or(1, 0) + m.get_or(2, 0); } function main(): i32 { var m: Map[i32, i32] = Map { 1: 10, 2: 20 }; return total(m); }", 30, ""},
	{"map-param-string-key", "import \"core/map\";\nfunction look(m: Map[string, i32], k: string): i32 { return m.get_or(k, 0); } function main(): i32 { var m: Map[string, i32] = Map { \"x\": 7 }; return look(m, \"x\"); }", 7, ""},
	{"map-field-keys-forin", "import \"core/map\";\nstruct Cfg { m: Map[i32, i32] } function main(): i32 { var c = Cfg{m: Map { 1: 10, 2: 20, 3: 30 }}; var t = 0; for k in c.m.keys() { t = t + c.m.get_or(k, 0); } return t; }", 60, ""},
	{"map-field-values-forin", "import \"core/map\";\nstruct Cfg { m: Map[string, i32] } function main(): i32 { var c = Cfg{m: Map { \"a\": 3, \"b\": 4 }}; var t = 0; for v in c.m.values() { t = t + v; } return t; }", 7, ""},
	{"map-ret-fn-binding", "import \"core/map\";\nfunction build(): Map[i32, i32] { return Map { 1: 5, 2: 6 }; } function main(): i32 { var m = build(); return m.get_or(1, 0) + m.get_or(2, 0); }", 11, ""},
	{"map-ret-method-binding", "import \"core/map\";\nstruct Reg { base: i32 } function (r: Reg) table(): Map[i32, i32] { return Map { 1: r.base, 2: r.base + 1 }; } function main(): i32 { var reg = Reg{base: 10}; var m = reg.table(); return m.get_or(1, 0) + m.get_or(2, 0); }", 21, ""},
	{"map-tuple-elem-get_or", "import \"core/map\";\nfunction main(): i32 { var t = (Map { 1: 10 }, 5); return t.0.get_or(1, 0) + t.1; }", 15, ""},
	{"map-tuple-elem-rebind", "import \"core/map\";\nfunction main(): i32 { var t = (Map { 1: 10 }, 5); var m = t.0; return m.get_or(1, 0) + t.1; }", 15, ""},
	{"map-tuple-elem-string-val", "import \"core/map\";\nfunction main(): i32 { var t = (Map { 1: \"abcd\" }, 5); return t.0.get_or(1, \"z\").len() + t.1; }", 9, ""},
	{"map-array-elem-get_or", "import \"core/map\";\nfunction main(): i32 { var ms = [Map { 1: 10 }, Map { 1: 20 }]; return ms[0].get_or(1, 0) + ms[1].get_or(1, 0); }", 30, ""},
	{"map-array-elem-rebind", "import \"core/map\";\nfunction main(): i32 { var ms = [Map { 1: 10 }, Map { 1: 20 }]; var m = ms[1]; return m.get_or(1, 0) + ms[0].get_or(1, 0); }", 30, ""},
	{"map-array-elem-annotated", "import \"core/map\";\nfunction main(): i32 { var ms: Map[i32, i32][] = [Map { 1: 10 }]; return ms[0].get_or(1, 0); }", 10, ""},
	{"map-array-elem-string-val", "import \"core/map\";\nfunction main(): i32 { var ms = [Map { 1: \"abcd\" }]; return ms[0].get_or(1, \"z\").len(); }", 4, ""},
	{"bytes-vals", "import \"std/string\";\nfunction main(): i32 { var b: u8[] = \"AB\".bytes(); if (b[0] != 65) { return 20; } if (b[1] != 66) { return 21; } return 6; }", 6, ""},
	{"trim-byte", "import \"std/string\";\nfunction main(): i32 { var t = \"  hi\".trim(); return (t[0] as i32); }", 104, ""},
	{"replace-byte", "import \"std/string\";\nfunction main(): i32 { var r = \"hello\".replace(\"l\", \"L\"); return (r[2] as i32); }", 76, ""},
	{"lines-elem", "import \"std/string\";\nfunction main(): i32 { var ls = \"ab\\ncd\".lines(); return (ls[1][0] as i32); }", 99, ""},
	{"derive-debug-struct-len", "import \"core/cmp\";\n@derive(cmp.Debug) struct P { x: i32, name: string } function main(): i32 { return P { x: 7, name: \"hi\" }.to_debug().len(); }", 22, ""},
	{"derive-debug-enum-len", "import \"core/cmp\";\n@derive(cmp.Debug) enum E { Dot, Circle(i32), Tag(string) } function main(): i32 { return Dot.to_debug().len() + Circle(5).to_debug().len() + Tag(\"ab\").to_debug().len(); }", 21, ""},
	{"map-has-and-not", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); if (m.has(1) && !m.has(2)) { return 7; } return 0; }", 7, ""},
	{"map-has-and-true", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); if (m.has(1) && m.has(2)) { return 5; } return 0; }", 5, ""},
	{"map-has-or-short", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); if (m.has(1) || m.has(2)) { return 9; } return 0; }", 9, ""},
	// target_os() / target_arch() are folded to the -target's names before the
	// typed lowering, which has no body for either call.
	{"target-name-folds", "function main(): i32 { write(target_os()); write(\"|\"); write(target_arch()); if (target_arch() == \"wasm32\") { return 0; } return 1; }", 0, "wasi|wasm32"},
}

// wasmStdlibLoader compiles programs that import the standard library to WAT
// through asm_load_run's wasm32-wasi leg with the stdlib root.
type wasmStdlibLoader struct {
	runner []string
	bin    string
	root   string
}

func newWasmStdlibLoader(t *testing.T) *wasmStdlibLoader {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	return &wasmStdlibLoader{runner: runner, bin: buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "asm_load_run"), root: root}
}

// emit compiles src and returns the WAT; extra flags follow the target.
func (l *wasmStdlibLoader) emit(t *testing.T, src string, extra ...string) []byte {
	t.Helper()
	mainPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return runDriverFile(t, l.runner, l.bin, mainPath, append([]string{l.root, "-target", "wasm32-wasi"}, extra...)...)
}

// TestSelfHostWasmLoadRunStdlib compiles each case with asm_load_run -target
// wasm32-wasi and runs the module under wasmtime, checking its exit code and
// stdout.
func TestSelfHostWasmLoadRunStdlib(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	l := newWasmStdlibLoader(t)
	for _, tc := range wasmLoadRunStdlibCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			watPath := filepath.Join(t.TempDir(), "main.wat")
			if err := os.WriteFile(watPath, l.emit(t, withPrintInt(tc.src)), 0o644); err != nil {
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

// TestSelfHostAsmLoadRunEmitForm pins asm_load_run's `-emit component-core`:
// the core it emits is the component-mode core (an _lang_run export, no
// preview1 imports), and the form is refused off wasm32-wasi.
func TestSelfHostAsmLoadRunEmitForm(t *testing.T) {
	l := newWasmStdlibLoader(t)
	wat := string(l.emit(t, "function main(): i32 { write(\"hi\"); return 0; }", "-emit", "component-core"))
	if !strings.Contains(wat, `(export "_lang_run"`) || strings.Contains(wat, "wasi_snapshot_preview1") {
		t.Errorf("-emit component-core did not emit a component core:\n%s", wat)
	}
	mainPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(mainPath, []byte("function main(): i32 { return 0; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := e2eharness.RunX86_64Bin(l.runner, l.bin, mainPath, l.root, "-target", "x86-64-linux", "-emit", "component-core")
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 2 || !strings.Contains(string(out), "-emit component-core is not an output form for x86-64-linux") {
		t.Errorf("-emit component-core on x86-64-linux: exit %d, want 2 with the refusal\n%s", code, out)
	}
}

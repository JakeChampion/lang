package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// asmLoadRunStdlibCases call the standard library, so they compile through
// asm_load_run with the stdlib root rather than asm_run, which loads no
// imports.
var asmLoadRunStdlibCases = []struct {
	name   string
	src    string
	want   int
	stdout string // "" means don't check
}{
	{"map-get-or-string", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 10); var a: i32 = m.get_or(\"a\", 0); var b: i32 = m.get_or(\"missing\", 99); return a + b; }", 109, ""},
	{"wider-int-as-cast-to-string", "import \"std/u32\";\nimport \"std/u64\";\nfunction main(): i32 { var a: u32 = 99 as u32; var b: u64 = 7 as u64; if (a.to_string() != \"99\") { return 1; } if (b.to_string() != \"7\") { return 2; } return 42; }", 42, ""},
	{"u32-high-bit-to-string", "import \"std/u32\";\nfunction main(): i32 { if ((4294967295 as u32).to_string() != \"4294967295\") { return 1; } if (((1 as u32) << (31 as u32)).to_string() != \"2147483648\") { return 2; } if (((0 as u32) - (1 as u32)).to_string() != \"4294967295\") { return 3; } return 42; }", 42, ""},
	{"i32-abs-positive", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 7; return n.abs(); }", 7, ""},
	{"i32-abs-negative", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0 - 42; return n.abs(); }", 42, ""},
	{"i32-abs-zero", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; return n.abs(); }", 0, ""},
	{"i32-is-zero-true", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; if (n.is_zero()) { return 1; } return 0; }", 1, ""},
	{"i32-is-zero-false", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 5; if (n.is_zero()) { return 1; } return 0; }", 0, ""},
	{"i32-is-positive-true", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 5; if (n.is_positive()) { return 1; } return 0; }", 1, ""},
	{"i32-is-positive-false-zero", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; if (n.is_positive()) { return 1; } return 0; }", 0, ""},
	{"i32-is-positive-false-negative", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0 - 5; if (n.is_positive()) { return 1; } return 0; }", 0, ""},
	{"i32-is-negative-true", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0 - 5; if (n.is_negative()) { return 1; } return 0; }", 1, ""},
	{"i32-is-negative-false-zero", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; if (n.is_negative()) { return 1; } return 0; }", 0, ""},
	{"i32-is-even-true", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 4; if (n.is_even()) { return 1; } return 0; }", 1, ""},
	{"i32-is-even-false", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 7; if (n.is_even()) { return 1; } return 0; }", 0, ""},
	{"i32-is-odd-true", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 9; if (n.is_odd()) { return 1; } return 0; }", 1, ""},
	{"i32-is-odd-false", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 8; if (n.is_odd()) { return 1; } return 0; }", 0, ""},
	{"i32-is-even-zero", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; if (n.is_even()) { return 1; } return 0; }", 1, ""},
	{"i32-sign-positive", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 42; return n.signum(); }", 1, ""},
	{"i32-sign-zero", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; return n.signum(); }", 0, ""},
	{"i32-sign-negative", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0 - 7; print_int(n.signum()); return 0; }", 0, "-1"},
	{"i32-clamp-in-range", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 5; return n.clamp(0, 10); }", 5, ""},
	{"i32-clamp-below", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0 - 3; return n.clamp(0, 10); }", 0, ""},
	{"i32-clamp-above", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 99; return n.clamp(0, 10); }", 10, ""},
	{"i32-clamp-equals-low", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; return n.clamp(0, 10); }", 0, ""},
	{"i32-clamp-equals-high", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 10; return n.clamp(0, 10); }", 10, ""},
	{"i32-min-pick-first", "import \"std/i32\";\nfunction main(): i32 { var a: i32 = 3; return a.min(7); }", 3, ""},
	{"i32-min-pick-second", "import \"std/i32\";\nfunction main(): i32 { var a: i32 = 9; return a.min(4); }", 4, ""},
	{"i32-max-pick-first", "import \"std/i32\";\nfunction main(): i32 { var a: i32 = 8; return a.max(3); }", 8, ""},
	{"i32-max-pick-second", "import \"std/i32\";\nfunction main(): i32 { var a: i32 = 2; return a.max(11); }", 11, ""},
	{"i32-min-equal", "import \"std/i32\";\nfunction main(): i32 { var a: i32 = 5; return a.min(5); }", 5, ""},
	{"arr-i32-first", "import \"std/array\";\nimport \"std/option\";\nfunction main(): i32 { var xs: i32[] = [10, 20, 30]; return xs.first().unwrap_or(0 - 1); }", 10, ""},
	{"arr-i32-last", "import \"std/array\";\nimport \"std/option\";\nfunction main(): i32 { var xs: i32[] = [10, 20, 30]; return xs.last().unwrap_or(0 - 1); }", 30, ""},
	{"arr-i32-first-single", "import \"std/array\";\nimport \"std/option\";\nfunction main(): i32 { var xs: i32[] = [99]; return xs.first().unwrap_or(0 - 1); }", 99, ""},
	{"arr-i32-last-single", "import \"std/array\";\nimport \"std/option\";\nfunction main(): i32 { var xs: i32[] = [99]; return xs.last().unwrap_or(0 - 1); }", 99, ""},
	{"arr-string-first", "import \"std/array\";\nimport \"std/option\";\nfunction main(): i32 { var xs: string[] = [\"hello\", \"world\"]; write(xs.first().unwrap_or(\"\")); return 0; }", 0, "hello"},
	{"arr-last-evaluates-receiver-once", "import \"std/array\";\nimport \"std/option\";\nfunction build(): i32[] { write(\"b\"); var out: i32[] = []; out = out.append(7); return out; }\nfunction main(): i32 { return build().last().unwrap_or(0 - 1); }", 7, "b"},
	{"arr-string-last", "import \"std/array\";\nimport \"std/option\";\nfunction main(): i32 { var xs: string[] = [\"hello\", \"world\"]; write(xs.last().unwrap_or(\"\")); return 0; }", 0, "world"},
	{"str-method-is-empty-false", "import \"std/string\";\nfunction main(): i32 { var s = \"hi\"; if (s.is_empty()) { return 1; } return 0; }", 0, ""},
	{"str-method-is-empty-true", "import \"std/string\";\nfunction main(): i32 { var s = \"\"; if (s.is_empty()) { return 1; } return 0; }", 1, ""},
	{"arr-method-is-empty-false", "import \"std/array\";\nfunction main(): i32 { var xs: i32[] = [1]; if (xs.is_empty()) { return 1; } return 0; }", 0, ""},
	{"arr-method-is-empty-true", "import \"std/array\";\nfunction main(): i32 { var xs: i32[] = []; if (xs.is_empty()) { return 1; } return 0; }", 1, ""},
	{"str-bytes-value", "import \"std/string\";\nfunction main(): i32 { var s = \"A\"; var bs = s.bytes(); return bs[0] as i32; }", 65, ""},
	{"str-bytes-multi", "import \"std/string\";\nfunction main(): i32 { var s = \"abc\"; var bs = s.bytes(); print_int((bs[0] as i32) + (bs[1] as i32) + (bs[2] as i32)); return 0; }", 0, "294"},
	{"map-i32-len3", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 100); m = m.insert(2, 200); m = m.insert(3, 300); return m.len(); }", 3, ""},
	{"map-i32-overwrite", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 40); m = m.insert(11, 99); m = m.insert(7, 42); return m.len(); }", 2, ""},
	{"map-i32-loop", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); var i = 0; while (i < 5) { m = m.insert(i, i*10); i = i + 1; } return m.len(); }", 5, ""},
	{"map-str-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"a\", 1); m = m.insert(\"bb\", 2); m = m.insert(\"a\", 9); return m.len(); }", 2, ""},
	{"map-get-hit", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(7)) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 42, ""},
	{"map-get-miss", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); match (m.get(999)) { Some(v) => { return v; }, None => { return 5; } } return 9; }", 5, ""},
	{"map-has", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 1); var r = 0; if (m.has(1)) { r = r + 1; } if (m.has(2)) { r = r + 10; } return r; }", 1, ""},
	{"map-get-strkey", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); match (m.get(\"hi\")) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 11, ""},
	{"map-get-or-hit", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(7, 0); }", 42, ""},
	{"map-get-or-miss", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 42); return m.get_or(999, 5); }", 5, ""},
	{"map-get-or-strhit", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"hi\", 0); }", 11, ""},
	{"map-get-or-strmiss", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert(\"hi\", 11); return m.get_or(\"no\", 7); }", 7, ""},
	{"map-keys-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var ks: i32[] = m.keys(); var s = 0; var i = 0; while (i < ks.len()) { s = s + ks[i]; i = i + 1; } return s; }", 6, ""},
	{"map-values-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var vs: i32[] = m.values(); var s = 0; var i = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s; }", 60, ""},
	{"map-forkv-values", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + v; } return s; }", 60, ""},
	{"map-forkv-keys", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); var s = 0; for (k, v) in m { s = s + k; } return s; }", 6, ""},
	{"map-forkv-pair", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); m = m.insert(2, 3); m = m.insert(3, 4); var s = 0; for (k, v) in m { s = s + k * v; } return s; }", 20, ""},
	{"map-forkv-strkey", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"ab\", 1); m = m.insert(\"cde\", 2); var s = 0; for (k, v) in m { s = s + k.len() + v; } return s; }", 8, ""},
	{"i32-to-string-zero", "import \"std/i32\";\nfunction main(): i32 { var s = (0).to_string(); write(s); return s.len(); }", 1, "0"},
	{"i32-dot-to-string", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 42; var s = n.to_string(); write(s); return s.len(); }", 2, "42"},
	{"i32-dot-to-string-in-closure", "import \"std/i32\";\nfunction main(): i32 { var n = 5; var f = (): string => { return n.to_string(); }; write(f()); return 0; }", 0, "5"},
	{"i32-dot-to-string-concat", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 99; var msg: string = \"value=\" + n.to_string(); write(msg); return 0; }", 0, "value=99"},
	{"i32-dot-to-string-zero", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0; var s = n.to_string(); write(s); return s.len(); }", 1, "0"},
	{"i32-dot-to-string-negative", "import \"std/i32\";\nfunction main(): i32 { var n: i32 = 0 - 7; var s = n.to_string(); write(s); return s.len(); }", 2, "-7"},
	{"string-dot-to-string-identity", "import \"std/string\";\nfunction main(): i32 { var s = \"hi\"; var t = s.to_string(); write(t); return t.len(); }", 2, "hi"},
	{"i32-to-string-positive", "import \"std/i32\";\nfunction main(): i32 { var s = (12345).to_string(); write(s); return s.len(); }", 5, "12345"},
	{"i32-to-string-negative", "import \"std/i32\";\nfunction main(): i32 { var s = (0 - 42).to_string(); write(s); return s.len(); }", 3, "-42"},
	{"i32-to-string-concat", "import \"std/i32\";\nfunction main(): i32 { var n = 7; var msg = \"answer: \" + n.to_string(); write(msg); return 0; }", 0, "answer: 7"},
	{"str-trim-spaces", "import \"std/string\";\nfunction main(): i32 { var t = \"   hello   \".trim(); write(t); return t.len(); }", 5, "hello"},
	{"str-trim-tabs-newlines", "import \"std/string\";\nfunction main(): i32 { var t = \"\\t\\n hi \\r\\n\".trim(); write(t); return t.len(); }", 2, "hi"},
	{"str-trim-no-whitespace", "import \"std/string\";\nfunction main(): i32 { var t = \"abc\".trim(); write(t); return t.len(); }", 3, "abc"},
	{"str-trim-empty", "import \"std/string\";\nfunction main(): i32 { var t = \"\".trim(); return t.len(); }", 0, ""},
	{"str-trim-all-whitespace", "import \"std/string\";\nfunction main(): i32 { var t = \"   \\n\\t \".trim(); return t.len(); }", 0, ""},
	{"str-to-upper-basic", "import \"std/string\";\nfunction main(): i32 { var u = \"hello\".to_upper(); write(u); return u.len(); }", 5, "HELLO"},
	{"str-to-upper-mixed", "import \"std/string\";\nfunction main(): i32 { var u = \"Hi 123 World!\".to_upper(); write(u); return 0; }", 0, "HI 123 WORLD!"},
	{"str-to-lower-basic", "import \"std/string\";\nfunction main(): i32 { var l = \"HELLO\".to_lower(); write(l); return l.len(); }", 5, "hello"},
	{"str-to-lower-mixed", "import \"std/string\";\nfunction main(): i32 { var l = \"AbCdE 99\".to_lower(); write(l); return 0; }", 0, "abcde 99"},
	{"str-to-upper-empty", "import \"std/string\";\nfunction main(): i32 { return (\"\".to_upper()).len(); }", 0, ""},
	{"str-case-round-trip", "import \"std/string\";\nfunction main(): i32 { var s = (\"AbCd\".to_upper()).to_lower(); write(s); return 0; }", 0, "abcd"},
	{"str-repeat-basic", "import \"std/string\";\nfunction main(): i32 { var r = \"ab\".repeat(3); write(r); return r.len(); }", 6, "ababab"},
	{"str-repeat-once", "import \"std/string\";\nfunction main(): i32 { var r = \"hi\".repeat(1); write(r); return r.len(); }", 2, "hi"},
	{"str-repeat-zero", "import \"std/string\";\nfunction main(): i32 { var r = \"foo\".repeat(0); return r.len(); }", 0, ""},
	{"str-repeat-negative", "import \"std/string\";\nfunction main(): i32 { var r = \"foo\".repeat(0 - 3); return r.len(); }", 0, ""},
	{"str-repeat-empty-source", "import \"std/string\";\nfunction main(): i32 { var r = \"\".repeat(5); return r.len(); }", 0, ""},
	{"str-repeat-many", "import \"std/string\";\nfunction main(): i32 { var r = \"-=\".repeat(4); write(r); return r.len(); }", 8, "-=-=-=-="},
	{"str-replace-basic", "import \"std/string\";\nfunction main(): i32 { var r = \"hello world\".replace(\"world\", \"there\"); write(r); return r.len(); }", 11, "hello there"},
	{"str-replace-shorter", "import \"std/string\";\nfunction main(): i32 { var r = \"abcabc\".replace(\"abc\", \"x\"); write(r); return r.len(); }", 2, "xx"},
	{"str-replace-longer", "import \"std/string\";\nfunction main(): i32 { var r = \"a-b\".replace(\"-\", \"---\"); write(r); return r.len(); }", 5, "a---b"},
	{"str-replace-none", "import \"std/string\";\nfunction main(): i32 { var r = \"hello\".replace(\"xyz\", \"---\"); write(r); return r.len(); }", 5, "hello"},
	{"str-replace-empty-old", "import \"std/string\";\nfunction main(): i32 { var r = \"abc\".replace(\"\", \"xyz\"); write(r); return r.len(); }", 3, "abc"},
	{"str-replace-all-occurrences", "import \"std/string\";\nfunction main(): i32 { var r = \"banana\".replace(\"a\", \"!\"); write(r); return r.len(); }", 6, "b!n!n!"},
	{"str-replace-empty-new", "import \"std/string\";\nfunction main(): i32 { var r = \"banana\".replace(\"a\", \"\"); write(r); return r.len(); }", 3, "bnn"},
	{"str-split-basic", "import \"std/string\";\nfunction main(): i32 { var a = \"a,b,c\".split(\",\"); return a.len(); }", 3, ""},
	{"str-split-content", "import \"std/string\";\nfunction main(): i32 { var a = \"a,bb,ccc\".split(\",\"); for s in a { write(s); write(\"|\"); } return 0; }", 0, "a|bb|ccc|"},
	{"str-split-no-sep", "import \"std/string\";\nfunction main(): i32 { var a = \"abc\".split(\",\"); return a.len(); }", 1, ""},
	{"str-split-empty-sep", "import \"std/string\";\nfunction main(): i32 { var a = \"hello\".split(\"\"); for s in a { write(s); write(\"|\"); } return a.len(); }", 5, "h|e|l|l|o|"},
	{"str-split-leading-sep", "import \"std/string\";\nfunction main(): i32 { var a = \",a,b\".split(\",\"); for s in a { write(s); write(\"|\"); } return a.len(); }", 3, "|a|b|"},
	{"str-split-trailing-sep", "import \"std/string\";\nfunction main(): i32 { var a = \"a,b,\".split(\",\"); for s in a { write(s); write(\"|\"); } return a.len(); }", 3, "a|b||"},
	{"str-split-multi-char-sep", "import \"std/string\";\nfunction main(): i32 { var a = \"foo--bar--baz\".split(\"--\"); for s in a { write(s); write(\"|\"); } return a.len(); }", 3, "foo|bar|baz|"},
	{"str-split-consecutive", "import \"std/string\";\nfunction main(): i32 { var a = \"a,,b\".split(\",\"); for s in a { write(s); write(\"|\"); } return a.len(); }", 3, "a||b|"},
}

// stdlibLoader compiles programs that import the standard library through
// asm_load_run with the stdlib root.
type stdlibLoader struct {
	gcc    string
	runner []string
	mmc    string
	root   string
}

func newStdlibLoader(t *testing.T) *stdlibLoader {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	return &stdlibLoader{gcc: gcc, runner: runner, mmc: buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc"), root: root}
}

// emit compiles src for x86-64 and returns the assembly.
func (l *stdlibLoader) emit(t *testing.T, src string, args ...string) string {
	t.Helper()
	mainPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return string(runDriverFile(t, l.runner, l.mmc, mainPath, append([]string{l.root}, args...)...))
}

// emitAST is emit on the AST lowering, for a check that reads that lowering's
// own output.
func (l *stdlibLoader) emitAST(t *testing.T, src string) string {
	t.Helper()
	mainPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(l.runner, l.mmc, mainPath, l.root)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("compile on the AST lowering: %v", err)
	}
	return string(out)
}

// TestSelfHostAsmLoadRunStdlibX86_64 compiles each case with asm_load_run,
// links it and checks its exit code and stdout.
func TestSelfHostAsmLoadRunStdlibX86_64(t *testing.T) {
	l := newStdlibLoader(t)
	for _, tc := range asmLoadRunStdlibCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			asm := l.emit(t, withPrintInt(tc.src))
			run := runX86_64Bin(l.runner, buildBin(t, l.gcc, t.TempDir(), "prog", asm))
			out, _ := run.Output()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit %d, want %d\n--- source ---\n%s", code, tc.want, tc.src)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("stdout %q, want %q\n--- source ---\n%s", out, tc.stdout, tc.src)
			}
		})
	}
}

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// lexErrorCases each hold one lex error. Before #12685 the self-host's lexer
// stopped at it and the parser read the stop as the end of the file, so at top
// level `-check` accepted the program with everything after the error dropped,
// and inside a body the truncation surfaced as an unrelated E052.
var lexErrorCases = []struct{ name, src string }{
	{"hash-top-level", "function main(): i32 { return 3; }\n# stray\nfunction helper(): i32 { return 0; }\n"},
	{"hash-in-body", "function main(): i32 {\n  let x: i32 = 1;\n  # x;\n  return x;\n}\n"},
	{"dollar-top-level", "$ leading\nfunction main(): i32 { return 3; }\n"},
	{"dollar-in-body", "function main(): i32 {\n  let x: i32 = 1;\n  $ x;\n  return x;\n}\n"},
	{"backtick-top-level", "function main(): i32 { return 0; }\n`\n"},
	{"backtick-in-body", "function main(): i32 {\n  let s: i32 = `1`;\n  return s;\n}\n"},
	{"tilde-top-level", "~~\nfunction main(): i32 { return 0; }\n"},
	{"tilde-in-body", "function main(): i32 {\n  let x: i32 = ~~1;\n  return x;\n}\n"},
	{"backslash-top-level", "function main(): i32 { return 0; }\n\\\n"},
	{"backslash-in-body", "function main(): i32 {\n  \\ return 0;\n}\n"},
	{"section-sign-top-level", "§\nfunction main(): i32 { return 0; }\n"},
	{"section-sign-in-body", "function main(): i32 {\n  let x: i32 = 1 § 2;\n  return x;\n}\n"},
	{"shebang", "#!/usr/bin/env fern\nfunction main(): i32 { return 0; }\n"},
	{"unterminated-string", "function main(): i32 {\n  let s: string = \"abc;\n  return 0;\n}\n"},
	{"unterminated-string-at-eof", "function main(): i32 { return 0; }\nlet s: string = \"abc"},
	{"bad-escape", "function main(): i32 {\n  let s: string = \"a\\qb\";\n  return 0;\n}\nfunction other(): i32 { return 1; }\n"},
	{"bad-escape-top-level", "let s: string = \"a\\qb\";\nfunction main(): i32 { return 0; }\n"},
	{"bad-numeric-suffix", "function main(): i32 {\n  let x: i32 = 42i33;\n  return x;\n}\n"},
	{"integer-suffix-on-float", "function main(): i32 {\n  let x: f64 = 1.5i32;\n  return 0;\n}\n"},
	{"interpolant", "import \"std/i32\";\nfunction main(): i32 {\n  let x: i32 = 1;\n  print(f\"{x $ 1}\");\n  return 0;\n}\n"},
	{"latin-letter", "function main(): i32 { let café = 1; return 0; }\n"},
	{"greek-letter", "λ\n"},
	{"cjk-letter", "function main(): i32 {\n  return 字;\n}\n"},
	{"emoji", "function main(): i32 { return 😀; }\n"},
	{"non-ascii-digit", "١\n"},
	{"invalid-leading-byte", "\xff\n"},
	{"invalid-byte-in-string", "function main(): i32 { let s: string = \"abc\xff\"; return 0; }\n"},
	{"invalid-byte-in-comment", "// é\n// abc\xff\nfunction main(): i32 { return 0; }\n"},
	{"invalid-byte-after-multibyte", "// é\xff\nfunction main(): i32 { return 0; }\n"},
	{"invalid-before-token-error", "$\n// \xff\n"},
	{"invalid-overlong", "// \xc0\xaf\n"},
	{"invalid-surrogate", "// \xed\xa0\x80\n"},
	{"invalid-out-of-range", "// \xf4\x90\x80\x80\n"},
	{"invalid-truncated", "// \xe2\x82"},
	{"invalid-continuation", "// \x80\n"},
	{"invalid-five-byte-lead", "// \xf8\x80\x80\n"},
	{"invalid-ff-with-continuations", "// \xff\x80\x80\n"},
	{"invalid-four-byte-lead", "// \xf5\x80\x80\x80\n"},
}

// diagHeader is one diagnostic's header with the file path removed:
// `LINE:COL: error[CODE]: MESSAGE`.
var diagHeader = regexp.MustCompile(`(?m)^\S+?:(\d+:\d+: error(\[[A-Z]\d+\])?: .*)$`)

// TestSelfHostCheckReportsLexErrors is the differential for #12685: `-check`
// refuses a program holding a lex error with that error alone, and both front
// ends report it identically — position, code and message. Native's lexer
// fails before its parser starts, so a second diagnostic from the self-host
// would be a report about the truncated file rather than the source.
func TestSelfHostCheckReportsLexErrors(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	native := buildLangBinForInterp(t)
	for _, c := range lexErrorCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(p, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			want := onlyDiag(t, "native", exec.Command(native, "-check", p))
			if !strings.Contains(want, "error[P00") {
				t.Fatalf("native's lex error carries no P001/P002 code: %s", want)
			}
			if got := onlyDiag(t, "self-host", exec.Command(cli, "-check", p, root)); got != want {
				t.Errorf("self-host reported\n  %s\nnative reported\n  %s", got, want)
			}
		})
	}
}

// onlyDiag runs a `-check`, which must fail with exactly one diagnostic, and
// returns that diagnostic's header without the file path.
func onlyDiag(t *testing.T, who string, cmd *exec.Cmd) string {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("%s -check accepted a program holding a lex error:\n%s", who, out)
	}
	ms := diagHeader.FindAllStringSubmatch(string(out), -1)
	if len(ms) != 1 {
		t.Fatalf("%s reported %d diagnostics, want the lex error alone:\n%s", who, len(ms), out)
	}
	return ms[0][1]
}

func TestSelfHostSourceDecodingModes(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	native := buildLangBinForInterp(t)
	source := "// é\nfunction main(): i32 { let s: string = \"x\xff\"; return 0; }\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	want := onlyDiag(t, "oracle", exec.Command(native, "-check", path))
	for _, mode := range []string{"-check", "-interp", "-fmt"} {
		t.Run(mode+"-file", func(t *testing.T) {
			args := []string{mode, path}
			if mode != "-fmt" {
				args = append(args, root)
			}
			if got := onlyDiag(t, "self-host", exec.Command(cli, args...)); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
		t.Run(mode+"-stdin", func(t *testing.T) {
			cmd := exec.Command(cli, mode, "-")
			cmd.Stdin = strings.NewReader(source)
			if got := onlyDiag(t, "self-host", cmd); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command(cli, "-target", target, "-o", filepath.Join(t.TempDir(), "program"), path, root)
			if got := onlyDiag(t, "self-host", cmd); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

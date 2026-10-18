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

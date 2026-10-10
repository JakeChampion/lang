package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The action after `defer` or `errdefer` is a block, an expression or an
// assignment, as the native parser has it (#11602); `assert(...)` is an
// expression there, which the self-host parses to its desugared `if`. Any
// other statement is a P001 at its first token, from both front ends.
func TestSelfHostDeferActionGrammar(t *testing.T) {
	h := selfHostCLIForHost(t)
	const prog = "struct P { x: i32 }\nfunction f(): i32 { return 1; }\nfunction main(): i32 {\n  let s: i32 = 0;\n  loop {\n    %s %s\n    break;\n  }\n  return s;\n}\n"
	accepted := []string{
		"f();",
		"s = s + 1;",
		"s += 1;",
		"{ s = s + 1; }",
		"{ for i in 0..2 { s = s + i; } }",
		"assert(s >= 0);",
	}
	rejected := []struct{ action, token string }{
		{"for i in 0..2 { s = s + i; }", "for"},
		{"while (s < 3) { s = s + 1; }", "while"},
		{"loop { break; }", "loop"},
		{"let t: i32 = 1;", "let"},
		{"return 1;", "return"},
		{"break;", "break"},
		{"defer f();", "defer"},
		{"if (s == 0) { s = 1; }", "if"},
		{"match (s) { 0 => { s = 1; }, _ => { s = 2; } }", "match"},
	}
	dir := t.TempDir()
	check := func(bin, src string, extra ...string) (string, error) {
		out, err := exec.Command(bin, append([]string{"-check", src}, extra...)...).CombinedOutput()
		return string(out), err
	}
	for _, kw := range []string{"defer", "errdefer"} {
		for i, action := range accepted {
			src := filepath.Join(dir, fmt.Sprintf("%s_ok%d.fern", kw, i))
			if err := os.WriteFile(src, []byte(fmt.Sprintf(prog, kw, action)), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := check(h.cli, src, h.stdlib); err != nil {
				t.Errorf("self-host rejects `%s %s`: %v\n%s", kw, action, err, out)
			}
			if out, _ := check(h.native, src); strings.Contains(out, "error[P001]") {
				t.Errorf("native does not parse `%s %s`:\n%s", kw, action, out)
			}
		}
		for i, r := range rejected {
			src := filepath.Join(dir, fmt.Sprintf("%s_bad%d.fern", kw, i))
			if err := os.WriteFile(src, []byte(fmt.Sprintf(prog, kw, r.action)), 0o644); err != nil {
				t.Fatal(err)
			}
			col := 4 + len(kw) + 2
			want := fmt.Sprintf(":6:%d: error[P001]: in fn 'main': expected an expression, got %q", col, r.token)
			out, err := check(h.cli, src, h.stdlib)
			if err == nil || !strings.Contains(out, want) {
				t.Errorf("self-host on `%s %s`: want %q, got (%v)\n%s", kw, r.action, want, err, out)
			}
			if out, err := check(h.native, src); err == nil || !strings.Contains(out, "error[P001]") {
				t.Errorf("native on `%s %s`: want a P001, got (%v)\n%s", kw, r.action, err, out)
			}
		}
	}
}

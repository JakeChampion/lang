package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const recursionSrc = `function down(n: i32): i32 {
  if (n == 0) {
    return 0;
  }
  return down(n - 1) + 1;
}

function main(): i32 {
  print(down(%d).to_string());
  return 0;
}
`

// The limit of fern -interp. The Go oracle has its own, interp.MaxCallDepth,
// because one Fern call takes about 11 KB of its host stack.
const selfHostMaxCallDepth = 1000000

// Each interpreter stops a recursion at its limit with the cause line, the
// number of active calls and the call site, and with status 134. It does not
// end with a stack overflow of its host. A recursion below the limit runs.
func TestRecursionLimitInterpreters(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	write := func(depth int) string {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(fmt.Sprintf(recursionSrc, depth)), 0o644); err != nil {
			t.Fatal(err)
		}
		return src
	}
	for _, c := range []struct {
		name  string
		limit int
		run   func(src string) *exec.Cmd
	}{
		{"self-host", selfHostMaxCallDepth, func(src string) *exec.Cmd { return runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib) }},
		{"oracle", interp.MaxCallDepth, func(src string) *exec.Cmd { return exec.Command(oracle, "-interp", src) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			below := c.limit - 100
			out, errText, code := runInterpCLI(t, c.run(write(below)), "")
			if code != 0 || out != fmt.Sprintf("%d\n", below) {
				t.Errorf("depth %d: exit %d, stdout %q, stderr %q; want the depth printed", below, code, out, errText)
			}
			// main is the first active call, so the call of down that would
			// be number limit + 1 is the one that stops.
			_, errText, code = runInterpCLI(t, c.run(write(2*c.limit)), "")
			want := fmt.Sprintf("fern: recursion limit\n  %d calls are active, at line 5 column 14\n", c.limit)
			if code != 134 || errText != want {
				t.Errorf("depth %d: exit %d, stderr %q; want 134 and %q", 2*c.limit, code, errText, want)
			}
		})
	}
}

// A recursion that reaches the limit is an error of one line of a -repl
// session, and the session continues.
func TestSelfHostREPLRecursionLimit(t *testing.T) {
	in := strings.Join([]string{
		"function down(n: i32): i32 { if (n == 0) { return 0; } return down(n - 1) + 1; }",
		"down(2000000)",
		"1 + 2",
		"",
	}, "\n")
	want := strings.Join([]string{
		"fern REPL — Ctrl+D to exit",
		"> > error: fern: recursion limit",
		"  1000000 calls are active, at line 1 column 67",
		"> 3",
		"> ",
		"",
	}, "\n")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-repl", e2eharness.SelfHostStdlibRoot(t))
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != want {
		t.Errorf("%v\n--- got ---\n%s\n--- want ---\n%s", err, out, want)
	}
}

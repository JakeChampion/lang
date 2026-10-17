package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `pick` returns `scale`'s result as it is on one path and a parameter on the
// other, so the call is its only one and it is a tail call.
const ssaTailCallProg = `@noinline function scale(a: i32, b: i32): i32 { return a * 3 + b; }
@noinline function pick(a: i32): i32 {
  if (a > 5) {
    return scale(a, 1);
  }
  return a;
}
function main(): i32 {
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < 10) {
    total = total + pick(i);
    i = i + 1;
  }
  return total - 100;
}
`

// A function whose only call is a register call it returns as it is leaves
// through a jump to the callee's register entry and builds no frame, on both
// native targets, including debug builds.
func TestSelfHostSSATailCall(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaTailCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	jumps := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+jmp __fn_scale\.r$`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+b __fn_scale\.r$`),
	}
	frames := map[string]string{"x86-64-linux": "%rbp", "arm64-linux": "x29"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			body := tailCallBody(t, string(asm), "pick")
			if !jumps[target].MatchString(body) {
				t.Fatalf("pick does not jump to scale's register entry:\n%s", body)
			}
			if strings.Contains(body, frames[target]) || strings.Contains(body, "call") || strings.Contains(body, "bl ") {
				t.Fatalf("pick builds a frame or calls:\n%s", body)
			}
			// 0..5 return themselves and 6..9 return 3a+1: 15 + 94 - 100.
			if stderr, code := cli.exitOf(t, ssaTailCallProg, target); code != 9 {
				t.Fatalf("exit %d, want 9\n%s", code, stderr)
			}
		})
	}
	t.Run("debug keeps the tail call", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out.s")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-g", "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, cli.stdlib)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, msg)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		body := tailCallBody(t, string(asm), "pick")
		if strings.Contains(body, "call __fn_scale.r") || !strings.Contains(body, "jmp __fn_scale.r") {
			t.Fatalf("a -g build does not keep pick's tail call:\n%s", body)
		}
	})
}

// tailCallBody is the listing of __fn_<name> up to the next function label.
func tailCallBody(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, "\n__fn_"+name+":\n")
	if start < 0 {
		t.Fatalf("no __fn_%s in the listing", name)
	}
	body := asm[start+1:]
	if end := regexp.MustCompile(`\n__fn_\w+:\n`).FindStringIndex(body[1:]); end != nil {
		body = body[:end[0]+1]
	}
	return body
}

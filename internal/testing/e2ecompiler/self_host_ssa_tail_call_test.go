package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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

const ssaJoinedTailCallProg = `enum Cells { End, Cell(i32, Cells) }
@noinline function clear(want: i32, step: i32, xs: Cells): boolean {
  return match (xs) { End => true, Cell(q, rest) => want != q && want != q + step && clear(want, step + 1, rest) };
}
@noinline function any(want: i32, xs: Cells): boolean {
  return match (xs) { End => false, Cell(q, rest) => want == q || any(want, rest) };
}
@noinline function twice(a: i32): i32 { return a * 2; }
@noinline function pick(a: i32): i32 {
  return if (a > 5) { twice(a) } else if (a > 2) { twice(a + 1) } else { a };
}
function main(): i32 {
  let xs = Cell(1, Cell(2, Cell(3, End)));
  let n = 0;
  if (clear(9, 1, xs)) { n += 1; }
  if (!clear(2, 1, xs)) { n += 2; }
  if (any(3, xs)) { n += 4; }
  if (!any(7, xs)) { n += 8; }
  return n + pick(6) + pick(3) + pick(1);
}
`

// A call that is the last operand of && or ||, or the value of a branch of the
// returned expression, reaches the return through a join. It is a tail call.
func TestSelfHostSSATailCallThroughJoin(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaJoinedTailCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	jump := map[string]string{"x86-64-linux": "jmp", "arm64-linux": "b"}
	frames := map[string]string{"x86-64-linux": "%rbp", "arm64-linux": "x29"}
	tails := map[string]string{"clear": "clear", "any": "any", "pick": "twice"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for name, callee := range tails {
				body := tailCallBody(t, string(asm), name)
				want := regexp.MustCompile(`(?m)^\s+` + jump[target] + ` __fn_` + callee + `\.r$`)
				if !want.MatchString(body) {
					t.Fatalf("%s does not jump to the register entry of %s:\n%s", name, callee, body)
				}
				if strings.Contains(body, frames[target]) || strings.Contains(body, "call") || strings.Contains(body, "bl ") {
					t.Fatalf("%s builds a frame or calls:\n%s", name, body)
				}
			}
			if stderr, code := cli.exitOf(t, ssaJoinedTailCallProg, target); code != 36 {
				t.Fatalf("exit %d, want 36\n%s", code, stderr)
			}
		})
	}
}

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

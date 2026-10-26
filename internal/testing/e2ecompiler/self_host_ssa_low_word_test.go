package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An i32 sum read only as an index, and a u32 sum, are one 32-bit operation
// that zero-extends. A negative index still fails the bounds check.
func TestSelfHostSSALowWordIndex(t *testing.T) {
	const helpers = `@noinline function at(w: u8[], a: i32, b: i32): i32 {
  return w[a + b] as i32;
}

@noinline function put(w: i32[], a: i32, b: i32): i32[] {
  return w.with(a - b, 7);
}

@noinline function count(n: u32): u32 {
  let k: u32 = 0u32;
  while (k < n) {
    k = k + 1u32;
  }
  return k;
}

function main(): i32 {
  let w: u8[] = [10, 20, 30];
`
	cases := []struct {
		name, body string
		code       int
	}{
		{"in_range", "return at(w, 0 - 1, 3) + put([1, 2, 3], 1, 0 - 1)[2] + count(5u32) as i32;", 42},
		{"negative", "return at(w, 0 - 5, 3);", 134},
		{"overflow", "return at(w, 2147483647, 1);", 134},
		{"negative_store", "return put([1, 2, 3], 0 - 2147483647, 2)[0];", 134},
	}
	sext := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`\bmovslq\b`),
		"arm64-linux":  regexp.MustCompile(`\bsxtw\b`),
	}
	narrow := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`\b(?:leal|addl|subl)\b`),
		"arm64-linux":  regexp.MustCompile(`\b(?:add|sub) w\d+, w\d+`),
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			for _, c := range cases {
				program := helpers + "  " + c.body + "\n}\n"
				stderr, code := cli.exitOf(t, program, target)
				if code != c.code {
					t.Errorf("%s: exit %d, want %d\n%s", c.name, code, c.code, stderr)
				}
				if c.code == 134 && !strings.Contains(stderr, "array index out of range") {
					t.Errorf("%s: stderr lacks the bounds cause\n%s", c.name, stderr)
				}
			}
			src := filepath.Join(t.TempDir(), "low_word.fern")
			if err := os.WriteFile(src, []byte(helpers+"  "+cases[0].body+"\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			assembly, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, fn := range []string{"at", "put", "count"} {
				body := functionBody(string(assembly), "__fn_"+fn)
				if body == "" {
					t.Fatalf("no body for __fn_%s", fn)
				}
				if sext[target].MatchString(body) {
					t.Errorf("__fn_%s sign-extends:\n%s", fn, body)
				}
				if !narrow[target].MatchString(body) {
					t.Errorf("__fn_%s has no 32-bit add or sub:\n%s", fn, body)
				}
			}
		})
	}
}

// The text from a function's label to its first return.
func functionBody(assembly, label string) string {
	at := strings.Index(assembly, "\n"+label+":\n")
	if at < 0 {
		return ""
	}
	rest := assembly[at+1:]
	end := strings.Index(rest, "\n    ret\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

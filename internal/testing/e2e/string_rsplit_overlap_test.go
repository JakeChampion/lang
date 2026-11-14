package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type rsplitCase struct {
	s, sep string
	limit  int
	all    bool
}

func rsplitOverlapCases() []rsplitCase {
	pairs := [][2]string{
		{"aaa", "aa"}, {"ababa", "aba"}, {"aaaaa", "aa"},
		{"abababa", "aba"}, {"aaaaaa", "aaaa"}, {"aaaaaaa", "aaa"},
		{"ééé", "éé"}, {"é🐸é🐸é", "é🐸é"}, {"a.b.c", "."},
		{"a,,b", ","}, {"aa", "aa"}, {"", ","}, {"hello", "x"},
		{"abc", "abcdef"}, {"abc", ""}, {"é🐸界", ""}, {"", ""},
		{"a\x00a\x00a", "\x00a"}, {"line\nline\n", "line\n"},
		{strings.Repeat("a", 512) + "b", strings.Repeat("a", 32) + "b"},
		{strings.Repeat("a", 512), strings.Repeat("a", 32) + "b"},
	}
	var cases []rsplitCase
	for _, p := range pairs {
		for _, n := range []int{-2147483648, -1, 0, 1, 2, 3, 8, 2147483647} {
			cases = append(cases, rsplitCase{p[0], p[1], n, false})
		}
		cases = append(cases, rsplitCase{p[0], p[1], 2147483647, true})
	}
	return cases
}

func rsplitReference(s, sep string, n int) []string {
	if n <= 0 {
		return nil
	}
	var out []string
	if sep == "" {
		runes := []rune(s)
		for len(runes) > 0 && len(out) < n-1 {
			out = append(out, string(runes[len(runes)-1]))
			runes = runes[:len(runes)-1]
		}
		if len(runes) > 0 {
			out = append(out, string(runes))
		}
		return out
	}
	for len(out) < n-1 {
		i := strings.LastIndex(s, sep)
		if i < 0 {
			break
		}
		out = append(out, s[i+len(sep):])
		s = s[:i]
	}
	return append(out, s)
}

func rsplitOverlapProgram(cases []rsplitCase) (string, string) {
	var source, want strings.Builder
	source.WriteString(`import "std/string";
@noinline
function split(s: string, sep: string, n: i32): string[] {
  let input = "!" + s;
  return input.remove_prefix("!").rsplitn(sep, n);
}
@noinline
function split_all(s: string, sep: string): string[] {
  let input = "!" + s;
  return input.remove_prefix("!").rsplit(sep);
}
@noinline
function churn(n: i32): string { return "z".repeat(n); }
@noinline
function report(parts: string[], n: i32): i32 {
  let junk = churn(n);
  let next = churn(n + 1);
  print(parts.len());
  for part in parts { print(part.len()); write(part); write("\n"); }
  if (junk.len() != n || next.len() != n + 1 || next[0] as i32 != 122) { print("bad churn"); }
  return 0;
}
function main(): i32 {
`)
	for _, c := range cases {
		if c.all {
			fmt.Fprintf(&source, "report(split_all(%s, %s), %d);\n", strconv.Quote(c.s), strconv.Quote(c.sep), len(c.s))
		} else {
			fmt.Fprintf(&source, "report(split(%s, %s, %d), %d);\n", strconv.Quote(c.s), strconv.Quote(c.sep), c.limit, len(c.s))
		}
		parts := rsplitReference(c.s, c.sep, c.limit)
		fmt.Fprintf(&want, "%d\n", len(parts))
		for _, part := range parts {
			fmt.Fprintf(&want, "%d\n%s\n", len(part), part)
		}
	}
	source.WriteString("return 0;\n}\n")
	return source.String(), want.String()
}

func TestStringRsplitOverlap(t *testing.T) {
	cases := rsplitOverlapCases()
	if len(cases) != 189 {
		t.Fatalf("got %d cases, want 189", len(cases))
	}
	source, want := rsplitOverlapProgram(cases)
	check := func(t *testing.T, output string) {
		t.Helper()
		if output != want {
			i := 0
			for i < len(output) && i < len(want) && output[i] == want[i] {
				i++
			}
			t.Fatalf("split output differs at byte %d: got %q, want %q", i, output[i:min(i+80, len(output))], want[i:min(i+80, len(want))])
		}
	}
	t.Run("go", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rsplit.fern")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		code, output, stderr := runLangInterp(t, buildLangBinForInterp(t), path)
		if code != 0 {
			t.Fatalf("Go exit %d: %s", code, stderr)
		}
		check(t, output)
	})
	t.Run("x86-64", func(t *testing.T) {
		output, code := compileAndRunX86_64(t, source)
		if code != 0 {
			t.Fatalf("x86-64 exit %d: %s", code, output)
		}
		check(t, output)
	})
	t.Run("arm64", func(t *testing.T) {
		output, code := compileAndRunArm64(t, source)
		if code != 0 {
			t.Fatalf("ARM64 exit %d: %s", code, output)
		}
		check(t, output)
	})
	t.Run("wasm", func(t *testing.T) { check(t, compileAndRunWasmCapture(t, source)) })
}

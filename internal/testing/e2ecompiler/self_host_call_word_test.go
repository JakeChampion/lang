package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// walk calls step in a block that is cold, and step returns three words.
// Five caller registers hold values live across that call.
const callWordWalk = `struct Cur { start: i32, ln: i32, cand: i32 }

@noinline
function step(t: string, c: Cur, end: i32, term: i32): Cur {
  let at = __memchr(t, term, c.start);
  if (at < 0 || at > end) {
    return Cur { start: end, ln: c.ln, cand: c.cand };
  }
  return Cur { start: at + 1, ln: c.ln + 1, cand: at };
}

@noinline
function walk(t: string, end: i32, term: i32, a: i32, b: i32, e: i32, f: i32, g: i32): i32 {
  let c = Cur { start: 0, ln: 0, cand: -1 };
  let n = 0;
  let m = 0;
  let k = 0;
  while (c.start < end) {
    if (a > 0 && n < b) {
      c = step(t, c, end, term);
    }
    n += a + b;
    m += e * c.cand;
    k += f + g + c.ln;
    c = Cur { ...c, start: c.start + 1 };
  }
  return n + c.ln + m + k;
}
`

var callWordRows = []leakRow{
	{"three_words", callWordWalk + `function main(): i32 {
  let t = "one line\ntwo lines\nthree lines and some more\nfour\n";
  let x = t.len();
  return walk(t, x, 10, x % 3 + 1, x % 5 + 40, x % 11, x % 13, x % 17) % 256;
}
`, false},
	{"string_word", `struct Got { name: string, at: i32, n: i32 }

@noinline
function next(words: string[], i: i32, n: i32): Got {
  if (i % 3 == 0) {
    return Got { name: words[i % words.len()] + "!", at: i, n: n + 1 };
  }
  return Got { name: words[i % words.len()], at: i * 2, n: n };
}

@noinline
function scan(words: string[], a: i32, b: i32, c: i32, d: i32): i32 {
  let g = Got { name: "", at: 0, n: 0 };
  let total = 0;
  let i = 0;
  while (i < 40) {
    if (a > 0 && i % b == 0) {
      g = next(words, i, g.n);
    }
    total += g.name.len() * c + g.at + d + g.n;
    i += 1;
  }
  return total;
}

function main(): i32 {
  let words = ["ab", "cde", "f", "ghij"];
  return scan(words, 1, 2, 3, 4) % 256;
}
`, true},
}

func TestSelfHostCallWordX86_64(t *testing.T) {
	runLeakRowsX86_64(t, callWordRows)
}

func TestSelfHostCallWordArm64(t *testing.T) {
	runLeakRowsArm64(t, callWordRows)
}

func TestSelfHostCallWordWasm(t *testing.T) {
	runLeakRowsWasm(t, callWordRows)
}

// A call_word only copies a result word out of its register, so the caller
// registers saved around the cold call are not saved again after it.
func TestSelfHostCallWordSavesNothing(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "walk.fern")
	prog := callWordWalk + "function main(): i32 { let t = args()[1]; return walk(t, t.len(), 10, 1, 9, 2, 3, 4) % 256; }\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		target, call string
		save         *regexp.Regexp
	}{
		{"x86-64-linux", "call __fn_step.r", regexp.MustCompile(`movq %r(8|9|10), -[0-9]+\(%rbp\)`)},
		{"arm64-linux", "bl __fn_step.r", regexp.MustCompile(`str x[0-9]+, \[sp`)},
	} {
		t.Run(c.target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, src, c.target))
			if err != nil {
				t.Fatal(err)
			}
			body := functionBody(string(data), "__fn_walk")
			at := strings.Index(body, c.call+"\n")
			if at < 0 {
				t.Fatalf("no %q in __fn_walk on %s\n%s", c.call, c.target, body)
			}
			after := blockAfter(body[at+len(c.call)+1:])
			if saves := c.save.FindAllString(after, -1); len(saves) != 0 {
				t.Errorf("%d saves after %s on %s, want 0: a call_word calls nothing\n%s", len(saves), c.call, c.target, after)
			}
		})
	}
}

// blockAfter is the code up to the next label, branch or call.
func blockAfter(code string) string {
	var out []string
	for _, line := range strings.Split(code, "\n") {
		op := strings.Fields(line)
		if strings.HasSuffix(line, ":") || len(op) > 0 && (strings.HasPrefix(op[0], "j") || strings.HasPrefix(op[0], "b") || op[0] == "call") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

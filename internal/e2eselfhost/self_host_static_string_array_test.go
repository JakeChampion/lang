package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A string literal is a static box, so an array of string literals, or a
// record whose string fields are literals, is one static box too:
// `http_status_text`'s phrase table costs nothing per call. The text rides
// the packing in hex, so a literal holding a comma, a brace, a bar or a high
// byte reads back intact, as does the empty string. An append to such an
// array grows a box of its own and leaves the constant as it was. x86-64's
// leakcheck stays balanced.
func TestSelfHostStaticStringArray(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "static_strings.fern")
	if err := os.WriteFile(src, []byte(staticStringArraySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			env := []string{"FERN_STRICT_IR=1"}
			if target == "x86-64-linux" {
				env = append(env, "FERN_LEAKCHECK=1")
			}
			out, code := cli.exitOfFile(t, src, target, nil, env...)
			if code != 0 {
				t.Fatalf("exit %d, want 0 (20 means a constant allocated):\n%s", code, out)
			}
			if target == "x86-64-linux" {
				e2eharness.CheckLeakcheckBalanced(t, out)
			}
		})
	}
}

const staticStringArraySrc = `struct Named { tag: i32, name: string, alts: string[] }
struct P { a: i32, b: i32 }
function phrase(i: i32): string {
  let phrases: string[] = ["OK", "a,b", "{x|y}", "", "\xc3\xa9t\xc3\xa9"];
  return phrases[i];
}
function named(): Named { return Named { tag: 3, name: "n,1", alts: ["p", "q}"] }; }
function pts(): P[] { return [P { a: 1, b: 2 }, P { a: 3, b: 4 }]; }
function main(): i32 {
  let before: i64 = __heap_alloc_count();
  let n: i32 = 0;
  let i: i32 = 0;
  while (i < 100) {
    n = n + phrase(i % 5).len() + named().alts[1].len() + pts()[1].b;
    i = i + 1;
  }
  if (__heap_alloc_count() - before != (0 as i64)) { return 20; }
  if (n != 900) { return 10; }
  if (phrase(1) != "a,b" || phrase(2) != "{x|y}" || phrase(3) != "" || phrase(4) != "\xc3\xa9t\xc3\xa9") { return 11; }
  let nm: Named = named();
  if (nm.name != "n,1" || nm.alts[0] != "p" || nm.alts[1] != "q}" || nm.tag != 3) { return 12; }
  let more: string[] = nm.alts.append("r");
  if (more.len() != 3 || named().alts.len() != 2 || more[2] != "r") { return 13; }
  return 0;
}
`

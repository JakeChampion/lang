package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The first arm of weigh binds c and d of the outer node and then tests the
// tag of the inner node. The second arm reads c and does not read d.
const sunkLoadProg = `enum Tree { Leaf, Node(Tree, i64, i64, Tree) }

@noinline function weigh(t: Tree): i64 {
  match (t) {
    Node(Node(_, a, b, _), c, d, _) => {
      return a + b * 10i64 + c * 100i64 + d * 1000i64;
    },
    Node(Leaf(), c, _, _) => {
      return c;
    },
    Leaf => {
      return 0i64 - 1i64;
    },
  }
}

function main(): i32 {
  let inner = Node(Leaf, 1i64, 2i64, Leaf);
  let outer = Node(inner, 3i64, 4i64, Leaf);
  if (weigh(outer) != 4321i64) {
    return 1;
  }
  if (weigh(Node(Leaf, 5i64, 6i64, Leaf)) != 5i64) {
    return 2;
  }
  if (weigh(Leaf) != 0i64 - 1i64) {
    return 3;
  }
  return 0;
}
`

// A load that only one side of a branch reads moves to that side. In weigh
// the load of d is after the test of the inner tag, so the second arm does
// not run it. The load of c stays above the test, because each side reads c.
func TestSelfHostLoadMovesToItsReader(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(sunkLoadProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(asm), "weigh")
	// Leaf has no field, so a test of the tag is a compare with its static box.
	tests := regexp.MustCompile(`cmpl \$\d+, -4\(%r\w+\)|leaq \.Kv\d+\(%rip\)`).FindAllStringIndex(body, -1)
	if len(tests) < 2 {
		t.Fatalf("weigh has %d tag tests, want 2 or more:\n%s", len(tests), body)
	}
	inner := tests[1][0]
	if d := strings.Index(body, "movq 16(%rax)"); d < inner {
		t.Errorf("the load of d is at %d, before the test of the inner tag at %d:\n%s", d, inner, body)
	}
	if c := strings.Index(body, "movq 8(%rax)"); c < 0 || c > inner {
		t.Errorf("the load of c is at %d, not before the test of the inner tag at %d:\n%s", c, inner, body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, env := range []string{"", "FERN_LEAKCHECK=1"} {
			if stderr, code := cli.exitOf(t, sunkLoadProg, target, env); code != 0 {
				t.Errorf("%s %s: exit %d, want 0\n%s", target, env, code, stderr)
			}
		}
	}
}

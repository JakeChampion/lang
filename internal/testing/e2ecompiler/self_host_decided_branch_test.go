package e2ecompiler

import (
	"regexp"
	"testing"
)

const decidedBranchShapeSrc = `enum Color { Red, Black }
enum Shade { Light, Mid, Dark }
enum Tree { Node(Color, Tree, i32, Tree), Leaf }
enum Swatch { Patch(Shade, i32), Blank }

@noinline
function kind(t: Tree): i32 {
  match (t) {
    Node(Red(), l, k, r) => {
      return 100 + k;
    },
    Node(Black(), l, k, r) => {
      return 200 + k;
    },
    Leaf => {
      return 300;
    }
  }
}

@noinline
function weight(s: Swatch): i32 {
  match (s) {
    Patch(Light(), n) => {
      return n;
    },
    Patch(Mid(), n) => {
      return 10 * n;
    },
    Patch(Dark(), n) => {
      return 100 * n;
    },
    Blank => {
      return 0 - 1;
    }
  }
}

function main(): i32 {
  let t: Tree = Node(Black, Node(Red, Leaf, 1, Leaf), 2, Leaf);
  let bad: i32 = 0;
  if (kind(t) != 202 || kind(Node(Red, Leaf, 4, Leaf)) != 104 || kind(Leaf) != 300) {
    bad = bad + 1;
  }
  if (weight(Patch(Light, 3)) + weight(Patch(Mid, 3)) + weight(Patch(Dark, 3)) + weight(Blank) != 332) {
    bad = bad + 2;
  }
  return bad;
}`

var shapeLabelRe = regexp.MustCompile(`(?m)^(\.S\d+): \.ascii "([^"]*)"`)
var shapeTestRe = regexp.MustCompile(`cmpq \$(\.S\d+), `)

func variantTests(asm, body string) map[string]int {
	names := map[string]string{}
	for _, m := range shapeLabelRe.FindAllStringSubmatch(asm, -1) {
		names[m[1]] = m[2]
	}
	out := map[string]int{}
	for _, m := range shapeTestRe.FindAllStringSubmatch(body, -1) {
		out[names[m[1]]]++
	}
	return out
}

func TestSelfHostDecidedBranchShapeX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, decidedBranchShapeSrc)
	want := map[string]map[string]int{
		"kind":   {"Tree$Node": 1, "Color$Red": 1},
		"weight": {"Swatch$Patch": 1, "Shade$Light": 1, "Shade$Mid": 1},
	}
	for fn, w := range want {
		got := variantTests(asm, fnText(t, asm, fn))
		for name, n := range w {
			if got[name] != n {
				t.Errorf("%s tests %s %d times, want %d: %v", fn, name, got[name], n, got)
			}
		}
		for name, n := range got {
			if w[name] == 0 && n > 0 {
				t.Errorf("%s tests %s, which the path to it decided: %v", fn, name, got)
			}
		}
	}
	if code := run(t, "decided_branch", asm); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}

package e2ecompiler

import (
	"regexp"
	"strconv"
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

var shapeTestRe = regexp.MustCompile(`cmp[lq] \$(\d+), `)

// The test of a variant with no field compares a value with the address of
// its static box, whose label holds its shape.
var staticTestRe = regexp.MustCompile(`leaq \.Kv(\d+)\(%rip\)`)

// A narrow enum field shares a word with its neighbours, so a match tests its index with a byte compare.
var indexTestRe = regexp.MustCompile(`cmpb \$(\d+), `)

func variantTests(body string, index []string) map[string]int {
	// Shapes follow the declarations in decidedBranchShapeSrc. Color and i32
	// share a word of Node, and Shade and i32 share the word of Patch.
	shapes := []struct {
		name  string
		words int
	}{
		{"Color$Red", 1}, {"Color$Black", 1},
		{"Shade$Light", 1}, {"Shade$Mid", 1}, {"Shade$Dark", 1},
		{"Tree$Node", 3}, {"Tree$Leaf", 1},
		{"Swatch$Patch", 1}, {"Swatch$Blank", 1},
	}
	names := map[int]string{}
	for i, shape := range shapes {
		names[(i+1)<<16|shape.words] = shape.name
	}
	out := map[string]int{}
	matches := append(shapeTestRe.FindAllStringSubmatch(body, -1), staticTestRe.FindAllStringSubmatch(body, -1)...)
	for _, m := range matches {
		tag, _ := strconv.Atoi(m[1])
		if name, ok := names[tag]; ok {
			out[name]++
		} else if tag >= 1<<16 {
			out["tag "+m[1]]++
		}
	}
	for _, m := range indexTestRe.FindAllStringSubmatch(body, -1) {
		i, _ := strconv.Atoi(m[1])
		name := "index " + m[1]
		if i < len(index) {
			name = index[i]
		}
		out[name]++
	}
	return out
}

func TestSelfHostDecidedBranchShapeX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, decidedBranchShapeSrc)
	// Leaf and Blank have no field, so the test of Node and of Patch is the
	// compare with their static box.
	want := map[string]map[string]int{
		"kind":   {"Tree$Leaf": 1, "Color$Red": 1},
		"weight": {"Swatch$Blank": 1, "Shade$Light": 1, "Shade$Mid": 1},
	}
	index := map[string][]string{
		"kind":   {"Color$Red", "Color$Black"},
		"weight": {"Shade$Light", "Shade$Mid", "Shade$Dark"},
	}
	for fn, w := range want {
		got := variantTests(fnText(t, asm, fn), index[fn])
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

package checker

import (
	"strings"
	"testing"
)

const nestedExhaustiveDecls = `enum Color { Red, Black }
enum Shade { Light, Mid, Dark }
enum Tree { Node(Color, Tree, i32, Tree), Leaf }
enum Swatch { Patch(Shade, boolean), Blank }
enum Pair { Two((Color, Color)), Zero }
struct Lamp { on: boolean, level: i32 }
struct Pair2 { x: boolean, y: boolean }
`

// A variant is covered when no value of its payloads escapes every
// unguarded arm, whichever payloads the arms' sub-patterns test and however
// deeply they nest (#12257).
func TestNestedPatternExhaustiveness(t *testing.T) {
	covered := map[string]string{
		"sub-patterns in different payloads": `function f(t: Tree): i32 {
  match (t) {
    Node(Red(), _, k, _) => { return 2; },
    Node(Black(), Node(Red(), _, _, _), _, _) => { return 3; },
    Node(Black(), _, _, _) => { return 4; },
    Leaf => { return 5; }
  }
}`,
		"a variant ruled in by its siblings": `function f(s: Swatch): i32 {
  match (s) {
    Patch(Light(), _) => { return 1; },
    Patch(Mid(), _) => { return 2; },
    Patch(Dark(), _) => { return 3; },
    Blank => { return 4; }
  }
}`,
		"boolean literals together": `function f(s: Swatch): i32 {
  match (s) {
    Patch(_, true) => { return 1; },
    Patch(_, false) => { return 2; },
    Blank => { return 4; }
  }
}`,
		"a tuple payload split per element": `function f(p: Pair): i32 {
  match (p) {
    Two((Red(), _)) => { return 1; },
    Two((Black(), Red())) => { return 2; },
    Two((Black(), Black())) => { return 3; },
    Zero => { return 4; }
  }
}`,
		"a generic payload's variants": `function f(o: Option[Color]): i32 {
  match (o) {
    Some(Red()) => { return 1; },
    Some(Black()) => { return 2; },
    None => { return 3; }
  }
}`,
		"a tuple scrutinee split per element": `function f(a: Color, b: Color): i32 {
  match ((a, b)) {
    (Red(), _) => { return 1; },
    (Black(), Red()) => { return 2; },
    (Black(), Black()) => { return 3; }
  }
}`,
		"a struct field's boolean literals": `function f(l: Lamp): i32 {
  match (l) {
    Lamp { on: true, level } => { return level; },
    Lamp { on: false, level } => { return 0; }
  }
}`,
		"struct fields written in different orders": `function f(q: Pair2): i32 {
  match (q) {
    Pair2 { x: true, y: true } => { return 1; },
    Pair2 { x: true, y: false } => { return 2; },
    Pair2 { y: false, x: false } => { return 3; },
    Pair2 { y: true, x: false } => { return 4; }
  }
}`,
		"a field one arm leaves out": `function f(q: Pair2): i32 {
  match (q) {
    Pair2 { y: true, .. } => { return 1; },
    Pair2 { x: true, y: false } => { return 2; },
    Pair2 { y: false, x: false } => { return 3; }
  }
}`,
	}
	for name, src := range covered {
		t.Run(name, func(t *testing.T) {
			if got := checkSrc(t, nestedExhaustiveDecls+src); got != "" {
				t.Errorf("want no diagnostic, got %s", got)
			}
		})
	}
	missing := map[string]string{
		"a payload variant no arm names": `function f(t: Tree): i32 {
  match (t) {
    Node(Red(), _, k, _) => { return 2; },
    Leaf => { return 5; }
  }
}`,
		"a case a deeper sub-pattern leaves": `function f(t: Tree): i32 {
  match (t) {
    Node(Red(), _, k, _) => { return 2; },
    Node(Black(), Node(Red(), _, _, _), _, _) => { return 3; },
    Leaf => { return 5; }
  }
}`,
		"a guarded arm covers nothing": `function f(s: Swatch): i32 {
  match (s) {
    Patch(Light(), _) => { return 1; },
    Patch(Mid(), _) => { return 2; },
    Patch(Dark(), b) when b => { return 3; },
    Blank => { return 4; }
  }
}`,
		"one boolean literal": `function f(s: Swatch): i32 {
  match (s) {
    Patch(_, true) => { return 1; },
    Blank => { return 4; }
  }
}`,
		"a tuple element left open": `function f(p: Pair): i32 {
  match (p) {
    Two((Red(), _)) => { return 1; },
    Two((Black(), Red())) => { return 2; },
    Zero => { return 4; }
  }
}`,
		"a tuple element no arm finishes": `function f(a: Color, b: Color): i32 {
  match ((a, b)) {
    (Red(), _) => { return 1; },
    (Black(), Red()) => { return 2; }
  }
}`,
		"one struct field literal": `function f(l: Lamp): i32 {
  match (l) {
    Lamp { on: true, level } => { return level; }
  }
}`,
	}
	for name, src := range missing {
		t.Run(name, func(t *testing.T) {
			got := checkSrc(t, nestedExhaustiveDecls+src)
			if !strings.Contains(got, "not exhaustive") && !strings.Contains(got, "does not cover variant") {
				t.Errorf("want E030, got %q", got)
			}
		})
	}
}

package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Color and Fault have no variant with a field. The program keeps their
// values in an array, a map, an Option, a record, a tuple and a closure,
// uses one as a map key, and passes one as a dyn value.
const immediateEnumProg = `import "core/error";
import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Color { Red, Green, Blue }

enum Fault { Lost, Late }

struct Pixel { c: Color, n: i32 }

impl error.Error for Fault {
  function message(self: Fault): string {
    return match (self) { Lost => "lost", Late => "late" };
  }
}

@noinline function pick(n: i32): Color {
  if (n % 3 == 0) { return Red; }
  if (n % 3 == 1) { return Green; }
  return Blue;
}

@noinline function weight(c: Color): i32 {
  match (c) {
    Red => { return 1; },
    Green => { return 10; },
    Blue => { return 100; },
  }
}

@noinline function show(d: dyn error.Error): string {
  return d.message();
}

@noinline function fault(n: i32): Fault {
  if (n > 0) { return Late; }
  return Lost;
}

@noinline function maybe(n: i32): Option[Color] {
  if (n > 1) { return Some(pick(n)); }
  return None;
}

function main(): i32 {
  let cs: Color[] = [];
  for i in 0..7 { cs = cs.append(pick(i)); }
  let total = 0;
  for c in cs { total += weight(c); }
  if (total != 223) { return 1; }
  let m: Map[string, Color] = map_new(4);
  m = m.insert("a", Red);
  m = m.insert("b", pick(2));
  match (m.get("b")) { Some(c) => { if (weight(c) != 100) { return 2; } }, None => { return 3; } }
  let k: Map[Color, i32] = map_new(4);
  k = k.insert(Green, 5);
  k = k.insert(pick(2), 6);
  k = k.insert(pick(1), 7);
  match (k.get(Green)) { Some(v) => { if (v != 7) { return 4; } }, None => { return 5; } }
  if (show(fault(1)) != "late" || show(Lost) != "lost") { return 6; }
  let p = Pixel { c: Green, n: 3 };
  let q = Pixel { c: pick(4), n: 4 };
  if (!(p.c == q.c) || p.c == pick(2)) { return 7; }
  match (maybe(2)) { Some(c) => { if (weight(c) != 100) { return 8; } }, None => { return 9; } }
  match (maybe(0)) { Some(_) => { return 10; }, None => {} }
  let f = (x: i32): i32 => weight(if (x > 0) { q.c } else { pick(2) });
  if (f(1) != 10 || f(0) != 100) { return 11; }
  let pair: (Color, Color) = (Red, pick(1));
  if (weight(pair.0) + weight(pair.1) != 11) { return 12; }
  return 0;
}
`

// A value of an enum whose variants hold no field is the index of its
// variant. A match compares the value with a constant and reads no box. The
// widening to a dyn value makes the one box.
func TestSelfHostFieldlessEnumIsItsIndex(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(immediateEnumProg), 0o644); err != nil {
		t.Fatal(err)
	}
	compare := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`cmpq \$1, %r\w+`),
		"arm64-linux":  regexp.MustCompile(`cmp x\d+, #1\b`),
	}
	tagLoad := map[string]string{"x86-64-linux": "-4(%r", "arm64-linux": "#-4]"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(asm)
			for _, name := range []string{"weight", "pick"} {
				if body := tailCallBody(t, text, name); strings.Contains(body, tagLoad[target]) {
					t.Errorf("%s reads the tag of a box:\n%s", name, body)
				}
			}
			if body := tailCallBody(t, text, "weight"); !compare[target].MatchString(body) {
				t.Errorf("weight has no compare with the index of Green:\n%s", body)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, env := range []string{"", "FERN_LEAKCHECK=1"} {
			if stderr, code := cli.exitOf(t, immediateEnumProg, target, env); code != 0 {
				t.Errorf("%s %s: exit %d, want 0\n%s", target, env, code, stderr)
			}
		}
	}
}

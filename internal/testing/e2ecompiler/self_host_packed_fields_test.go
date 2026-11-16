package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Node has a colour, a key of 32 bits and a boolean, and Mixed has a byte, a
// boolean, a colour and a u32. The program reads each field, changes one of
// them, joins the fields of two boxes, and keeps a box that it also changes.
// bump and late write a colour in an arm that tested the colour of the node:
// the colour of the arm, another colour, and a colour after the inner match.
const packedFieldsProg = `enum Color { Red, Black }

enum Tree { Leaf, Node(Color, Tree, i32, boolean, Tree) }

enum Wide { Plain(i64), Mixed(u8, i64, boolean, u32, Color) }

@noinline function mk(c: Color, k: i32, v: boolean): Tree {
  return Node(c, Leaf, k, v, Leaf);
}

@noinline function key(t: Tree): i32 {
  match (t) {
    Node(_, _, k, _, _) => {
      return k;
    },
    Leaf => {
      return 0;
    },
  }
}

@noinline function code(t: Tree): i32 {
  match (t) {
    Node(c, l, k, v, r) => {
      let colour = match (c) { Red => 100, Black => 200 };
      if (v) {
        return colour + k + 50 + code(l) + code(r);
      }
      return colour + k + code(l) + code(r);
    },
    Leaf => {
      return 0;
    },
  }
}

@noinline function blacken(t: Tree): Tree {
  return match (t) { Node(_, l, k, v, r) => Node(Black, l, k, v, r), _ => t };
}

@noinline function rekey(t: Tree, n: i32): Tree {
  return match (t) { Node(c, l, _, v, r) => Node(c, l, n, v, r), _ => t };
}

@noinline function graft(a: Tree, b: Tree): Tree {
  match (a) {
    Node(c, _, k, _, _) => {
      match (b) {
        Node(_, l, _, v, r) => {
          return Node(c, l, k, v, r);
        },
        Leaf => {
          return Leaf;
        },
      }
    },
    Leaf => {
      return b;
    },
  }
}

@noinline function bump(t: Tree): Tree {
  match (t) {
    Node(Red(), l, k, v, r) => {
      return Node(Red, l, k + 1, v, r);
    },
    Node(Black(), l, k, v, r) => {
      return Node(Red, l, k, v, r);
    },
    Leaf => {
      return t;
    },
  }
}

@noinline function late(t: Tree): Tree {
  match (t) {
    Node(c, l, k, v, r) => {
      let m = 0;
      match (c) {
        Red => {
          m = 1;
        },
        Black => {
          m = 2;
        },
      }
      return Node(Red, l, k + m, v, r);
    },
    Leaf => {
      return t;
    },
  }
}

@noinline function mixed(a: u8, b: i64, c: boolean, d: u32, e: Color): Wide {
  return Mixed(a, b, c, d, e);
}

@noinline function sum(w: Wide): i64 {
  match (w) {
    Mixed(a, b, c, d, e) => {
      let colour = match (e) { Red => 1i64, Black => 2i64 };
      let flag = if (c) { 10i64 } else { 20i64 };
      return (a as i64) + b + flag + (d as i64) + colour;
    },
    Plain(n) => {
      return n;
    },
  }
}

function main(): i32 {
  if (code(mk(Red, 7, true)) != 157 || code(mk(Black, -3, false)) != 197) {
    return 1;
  }
  if (key(mk(Red, -2147483648, false)) != -2147483648 || key(mk(Black, 2147483647, true)) != 2147483647) {
    return 2;
  }
  if (code(blacken(mk(Red, 7, true))) != 257 || code(blacken(mk(Red, -9, false))) != 191) {
    return 3;
  }
  if (code(rekey(mk(Black, 7, true), -40)) != 210 || code(rekey(mk(Red, 7, false), 1)) != 101) {
    return 4;
  }
  if (code(graft(mk(Black, 5, false), mk(Red, 900, true))) != 255) {
    return 5;
  }
  let kept = mk(Red, 11, true);
  let shared = blacken(kept);
  if (code(kept) != 161 || code(shared) != 261) {
    return 6;
  }
  if (sum(mixed(255u8, -5i64, true, 4294967295u32, Black)) != 4294967557i64) {
    return 7;
  }
  if (sum(mixed(0u8, 5i64, false, 0u32, Red)) != 26i64 || sum(Plain(-4i64)) != -4i64) {
    return 8;
  }
  let constant = Node(Black, Leaf, 12, true, Leaf);
  if (code(constant) != 262) {
    return 9;
  }
  if (code(bump(mk(Red, 7, true))) != 158 || code(bump(mk(Black, 7, false))) != 107) {
    return 10;
  }
  if (code(late(mk(Black, 7, true))) != 159 || code(late(mk(Red, 7, false))) != 108) {
    return 11;
  }
  let held = mk(Black, 3, true);
  let red = bump(held);
  if (code(held) != 253 || code(red) != 153) {
    return 12;
  }
  return 0;
}
`

// On a native target the narrow fields of a variant share one word of its
// box. Node has three words and Mixed has two. A read of the key is a load of
// 32 bits with the sign. The wasm target keeps one word for each field.
func TestSelfHostNarrowFieldsShareAWord(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(packedFieldsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	words := map[string][2]string{
		"x86-64-linux": {"call __fern_rec_box_3\n", "call __fern_rec_box_2\n"},
		"arm64-linux":  {"bl __fern_rec_box_3\n", "bl __fern_rec_box_2\n"},
	}
	keyLoad := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movslq 4\(%r\w+\), %r\w+`),
		"arm64-linux":  regexp.MustCompile(`ldrsw x\d+, \[x\d+, #4\]`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(asm)
			if body := tailCallBody(t, text, "mk"); !strings.Contains(body, words[target][0]) {
				t.Errorf("mk does not ask for a box of three words:\n%s", body)
			}
			if body := tailCallBody(t, text, "mixed"); !strings.Contains(body, words[target][1]) {
				t.Errorf("mixed does not ask for a box of two words:\n%s", body)
			}
			if body := tailCallBody(t, text, "key"); !keyLoad[target].MatchString(body) {
				t.Errorf("key does not load the key as 32 bits with the sign:\n%s", body)
			}
			for _, env := range []string{"", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				if stderr, code := cli.exitOf(t, packedFieldsProg, target, env); code != 0 {
					t.Errorf("%s: exit %d, want 0\n%s", env, code, stderr)
				}
			}
		})
	}
	for _, env := range []string{"", "FERN_LEAKCHECK=1"} {
		if stderr, code := cli.exitOf(t, packedFieldsProg, "wasm32-wasi", env); code != 0 {
			t.Errorf("wasm32-wasi %s: exit %d, want 0\n%s", env, code, stderr)
		}
	}
}

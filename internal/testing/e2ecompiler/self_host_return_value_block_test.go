package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const returnValueBlockProg = `enum Color { Red, Black }

enum Tree {
  Node(Color, Tree, i32, Tree),
  Leaf,
}

@noinline function is_red_expr(t: Tree): boolean {
  return match (t) { Node(Red(), _, _, _) => true, _ => false };
}

@noinline function is_red_stmt(t: Tree): boolean {
  match (t) {
    Node(Red(), _, _, _) => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

@noinline function band_expr(n: i32): i32 {
  return if (n < 10) { 1 } else if (n < 100) { 2 } else { 3 };
}

@noinline function band_stmt(n: i32): i32 {
  if (n < 10) {
    return 1;
  } else if (n < 100) {
    return 2;
  } else {
    return 3;
  }
}

@noinline function color_expr(c: Color): i32 {
  return match (c) { Red => 7, Black => 9 };
}

@noinline function color_stmt(c: Color): i32 {
  match (c) {
    Red => {
      return 7;
    },
    Black => {
      return 9;
    }
  }
}

function main(): i32 {
  let red: Tree = Node(Red, Leaf, 1, Leaf);
  let black: Tree = Node(Black, red, 2, Leaf);
  let reds: i32 = 0;
  for t in [red, black, Leaf] {
    if (is_red_expr(t) != is_red_stmt(t)) {
      return 1;
    }
    if (is_red_expr(t)) {
      reds = reds + 1;
    }
  }
  if (reds != 1) {
    return 2;
  }
  for k in [3, 30, 300] {
    if (band_expr(k) != band_stmt(k)) {
      return 3;
    }
  }
  if (band_expr(30) != 2 || color_expr(Black) != color_stmt(Black) || color_expr(Red) != 7) {
    return 4;
  }
  return 42;
}
`

var returnValueAsmLabel = regexp.MustCompile(`\.L[\w$]+`)

// The text from a function's label to the end of its frame description, with
// its name and its local labels made neutral.
func normalisedBody(assembly, name string) string {
	at := strings.Index(assembly, "\n__fn_"+name+":\n")
	if at < 0 {
		return ""
	}
	rest := assembly[at+1:]
	if end := strings.Index(rest, "\n    .cfi_endproc"); end >= 0 {
		rest = rest[:end]
	}
	return returnValueAsmLabel.ReplaceAllString(strings.ReplaceAll(rest, name, "F"), "L")
}

// A return of a value match or a value if gives the same code as the
// statement form that returns in each arm (#14094).
func TestSelfHostReturnOfValueBlockMatchesStatementForm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "return_value_block.fern")
	if err := os.WriteFile(src, []byte(returnValueBlockProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, returnValueBlockProg, target); code != 42 {
				t.Fatalf("exit %d, want 42\n%s", code, stderr)
			}
			assembly, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range []string{"is_red", "band", "color"} {
				expr := normalisedBody(string(assembly), pair+"_expr")
				stmt := normalisedBody(string(assembly), pair+"_stmt")
				if expr == "" || stmt == "" {
					t.Fatalf("no body for %s_expr or %s_stmt", pair, pair)
				}
				if expr != stmt {
					t.Errorf("%s: the value form differs from the statement form\n--- %s_expr\n%s\n--- %s_stmt\n%s", pair, pair, expr, pair, stmt)
				}
			}
		})
	}
}

package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exhaustiveModuleSrc = `pub enum Color { Red, Black }
pub enum Tree { Node(Color, Tree, i32, Tree), Leaf }

pub function kind(t: Tree): i32 {
  match (t) {
    Node(Red(), _, k, _) => { return 100 + k; },
    Node(Black(), Node(Red(), _, _, _), k, _) => { return 200 + k; },
    Node(Black(), _, k, _) => { return 300 + k; },
    Leaf => { return 0; }
  }
}
`

const gapModuleSrc = `pub enum Color { Red, Black }
pub enum Tree { Node(Color, Tree, i32, Tree), Leaf }

pub function kind(t: Tree): i32 {
  match (t) {
    Node(Red(), _, k, _) => { return 100 + k; },
    Leaf => { return 0; }
  }
}
`

const exhaustiveModuleMain = `import "./tree";

function main(): i32 {
  let red: tree.Tree = tree.Node(tree.Red, tree.Leaf, 1, tree.Leaf);
  let a: i32 = tree.kind(red);
  let b: i32 = tree.kind(tree.Node(tree.Black, red, 2, tree.Leaf));
  let c: i32 = tree.kind(tree.Node(tree.Black, tree.Leaf, 3, tree.Leaf));
  if (a != 101 || b != 202 || c != 303 || tree.kind(tree.Leaf) != 0) {
    return 1;
  }
  return 0;
}
`

func writeTreeProgram(t *testing.T, module string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tree.fern"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(exhaustiveModuleMain), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// A match in an imported module is held to E030 over its arms as written
// (#12257), where they name its enum's variants bare while the enum lists
// them under the module's prefix: a complete match compiles and runs, one
// that leaves `Node(Black, ..)` unmatched names the variant.
func TestSelfHostImportedModuleMatchExhaustiveness(t *testing.T) {
	cli := buildSelfHostCLI(t)
	if out, code := cli.exitOfFile(t, writeTreeProgram(t, exhaustiveModuleSrc), "x86-64-linux", nil); code != 0 {
		t.Fatalf("complete match in an imported module: exit %d, want 0\n%s", code, out)
	}
	src := writeTreeProgram(t, gapModuleSrc)
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out.s"), src, cli.stdlib)
	msg, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E030") || !strings.Contains(string(msg), "does not cover variant Node of enum Tree —") {
		t.Fatalf("a gap in an imported module's match: want E030 naming the variant, got err=%v\n%s", err, msg)
	}
}

package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const typedConstUse = `import "./kinds";

function none(): kinds.Inst {
  return kinds.Inst { kind: KIND_EXPR, a: 0, b: 0 };
}

function build(n: i32): kinds.Inst[] {
  let xs: kinds.Inst[] = [];
  let cur = none();
  for i in 0..n {
    xs = xs.append(cur);
    cur = kinds.Inst { ...cur, a: i };
  }
  return xs;
}

function main(): i32 {
  return build(3).len() + LOCAL_EXPR;
}

const LOCAL_ONE: i32 = 1;
const LOCAL_ON: boolean = true;
`

// A typed const whose value is a literal of its declared type compiles as
// that literal, in its own module and across an import: the program that
// names the const and the one that writes the literal emit the same code.
func TestSelfHostTypedConstIsItsLiteral(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	emit := func(kind, local string) []byte {
		dir := t.TempDir()
		files := map[string]string{
			"kinds.fern": "pub const COPY: i32 = 7;\n\npub struct Inst { kind: i32, a: i32, b: i32 }\n",
			"main.fern":  strings.NewReplacer("KIND_EXPR", kind, "LOCAL_EXPR", local).Replace(typedConstUse),
		}
		for name, src := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		out := filepath.Join(dir, "out.s")
		cmd := exec.Command(cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, filepath.Join(dir, "main.fern"), stdlib)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile with %s, %s: %v\n%s", kind, local, err, msg)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return asm
	}
	literal := emit("7", "1")
	if named := emit("kinds.COPY", "LOCAL_ONE"); !bytes.Equal(literal, named) {
		t.Error("naming the typed consts changed the emitted code")
	}
	if guarded := emit("kinds.COPY", "(if (LOCAL_ON) { 1 } else { 0 })"); len(guarded) == 0 {
		t.Error("a boolean const did not compile")
	}
}

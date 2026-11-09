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
	literal := emitTypedConstProgram(t, cli, stdlib, typedConstUse, "7", "1")
	if named := emitTypedConstProgram(t, cli, stdlib, typedConstUse, "kinds.COPY", "LOCAL_ONE"); !bytes.Equal(literal, named) {
		t.Error("naming the typed consts changed the emitted code")
	}
	if guarded := emitTypedConstProgram(t, cli, stdlib, typedConstUse, "kinds.COPY", "(if (LOCAL_ON) { 1 } else { 0 })"); len(guarded) == 0 {
		t.Error("a boolean const did not compile")
	}
}

const constInLambdaProbe = `import "./ast";
import "./lexer";
import "./parser";

function lambda_value(fd: parser.FuncDecl): ast.Expr {
  if let ast.StmtVar(v) = fd.body[0] {
    if let ast.ExprLambda(lm) = v.init {
      if let ast.StmtReturn(r) = lm.body[0] {
        return r.value;
      }
    }
  }
  return ast.ExprUnknown { kind: "", line: 0, col: 0 };
}

function main(): i32 {
  let src = "const K: i32 = 7;\nfunction f(): i32 {\n  let g = () => K;\n  return g();\n}\nfunction h(): i32 {\n  let k = () => 2;\n  return k();\n}\n";
  let mod = parser.parse_module(lexer.tokenize(src));
  let seen = 0;
  for fd in mod.funcs {
    if (fd.name == "f") {
      seen += 1;
      let ast.ExprNumber(n) = lambda_value(fd) else {
        print("the const in the lambda is not its value");
        return 2;
      };
      if (n.text != "7") {
        print("the const in the lambda has the wrong value");
        return 3;
      }
    }
    if (fd.name == "h") {
      seen += 1;
      let ast.ExprNumber(_) = lambda_value(fd) else {
        print("a body without a const changed");
        return 4;
      };
    }
  }
  if (seen != 2) {
    print("missing function");
    return 1;
  }
  print("ok");
  return 0;
}
`

// The substitution skips a body that names no const, so a body that names
// one only inside a lambda must still have each use replaced.
func TestSelfHostConstInLambdaIsItsLiteral(t *testing.T) {
	dir := t.TempDir()
	e2eharness.CopySelfHostDriver(t, dir, "parser.fern")
	if err := os.WriteFile(filepath.Join(dir, "const_in_lambda.fern"), []byte(constInLambdaProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := e2eharness.CachedDriverBinFor(t, dir, "const_in_lambda.fern", e2eharness.TargetX86_64Linux)
	if out, err := runScaleTarget(t, e2eharness.TargetX86_64Linux, bin).CombinedOutput(); err != nil || !strings.HasPrefix(string(out), "ok\n") {
		t.Fatalf("const in lambda: %v\n%s", err, out)
	}
}

func emitTypedConstProgram(t *testing.T, cli, stdlib, program, kind, local string) []byte {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"kinds.fern": "pub const COPY: i32 = 7;\n\npub struct Inst { kind: i32, a: i32, b: i32 }\n",
		"main.fern":  strings.NewReplacer("KIND_EXPR", kind, "LOCAL_EXPR", local).Replace(program),
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

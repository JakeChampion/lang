package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// lexicalBindDriver resolves one function of n locals with
// lexical.resolve_func and counts the appends that copy a shared array. Each
// local is bound as the last use of the old scope, so no bind may copy.
const lexicalBindDriver = `import "./lexical"; import "./parser"; import "./lexer"; import "./util";

function lets(n: i32): string {
  let body = "";
  for k in 0..n {
    body += "  let v" + util.i32_to_string(k) + " = \"x\";\n";
  }
  return "function f(): i32 {\n" + body + "  return 0;\n}\n";
}

function resolve_copies(n: i32): i32 {
  let mod = parser.parse_module(lexer.tokenize(lets(n)));
  let before = __arr_push_shared_count();
  let r = lexical.resolve_func(mod.funcs[0]);
  return __arr_push_shared_count() - before + r.func.body.len() * 0;
}

function main(): i32 {
  print("16 " + util.i32_to_string(resolve_copies(16)));
  print("64 " + util.i32_to_string(resolve_copies(64)));
  return 0;
}
`

// lexical.resolve_func binds the locals of a function body in place (#14179).
func TestSelfHostLexicalResolveBindsInPlace(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "lexical_bind.fern"), []byte(lexicalBindDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "lexical_bind.fern", "lexical-bind")
	got, err := runX86_64Bin(runner, driver).CombinedOutput()
	if err != nil {
		t.Fatalf("lexical driver: %v\n%s", err, got)
	}
	if want := "16 0\n64 0\n"; string(got) != want {
		t.Fatalf("copies while resolving a function of 16 and of 64 locals:\n%s\nwant:\n%s", got, want)
	}
}

package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// bindInferredDriver binds the locals of one function in sequence, and
// annotates the function, and counts the appends that copy a shared array.
// Each bind is the last use of the old scope, so no bind may copy the scope's
// arrays, for an annotated or an unannotated local.
const bindInferredDriver = `import "./checker"; import "./parser"; import "./lexer"; import "./util";

function lets(n: i32, annotated: boolean): string {
  let body = "";
  for k in 0..n {
    let ty = if (annotated) { ": string" } else { "" };
    body += "  let v" + util.i32_to_string(k) + ty + " = \"x\";\n";
  }
  return "function f(): i32 {\n" + body + "  return 0;\n}\n";
}

function crossings(src: string): i32 {
  let v = checker.query_view(parser.parse_module(lexer.tokenize(src)));
  let s = checker.func_scope(v, v.mod.funcs[0]);
  let before = __arr_push_shared_count();
  for st in v.mod.funcs[0].body {
    s = checker.bind_stmt(st, s);
  }
  return __arr_push_shared_count() - before;
}

function annotate_copies(src: string): i32 {
  let mod = parser.parse_module(lexer.tokenize(src));
  let before = __arr_push_shared_count();
  let out = checker.annotate_module(mod);
  return __arr_push_shared_count() - before + out.funcs.len() * 0;
}

function main(): i32 {
  print("bind annotated " + util.i32_to_string(crossings(lets(64, true))));
  print("bind inferred " + util.i32_to_string(crossings(lets(64, false))));
  print("annotate annotated " + util.i32_to_string(annotate_copies(lets(64, true))));
  print("annotate inferred " + util.i32_to_string(annotate_copies(lets(64, false))));
  return 0;
}
`

// An unannotated local is bound in place, as an annotated one is (#14179).
func TestSelfHostBindInferredGrowsInPlace(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "bind_inferred.fern"), []byte(bindInferredDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "bind_inferred.fern", "bind-inferred")
	got, err := runX86_64Bin(runner, driver).CombinedOutput()
	if err != nil {
		t.Fatalf("bind driver: %v\n%s", err, got)
	}
	if want := "bind annotated 0\nbind inferred 0\nannotate annotated 0\nannotate inferred 0\n"; string(got) != want {
		t.Fatalf("copies while binding 64 locals:\n%s\nwant:\n%s", got, want)
	}
}

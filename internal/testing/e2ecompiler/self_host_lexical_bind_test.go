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

// lexicalGlobalsDriver resolves one function of n statements that name only
// globals and counts the heap allocations of lexical.resolve_func.
const lexicalGlobalsDriver = `import "./lexical"; import "./parser"; import "./lexer"; import "./util";

function calls(n: i32): string {
  let body = "";
  for k in 0..n {
    body += "  emit(limit + " + util.i32_to_string(k) + ", name.trim());\n";
  }
  return "function f(): i32 {\n" + body + "  return 0;\n}\n";
}

function resolve_allocs(n: i32): i64 {
  let mod = parser.parse_module(lexer.tokenize(calls(n)));
  let before: i64 = __heap_alloc_count();
  let r = lexical.resolve_func(mod.funcs[0]);
  return __heap_alloc_count() - before + (r.func.body.len() * 0) as i64;
}

function main(): i32 {
  let small = resolve_allocs(16);
  let large = resolve_allocs(64);
  if (large - small < 576i64) {
    print("ok");
  } else {
    print("16 statements: " + small.to_string() + ", 64 statements: " + large.to_string());
  }
  return 0;
}
`

// lexical.resolve_func does not build an expression again when the expression
// names no local (#14179). Each statement of the driver then costs 9
// allocations, and cost 17 when each identifier made a node. The limit is 12
// for each of the 48 more statements.
func TestSelfHostLexicalResolveKeepsGlobalStatements(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "lexical_globals.fern"), []byte(lexicalGlobalsDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "lexical_globals.fern", "lexical-globals")
	got, err := runX86_64Bin(runner, driver).CombinedOutput()
	if err != nil {
		t.Fatalf("lexical driver: %v\n%s", err, got)
	}
	if want := "ok\n"; string(got) != want {
		t.Fatalf("allocations while resolving statements that name no local:\n%s", got)
	}
}

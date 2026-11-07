package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// instPendingDriver checks a module of n functions that each call a generic
// function, and counts the appends that copy a shared array. The checker
// gathers the instances of each body into one list, so no body may copy it.
const instPendingDriver = `import "./checker"; import "./parser"; import "./lexer"; import "./util";

function program(n: i32): string {
  let src = "function id[T](x: T): T { return x; }\n";
  for k in 0..n {
    src += "function f" + util.i32_to_string(k) + "(): i32 { return id(" + util.i32_to_string(k) + "); }\n";
  }
  return src + "function main(): i32 { return f0(); }\n";
}

function check_copies(n: i32): i32 {
  let mod = parser.parse_module(lexer.tokenize(program(n)));
  let before = __arr_push_shared_count();
  let mt = checker.check_module(mod);
  return __arr_push_shared_count() - before + mt.diags.len() * 0;
}

function main(): i32 {
  print("32 " + util.i32_to_string(check_copies(32)));
  print("128 " + util.i32_to_string(check_copies(128)));
  return 0;
}
`

// The instances that check_module gathers grow in place (#14179).
func TestSelfHostInstancesGrowInPlace(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "inst_pending.fern"), []byte(instPendingDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "inst_pending.fern", "inst-pending")
	got, err := runX86_64Bin(runner, driver).CombinedOutput()
	if err != nil {
		t.Fatalf("instances driver: %v\n%s", err, got)
	}
	if want := "32 0\n128 0\n"; string(got) != want {
		t.Fatalf("copies while checking 32 and 128 generic calls:\n%s\nwant:\n%s", got, want)
	}
}

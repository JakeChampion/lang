package e2ecompiler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// liftTablesDriver lifts the lambdas of a module of n functions, each with one
// lambda, and prints the bytes that appends copied off a shared array. The
// name and signature tables of the lifted functions grow in place, so the
// bytes grow linearly with n.
const liftTablesDriver = `import "./lift"; import "./parser"; import "./lexer"; import "./util";

function program(n: i32): string {
  let src = "";
  for k in 0..n {
    let i = util.i32_to_string(k);
    src += "function f" + i + "(a: i32): i32 {\n  let g = (x: i32): i32 => x + a + " + i + ";\n  return g(1);\n}\n";
  }
  return src + "function main(): i32 { return f0(1); }\n";
}

function lift_bytes(n: i32): i64 {
  let mod = parser.parse_module(lexer.tokenize(program(n)));
  let before = __arr_push_shared_bytes();
  let out = lift.lift_lambdas_view_typed(mod, []);
  return __arr_push_shared_bytes() - before + (out.funcs.len() * 0) as i64;
}

function main(): i32 {
  print(util.i64_to_string(lift_bytes(256)));
  print(util.i64_to_string(lift_bytes(512)));
  return 0;
}
`

// Lifting the lambdas of twice the functions copies about twice the bytes,
// not four times (#14179).
func TestSelfHostLiftTablesGrowInPlace(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "lift_tables.fern"), []byte(liftTablesDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "lift_tables.fern", "lift-tables")
	got, err := runX86_64Bin(runner, driver).CombinedOutput()
	if err != nil {
		t.Fatalf("lift driver: %v\n%s", err, got)
	}
	lines := strings.Fields(string(got))
	if len(lines) != 2 {
		t.Fatalf("lift driver output:\n%s", got)
	}
	small, err1 := strconv.ParseInt(lines[0], 10, 64)
	large, err2 := strconv.ParseInt(lines[1], 10, 64)
	if err1 != nil || err2 != nil || small <= 0 {
		t.Fatalf("lift driver output:\n%s", got)
	}
	if large > 3*small {
		t.Fatalf("lifting 512 functions copies %d bytes and 256 functions %d bytes: the tables are copied for each function", large, small)
	}
}

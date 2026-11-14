package e2ecompiler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// liftClosuresDriver lifts the lambdas of one function with n closure
// bindings, each called one time, and prints the count of the functions
// that the lift makes and the allocations of the lift. The module is checked
// before the count starts.
const liftClosuresDriver = `import "./lift"; import "./parser"; import "./lexer"; import "./checker"; import "./util";

function program(n: i32): string {
  let src = "function work(p: i32): i32 {\n  let total = 0;\n";
  for k in 0..n {
    let i = util.i32_to_string(k);
    src += "  let g" + i + " = (x: i32): i32 => x + p + " + i + ";\n  total += g" + i + "(" + i + ");\n";
  }
  return src + "  return total;\n}\nfunction main(): i32 { return work(3); }\n";
}

function lift_allocs(n: i32): string {
  let mod = checker.ensure_annotated(parser.parse_module(lexer.tokenize(program(n))));
  let before = __heap_alloc_count();
  let out = lift.lift_lambdas_view_typed(mod, []);
  let allocs = __heap_alloc_count() - before;
  return util.i32_to_string(out.funcs.len()) + " " + util.i64_to_string(allocs);
}

function main(): i32 {
  print(lift_allocs(250));
  print(lift_allocs(1000));
  return 0;
}
`

// The lift of a function with four times the closures allocates about four
// times as much, not sixteen times (#14578).
func TestSelfHostLiftClosuresInLinearWork(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "lift_closures.fern"), []byte(liftClosuresDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "lift_closures.fern", "lift-closures")
	got, err := runX86_64Bin(runner, driver).CombinedOutput()
	if err != nil {
		t.Fatalf("lift driver: %v\n%s", err, got)
	}
	f := strings.Fields(string(got))
	if len(f) != 4 {
		t.Fatalf("lift driver output:\n%s", got)
	}
	var v [4]int64
	for i := range f {
		n, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			t.Fatalf("lift driver output:\n%s", got)
		}
		v[i] = n
	}
	// work and main, and one function for each closure.
	if v[0] != 252 || v[2] != 1002 {
		t.Fatalf("the lift makes %d and %d functions, want 252 and 1002: a closure was not lifted\n%s", v[0], v[2], got)
	}
	small, large := v[1], v[3]
	t.Logf("250 closures: %d allocations, 1000 closures: %d allocations", small, large)
	if small <= 0 || large > 8*small {
		t.Fatalf("1000 closures allocate %d times and 250 closures %d times: the lift does work for each pair of closures", large, small)
	}
}

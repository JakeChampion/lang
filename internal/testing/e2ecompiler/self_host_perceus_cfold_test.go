package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Track leaf multiplicities instead of constructing or rewriting expressions.
func perceusFoldOracle(depth int, initial int64) (constant, variables int64) {
	leaves := map[int64]int64{initial: 1}
	for i := 0; i < depth; i++ {
		next := make(map[int64]int64)
		for v, count := range leaves {
			next[v+1] += count
			previous := v - 1
			if previous < 0 {
				previous = 0
			}
			next[previous] += count
		}
		leaves = next
	}
	for v, count := range leaves {
		if v == 0 {
			variables += count
		} else {
			constant += v * count
		}
	}
	return
}

func TestSelfHostPerceusConstantFold(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "bench", "perceus", "cfold.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cfold.fern"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	src.WriteString(`import "./cfold";
function same(a: cfold.Expr, b: cfold.Expr): boolean {
  match (a) {
    cfold.Var(x) => { match (b) { cfold.Var(y) => { return x == y; }, _ => { return false; } } },
    cfold.Val(x) => { match (b) { cfold.Val(y) => { return x == y; }, _ => { return false; } } },
    cfold.Add(l, r) => { match (b) { cfold.Add(x, y) => { return same(l, x) && same(r, y); }, _ => { return false; } } },
    cfold.Mul(l, r) => { match (b) { cfold.Mul(x, y) => { return same(l, x) && same(r, y); }, _ => { return false; } } }
  }
}
function eval_at(e: cfold.Expr, x: i64): i64 {
  match (e) {
    cfold.Var(id) => { return id * x; },
    cfold.Val(v) => { return v; },
    cfold.Add(l, r) => { return eval_at(l, x) + eval_at(r, x); },
    cfold.Mul(l, r) => { return eval_at(l, x) * eval_at(r, x); }
  }
}
function generated(depth: i32, initial: i64, constant: i64, variables: i64): boolean {
  let original: cfold.Expr = cfold.mk_expr(depth, initial);
  let saved: cfold.Expr = original;
  let rearranged: cfold.Expr = cfold.reassoc(original);
  let folded: cfold.Expr = cfold.cfold(rearranged);
  if (cfold.eval(folded) != constant || cfold.eval(original) != constant) { return false; }
  for x in [-3i64, 0i64, 1i64, 7i64] {
    let expected: i64 = constant + variables * x;
    if (eval_at(original, x) != expected || eval_at(rearranged, x) != expected || eval_at(folded, x) != expected) { return false; }
  }
  return same(saved, cfold.mk_expr(depth, initial));
}
function main(): i32 {
`)
	// Pin constructor shape and branch ordering as well as the arithmetic result.
	cases := []struct{ input, expected string }{
		{"Var(7i64)", "Var(7i64)"},
		{"Val(-5i64)", "Val(-5i64)"},
		{"Add(Val(2i64), Val(3i64))", "Val(5i64)"},
		{"Mul(Val(-2i64), Val(3i64))", "Val(-6i64)"},
		{"Add(Val(2i64), Add(Var(7i64), Val(3i64)))", "Add(Val(5i64), Var(7i64))"},
		{"Add(Val(2i64), Add(Val(3i64), Var(7i64)))", "Add(Val(5i64), Var(7i64))"},
		{"Mul(Val(2i64), Mul(Var(7i64), Val(3i64)))", "Mul(Val(6i64), Var(7i64))"},
		{"Mul(Val(2i64), Mul(Val(3i64), Var(7i64)))", "Mul(Val(6i64), Var(7i64))"},
		{"Add(Var(1i64), Val(3i64))", "Add(Var(1i64), Val(3i64))"},
		{"Mul(Var(1i64), Val(3i64))", "Mul(Var(1i64), Val(3i64))"},
		{"Add(Val(2i64), Mul(Var(1i64), Var(2i64)))", "Add(Val(2i64), Mul(Var(1i64), Var(2i64)))"},
		{"Mul(Val(2i64), Add(Var(1i64), Var(2i64)))", "Mul(Val(2i64), Add(Var(1i64), Var(2i64)))"},
	}
	qualify := strings.NewReplacer("Var(", "cfold.Var(", "Val(", "cfold.Val(", "Add(", "cfold.Add(", "Mul(", "cfold.Mul(")
	for i, tc := range cases {
		fmt.Fprintf(&src, "if (!same(cfold.cfold(%s), %s)) { return %d; }\n", qualify.Replace(tc.input), qualify.Replace(tc.expected), i+1)
	}
	for _, op := range []string{"Add", "Mul"} {
		fmt.Fprintf(&src, "if (!same(cfold.reassoc(cfold.%s(cfold.%s(cfold.Var(1i64), cfold.Var(2i64)), cfold.Var(3i64))), cfold.%s(cfold.Var(1i64), cfold.%s(cfold.Var(2i64), cfold.Var(3i64))))) { return 30; }\n", op, op, op, op)
	}
	for _, depth := range []int{0, 1, 2, 5, 8} {
		for _, initial := range []int64{0, 1, 3} {
			constant, variables := perceusFoldOracle(depth, initial)
			fmt.Fprintf(&src, "if (!generated(%d, %di64, %di64, %di64)) { return 40; }\n", depth, initial, constant, variables)
		}
	}
	src.WriteString("return 0;\n}\n")
	path := filepath.Join(dir, "fold_test.fern")
	if err := os.WriteFile(path, []byte(src.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("interpreter", func(t *testing.T) {
		if out, err := exec.Command(buildLangBinForInterp(t), "-interp", path).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			cli := buildSelfHostCLI(t)
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

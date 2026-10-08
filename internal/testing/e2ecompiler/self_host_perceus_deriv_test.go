package e2ecompiler

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Taylor coefficients of exp((1+t)*log(1+t)) give an independent analytic
// oracle for successive derivatives of x^x at x=1, without expression rewrites.
func perceusDerivAtOne(n int) int64 {
	h := make([]*big.Rat, n+1)
	f := make([]*big.Rat, n+1)
	h[0], f[0] = new(big.Rat), big.NewRat(1, 1)
	for k := 1; k <= n; k++ {
		if k == 1 {
			h[k] = big.NewRat(1, 1)
		} else {
			sign := int64(1)
			if k%2 != 0 {
				sign = -1
			}
			h[k] = big.NewRat(sign, int64(k*(k-1)))
		}
		f[k] = new(big.Rat)
		for j := 1; j <= k; j++ {
			term := new(big.Rat).Mul(h[j], f[k-j])
			term.Mul(term, big.NewRat(int64(j), 1))
			f[k].Add(f[k], term)
		}
		f[k].Quo(f[k], big.NewRat(int64(k), 1))
	}
	for k := 2; k <= n; k++ {
		f[n].Mul(f[n], big.NewRat(int64(k), 1))
	}
	if !f[n].IsInt() || !f[n].Num().IsInt64() {
		panic("derivative oracle outside integer range")
	}
	return f[n].Num().Int64()
}

func TestSelfHostPerceusDerivative(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "bench", "perceus", "deriv.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deriv.fern"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	src.WriteString(`import "./deriv";
function same(a: deriv.Expr, b: deriv.Expr): boolean {
  match (a) {
    deriv.Val(v) => { match (b) { deriv.Val(w) => { return v == w; }, _ => { return false; } } },
    deriv.Var(v) => { match (b) { deriv.Var(w) => { return v == w; }, _ => { return false; } } },
    deriv.Add(l, r) => { match (b) { deriv.Add(x, y) => { return same(l, x) && same(r, y); }, _ => { return false; } } },
    deriv.Mul(l, r) => { match (b) { deriv.Mul(x, y) => { return same(l, x) && same(r, y); }, _ => { return false; } } },
    deriv.Pow(l, r) => { match (b) { deriv.Pow(x, y) => { return same(l, x) && same(r, y); }, _ => { return false; } } },
    deriv.Ln(e) => { match (b) { deriv.Ln(f) => { return same(e, f); }, _ => { return false; } } }
  }
}
function at_one(e: deriv.Expr): i64 {
  match (e) {
    deriv.Val(v) => { return v; },
    deriv.Var(_) => { return 1i64; },
    deriv.Add(l, r) => { return at_one(l) + at_one(r); },
    deriv.Mul(l, r) => { return at_one(l) * at_one(r); },
    deriv.Pow(l, _) => { if (at_one(l) != 1i64) { exit(90); } return 1i64; },
    deriv.Ln(f) => { if (at_one(f) != 1i64) { exit(91); } return 0i64; }
  }
}
function bounded(e: deriv.Expr): boolean {
  match (e) {
    deriv.Val(v) => { return v >= -1i64 && v <= 1i64; },
    deriv.Var(_) => { return true; },
    deriv.Add(l, r) => { return bounded(l) && bounded(r); },
    deriv.Mul(l, r) => { return bounded(l) && bounded(r); },
    deriv.Pow(l, r) => { return bounded(l) && bounded(r); },
    deriv.Ln(f) => { return bounded(f); }
  }
}
function step(i: i32, e: deriv.Expr): deriv.Expr { return deriv.d("x", e); }
function main(): i32 {
`)
	qualify := strings.NewReplacer("Val(", "deriv.Val(", "Var(", "deriv.Var(", "Add(", "deriv.Add(", "Mul(", "deriv.Mul(", "Pow(", "deriv.Pow(", "Ln(", "deriv.Ln(")
	cases := []struct{ call, expected string }{
		{`add(Val(2i64), Val(3i64))`, `Val(5i64)`},
		{`add(Val(0i64), Var("x"))`, `Var("x")`},
		{`add(Var("x"), Val(0i64))`, `Var("x")`},
		{`add(Var("x"), Val(3i64))`, `Add(Val(3i64), Var("x"))`},
		{`add(Val(2i64), Add(Val(3i64), Var("x")))`, `Add(Val(5i64), Var("x"))`},
		{`add(Var("x"), Add(Val(3i64), Var("y")))`, `Add(Val(3i64), Add(Var("x"), Var("y")))`},
		{`add(Add(Var("x"), Var("y")), Var("z"))`, `Add(Var("x"), Add(Var("y"), Var("z")))`},
		{`mul(Val(-2i64), Val(3i64))`, `Val(-6i64)`},
		{`mul(Val(0i64), Var("x"))`, `Val(0i64)`},
		{`mul(Var("x"), Val(0i64))`, `Val(0i64)`},
		{`mul(Val(1i64), Var("x"))`, `Var("x")`},
		{`mul(Var("x"), Val(1i64))`, `Var("x")`},
		{`mul(Var("x"), Val(3i64))`, `Mul(Val(3i64), Var("x"))`},
		{`mul(Val(2i64), Mul(Val(3i64), Var("x")))`, `Mul(Val(6i64), Var("x"))`},
		{`mul(Var("x"), Mul(Val(3i64), Var("y")))`, `Mul(Val(3i64), Mul(Var("x"), Var("y")))`},
		{`mul(Mul(Var("x"), Var("y")), Var("z"))`, `Mul(Var("x"), Mul(Var("y"), Var("z")))`},
		{`powr(Val(0i64), Val(0i64))`, `Val(1i64)`},
		{`powr(Val(0i64), Val(-1i64))`, `Val(0i64)`},
		{`powr(Val(1i64), Val(-1i64))`, `Val(1i64)`},
		{`powr(Val(-1i64), Val(-3i64))`, `Val(-1i64)`},
		{`powr(Val(-1i64), Val(-2i64))`, `Val(1i64)`},
		{`powr(Val(2i64), Val(-1i64))`, `Val(0i64)`},
		{`powr(Val(-2i64), Val(5i64))`, `Val(-32i64)`},
		{`powr(Var("x"), Val(0i64))`, `Val(1i64)`},
		{`powr(Var("x"), Val(1i64))`, `Var("x")`},
		{`powr(Val(0i64), Var("x"))`, `Val(0i64)`},
		{`ln(Val(1i64))`, `Val(0i64)`},
		{`ln(Var("x"))`, `Ln(Var("x"))`},
		{`d("x", Val(7i64))`, `Val(0i64)`},
		{`d("x", Var("x"))`, `Val(1i64)`},
		{`d("x", Var("y"))`, `Val(0i64)`},
		{`d("x", Mul(Var("x"), Var("x")))`, `Add(Var("x"), Var("x"))`},
		{`d("x", Ln(Var("x")))`, `Pow(Var("x"), Val(-1i64))`},
	}
	for i, tc := range cases {
		fmt.Fprintf(&src, "if (!same(deriv.%s, %s)) { return %d; }\n", qualify.Replace(tc.call), qualify.Replace(tc.expected), i+1)
	}
	// Counts from the committed arbitrary-precision DAG audit; no memoization
	// is used by the benchmark. The independent Taylor oracle checks its value.
	counts := []int64{2, 6, 22, 90, 420, 2202, 12886}
	src.WriteString("let original: deriv.Expr = deriv.Pow(deriv.Var(\"x\"), deriv.Var(\"x\"));\nlet e: deriv.Expr = original;\n")
	for i, count := range counts {
		fmt.Fprintf(&src, "if (deriv.count(e) != %di64 || at_one(e) != %di64 || !bounded(e)) { return %d; }\n", count, perceusDerivAtOne(i), 40+i)
		if i+1 < len(counts) {
			fmt.Fprintf(&src, "let saved%d: deriv.Expr = e;\ne = deriv.d(\"x\", e);\nif (deriv.count(saved%d) != %di64 || at_one(saved%d) != %di64) { return 60; }\n", i, i, count, i, perceusDerivAtOne(i))
		}
	}
	src.WriteString("if (!same(original, deriv.Pow(deriv.Var(\"x\"), deriv.Var(\"x\"))) || !same(e, deriv.nest(step, 6, original))) { return 61; }\nreturn 0;\n}\n")
	path := filepath.Join(dir, "deriv_test.fern")
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

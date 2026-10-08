package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Enumerate row placements by backtracking, independently of the benchmark's
// breadth-wise lists of shared partial solutions.
func perceusQueensOracle(n int) []int64 {
	var answers []int64
	var visit func([]int)
	visit = func(rows []int) {
		if len(rows) == n {
			var code int64
			for i := len(rows) - 1; i >= 0; i-- {
				code = code*int64(n+1) + int64(rows[i]+1)
			}
			answers = append(answers, code)
			return
		}
		for column := 0; column < n; column++ {
			safe := true
			for row, previous := range rows {
				distance := len(rows) - row
				if column == previous || column-previous == distance || previous-column == distance {
					safe = false
					break
				}
			}
			if safe {
				visit(append(rows, column))
			}
		}
	}
	visit(nil)
	return answers
}

func TestSelfHostPerceusNQueens(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "bench", "perceus", "nqueens.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nqueens.fern"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	src.WriteString(`import "./nqueens";
function encode(xs: nqueens.Positions, n: i32): i64 {
  let code: i64 = 0i64;
  let count: i32 = 0;
  let done: boolean = false;
  while (!done) {
    match (xs) {
      nqueens.NoPositions => { done = true; },
      nqueens.Position(q, rest) => {
        if (q < 1 || q > n) { return -1i64; }
        code = code * (n + 1) as i64 + q as i64;
        count = count + 1;
        xs = rest;
      }
    }
  }
  if (count != n) { return -1i64; }
  return code;
}
function check(n: i32, expected: i64[]): i32 {
  let solutions: nqueens.Solutions = nqueens.find_solutions(n, n);
  let saved: nqueens.Solutions = solutions;
  let remaining: i64[] = expected;
  let count: i32 = 0;
  let done: boolean = false;
  while (!done) {
    match (solutions) {
      nqueens.NoSolutions => { done = true; },
      nqueens.Solution(xs, rest) => {
        let code: i64 = encode(xs, n);
        if (code < 0i64) { return 10; }
        let found: i32 = -1;
        let i: i32 = 0;
        while (i < remaining.len()) {
          if (remaining[i] == code) { found = i; }
          i = i + 1;
        }
        if (found < 0) { return 11; }
        remaining = remaining.with(found, -1i64);
        count = count + 1;
        solutions = rest;
      }
    }
  }
  if (count != expected.len() || nqueens.length(saved) != count || nqueens.queens(n) != count) { return 12; }
  return 0;
}
function main(): i32 {
`)
	for _, n := range []int{0, 1, 2, 3, 4, 6, 8} {
		var values []string
		for _, code := range perceusQueensOracle(n) {
			values = append(values, fmt.Sprintf("%di64", code))
		}
		fmt.Fprintf(&src, "if (check(%d, [%s]) != 0) { return %d; }\n", n, strings.Join(values, ","), n+20)
	}
	src.WriteString("return 0;\n}\n")
	path := filepath.Join(dir, "queens_test.fern")
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

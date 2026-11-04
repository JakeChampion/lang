package interp

import (
	"errors"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func runFolded(t *testing.T, src string) (Value, error, int) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	constReads := 0
	for _, fn := range prog.Funcs {
		ast.Walk(fn.Body, func(n ast.Node) bool {
			if ix, ok := n.(*ast.Index); ok {
				if lit, ok := ix.Array.(*ast.ArrayLit); ok && lit.Const {
					constReads++
				}
			}
			return true
		})
	}
	i := New()
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	v, err := i.CallByName("main", nil)
	return v, err, constReads
}

func TestConstArrayIndexReadsOneElement(t *testing.T) {
	v, err, reads := runFolded(t, `
		const T: i32[] = [10, 20, 30];
		const G: i32[][] = [[1, 2], [3, 4]];
		const F: f64[] = [0.5, 1.5];
		function main(): i32 {
			let row: i32[] = G[0];
			row = row.with(0, 9);
			let k: i32 = 2;
			let f: f64 = F[1] * 2.0;
			return T[k] + G[1][0] * 100 + row[0] * 1000 + G[0][0] * 10000 + f as i32;
		}`)
	if err != nil {
		t.Fatalf("call main: %v", err)
	}
	if reads < 4 {
		t.Fatalf("constfold left %d indexed const literals, want at least 4", reads)
	}
	if n, ok := v.(Number); !ok || n != 30+300+9000+10000+3 {
		t.Errorf("got %v, want %d", v, 30+300+9000+10000+3)
	}
}

func TestConstArrayIndexOutOfBounds(t *testing.T) {
	_, err, _ := runFolded(t, `
		const T: i32[] = [10, 20, 30];
		function main(): i32 {
			let k: i32 = 3;
			return T[k];
		}`)
	var ab *Abort
	if !errors.As(err, &ab) {
		t.Fatalf("got %v, want an abort", err)
	}
	if want := indexAbort(CauseArrOOB, "3", 3).Error(); ab.Error() != want {
		t.Errorf("got %q, want %q", ab.Error(), want)
	}
}

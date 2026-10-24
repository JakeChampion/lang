package constfold_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestFoldKeepsDeadArmsForTheChecker(t *testing.T) {
	for _, cond := range []string{"false", `target_os() == "wasi-http"`} {
		prog, err := parser.Parse(`struct Pair { fst: i32, snd: i32 }
function main(): i32 {
    if (` + cond + `) {
        let x: u8 = Pair { fst: 1, snd: 2 };
    }
    return 0;
}`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: "linux"}); err != nil {
			t.Fatalf("fold: %v", err)
		}
		_, err = checker.Check(prog)
		if err == nil || !strings.Contains(err.Error(), "expected u8, got Pair") {
			t.Errorf("if (%s): check err = %v, want the dead arm's type error", cond, err)
		}
	}
}

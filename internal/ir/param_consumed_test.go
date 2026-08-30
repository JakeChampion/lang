package ir_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Func.ParamConsumed is the lowering's own ownership verdict, recorded so
// something other than the lowering can read it. A verdict that silently
// stopped being written would leave every reader answering "nothing is
// consumed", which reads exactly like a correct program with no owned
// parameters — so the population is asserted rather than assumed.
func TestLoweringRecordsAParamConsumedVerdictForEveryFunction(t *testing.T) {
	path := filepath.Join("..", "..", "conformance", "cases", "own_transfer", "main.fern")
	prog, _, err := modload.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}

	// Generated drop bodies are built after lowerFunc and carry no
	// verdict; every function the source declared has one.
	declared := map[string]bool{}
	for _, fn := range prog.Funcs {
		declared[fn.Name] = true
	}
	checked, consumed := 0, 0
	for _, fn := range ip.Funcs {
		if !declared[fn.Name] {
			continue
		}
		checked++
		if len(fn.ParamConsumed) != len(fn.Params) {
			t.Errorf("%s: %d parameters but %d verdicts",
				fn.Name, len(fn.Params), len(fn.ParamConsumed))
			continue
		}
		for i, c := range fn.ParamConsumed {
			// A parameter the source declared `own` is consumed by
			// definition — the first disjunct of the verdict — so it
			// pins the recording against a field that is present but
			// always false.
			if fn.Params[i].Own && !c {
				t.Errorf("%s: parameter %q is declared own but the recorded verdict says borrowed",
					fn.Name, fn.Params[i].Name)
			}
			if c {
				consumed++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no declared function was checked — the fixture or the name match is wrong")
	}
	if consumed == 0 {
		t.Fatalf("%d functions checked and not one consumed parameter; own_transfer declares one",
			checked)
	}
	t.Logf("checked %d declared functions, %d consumed parameters", checked, consumed)
}

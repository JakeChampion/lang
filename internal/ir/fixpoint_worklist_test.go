package ir

import (
	"crypto/sha256"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// The pre-pass fixpoints in LowerWith call order — every summaryTable.fixpoint
// a single lowering runs, which is what fixpointTables collects.
var lowerWithFixpoints = []string{
	"findReturnsNoParamEscape",
	"inferParamEscapes",
	"inferParamCountedRetain",
	"findReturnsFreshBox",
	"computeGrowParams",
}

// lowerSelfHostFixpoints lowers the self-host compiler with the pre-pass
// fixpoints either pruned by the worklist or sweeping every function every
// round, and returns the tables each pass settled on plus a digest of the
// lowered program.
func lowerSelfHostFixpoints(t *testing.T, exhaustive bool) ([]fixpointResult, [32]byte) {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "self_host", "fern.fern")
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
	var tables []fixpointResult
	fixpointTables = &tables
	fixpointExhaustive = exhaustive
	prevRc := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() {
		fixpointTables = nil
		fixpointExhaustive = false
		ast.RcFreeEnabled = prevRc
	}()
	ip, err := LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return tables, sha256.Sum256([]byte(ip.String()))
}

// The worklist revisits a function only when a summary it consulted changed,
// so its dependency edges come from the reads the analysis performed. A
// missed edge — a shape the pass reaches by a route the recording did not
// see — would settle on a different table than the full sweep and miscompile
// quietly, since the fixpoint suite reproduces whatever the compiler
// believes. The self-host compiler is the largest program the passes see, so
// its tables are compared entry for entry against the exhaustive sweep, and
// the lowered program as a whole.
func TestFixpointWorklistMatchesFullSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the self-host compiler twice; not a -short test")
	}
	worklist, worklistIR := lowerSelfHostFixpoints(t, false)
	sweep, sweepIR := lowerSelfHostFixpoints(t, true)
	if len(worklist) != len(lowerWithFixpoints) || len(sweep) != len(lowerWithFixpoints) {
		t.Fatalf("collected %d worklist and %d sweep tables, want %d",
			len(worklist), len(sweep), len(lowerWithFixpoints))
	}
	for i, name := range lowerWithFixpoints {
		w, s := worklist[i], sweep[i]
		if reflect.TypeOf(w.vals) != reflect.TypeOf(s.vals) {
			t.Fatalf("%s: table types differ (%T vs %T); the call order changed",
				name, w.vals, s.vals)
		}
		if !reflect.DeepEqual(w.vals, s.vals) {
			t.Errorf("%s: worklist table differs from the full sweep", name)
		}
		// A worklist that prunes nothing has stopped recording its reads,
		// and would still pass the identity check.
		if w.visits >= s.visits {
			t.Errorf("%s: worklist visited %d functions, full sweep %d; the worklist is not pruning",
				name, w.visits, s.visits)
		}
		t.Logf("%s: %d visits under the worklist, %d under the full sweep", name, w.visits, s.visits)
	}
	if worklistIR != sweepIR {
		t.Errorf("lowered program differs between the worklist and the full sweep")
	}
}

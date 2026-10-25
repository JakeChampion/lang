package e2e

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

// diffLeg is how one self-host differential leg builds a program and judges the
// result against the interpreter's exit code.
type diffLeg struct {
	fernBin, stdlibRoot, target string
	flags                       []string
	run                         func(t *testing.T, src string, env []string) (*selfHostRun, string)
	agrees                      func(r *selfHostRun, gap string, want int, src string) bool
}

// triage bisects a failing seed to the pass decision it follows, shrinks it
// to a smaller program that fails the same way, and records the finding.
func (l diffLeg) triage(t *testing.T, seed uint64, src string, want int, env []string, class string) {
	t.Helper()
	report, pass := bisectSelfHostSeed(t, l.fernBin, l.stdlibRoot, l.target, src, l.flags, env, func(env []string) bool {
		r, gap := l.run(t, src, env)
		return l.agrees(r, gap, want, src)
	})
	t.Log(report)
	report, repro := l.shrink(t, seed, env, class)
	t.Log(report)
	recordFinding(t, selfHostFinding{Test: legName(t), Seed: seed, Class: class, Pass: pass, Repro: repro})
}

const (
	selfHostShrinkBudget = 4
	selfHostShrinkTime   = 3 * time.Minute
)

var selfHostShrinks atomic.Int64

// shrink reduces the seed's program while it still fails: refused for the
// same reason when the compiler refused it, and otherwise still disagreeing
// with the interpreter.
func (l diffLeg) shrink(t *testing.T, seed uint64, env []string, class string) (report, repro string) {
	t.Helper()
	if selfHostShrinks.Add(1) > selfHostShrinkBudget {
		return fmt.Sprintf("shrink: skipped, %d seeds already shrunk in this run", selfHostShrinkBudget), ""
	}
	data, err := fernsmith.MainBytes(seed)
	if err != nil {
		return "shrink: " + err.Error(), ""
	}
	refused := strings.HasPrefix(class, "refused")
	deadline := time.Now().Add(selfHostShrinkTime)
	tries, late := 0, false
	fails := func(src string) bool {
		if late = late || time.Now().After(deadline); late {
			return false
		}
		tries++
		if refused {
			_, gap := l.run(t, src, env)
			return compilerRefused(gap) && refusalClass(gap) == class
		}
		want, err := interpRunExit(t, src)
		if err != nil {
			return false
		}
		r, gap := l.run(t, src, env)
		return gap == "" && !l.agrees(r, gap, want, src)
	}
	data = fernsmith.ShrinkMainBytes(data, func(cand []byte) bool { return fails(fernsmith.GenMainBytes(cand)) })
	repro = fernsmith.ShrinkSource(fernsmith.GenMainBytes(data), func(src string) bool { return checksClean(src) && fails(src) })
	stop := ""
	if late {
		stop = fmt.Sprintf(", stopped at the %v limit", selfHostShrinkTime)
	}
	return fmt.Sprintf("shrink: %d tries%s took the program from %d bytes to %d:\n%s",
		tries, stop, len(fernsmith.GenMain(seed)), len(repro), repro), repro
}

// checksClean reports whether src gets as far as the interpreter, as every
// program the generator writes does but one with a line deleted need not.
func checksClean(src string) bool {
	prog, _, err := modload.LoadSource(src)
	if err != nil || constfold.Fold(prog, nil) != nil {
		return false
	}
	info, err := checker.Check(prog)
	return err == nil && monomorph.Run(prog, info) == nil
}

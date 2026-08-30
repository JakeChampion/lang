package e2e

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/ssa"
	"github.com/jakechampion/lang/internal/treeshake"
)

// The static leak walk, measured against the runtime oracle.
//
// `ssa.Certify` says a value still holds an ownership unit where its
// function returns. The conformance leak census (#7831) says, per
// fixture, how many allocations the program never gave back on the path
// it took. A fixture the census pins at 0 leaked nothing on that path,
// so every function `Certify` flags in it is a report the runtime
// contradicts.
//
// # What the comparison is and is not
//
// It is not a proof of correctness in either direction. The census
// observes ONE path; the walk makes a claim about every path, so a
// function flagged in a clean fixture could in principle leak on a path
// the fixture never takes. That reading is available for any single
// finding and it is not available for a population: the previous probe
// flagged 20.3% of all functions in the clean fixtures, and the
// breakdown named `enum_sentinel` — a static `.rodata` cell that cannot
// leak on any path — as a top contributor. A rate is evidence about the
// walk even though an individual row is not.
//
// So this gate holds a CEILING on the rate rather than demanding zero,
// and holds a FLOOR under coverage beside it, because a walk that
// reports nothing because it understands nothing would satisfy a
// ceiling alone.
//
// x86-64 config only, matching the census: it runs the x86-64 backend,
// which is the only one with the heap tracer. The name carries that —
// `test-e2e-other.yml` is a catch-all for everything no dedicated lane
// claims, and `^TestX86_64` is how `test-e2e-x86_64.yml` claims a test.
// Measured before choosing: the catch-all lane runs 16m14s locally and
// 16m58s on a slow runner against an 18m timeout, so 13.6s of new work
// is enough to time it out, while the x86-64 lane runs 8m37s against
// 25m.
//
// The `unplaced call results` figure it logs is the result axis's own
// metric: 1527 before `internal/ir/rcresults.go` existed, 498 after.
func TestX86_64CertifyAgreesWithTheLeakCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the whole conformance corpus; not a -short test")
	}
	clean := cleanCensusFixtures(t)
	if len(clean) < 250 {
		t.Fatalf("only %d fixtures pinned clean in the census — this is no longer "+
			"measuring against the oracle", len(clean))
	}

	var funcs, flagged, skipped, liftFailed, unplaced, poisoned, values int
	byFunc := map[string]int{}
	byOrigin := map[string]int{}
	byKind := map[string]int{}
	byLiftErr := map[string]int{}
	for _, name := range clean {
		prog, ok := lowerFixtureForCertify(t, name)
		if !ok {
			continue
		}
		lifted, failures := ssa.LiftProgram(prog)
		liftFailed += len(failures)
		for _, lf := range failures {
			byLiftErr[liftErrClass(lf.Err.Error())]++
		}
		sol := ssa.SolveOwnership(lifted)
		for _, f := range lifted {
			rep := ssa.Certify(f, sol.Sigs)
			funcs++
			unplaced += rep.Unplaced
			poisoned += rep.Poisoned
			if !rep.Modelled {
				skipped++
				continue
			}
			values += len(rep.Leaks)
			if len(rep.Leaks) > 0 {
				flagged++
				byFunc[f.Name]++
				for _, l := range rep.Leaks {
					byOrigin[l.Origin.String()]++
					byKind[l.Kind.String()]++
				}
			}
		}
	}
	if funcs == 0 {
		t.Fatal("no functions walked — the corpus lowering is broken, not the walk")
	}

	rate := 100 * float64(flagged) / float64(funcs)
	t.Logf("%d fixtures the census pins clean, %d functions walked", len(clean), funcs)
	t.Logf("  flagged: %d functions (%.2f%%), %d values", flagged, rate, values)
	t.Logf("  walk skipped: %d, lift failures: %d (#7803, not the walk's): %s",
		skipped, liftFailed, topCounts(byLiftErr, 4))
	t.Logf("  unplaced call results: %d, poisoned roots: %d", unplaced, poisoned)
	t.Logf("  by origin: %s", topCounts(byOrigin, 5))
	t.Logf("  by defining op: %s", topCounts(byKind, 6))
	t.Logf("  worst functions: %s", topCounts(byFunc, 5))

	// Measured 2026-08-30: 2.05%, 15 functions of 730.
	//
	// A ratchet, not a specification — the same stance the leak census
	// itself takes. Every flagged function here is a report the runtime
	// contradicts and the walk should not make; the ceiling exists so
	// the number cannot climb back toward the 20.3% the probe this
	// replaces measured, and so the one open class below stays visible
	// instead of being absorbed.
	//
	// One class is left, from the breakdown above: `make_closure`. A
	// closure cell is 32 bytes from `__fern_alloc_rc1` with rc=1, and
	// lowering does not always emit its release. Whether each of these
	// is the walk missing a transfer or the compiler missing a drop is
	// unsettled — closure reclamation is on `docs/TEST-GATES.md`'s live
	// gap list, so both readings are open.
	//
	// The `alloc` and `call` classes that stood beside it are gone: a
	// unit threaded through a loop is disposed of under the PHI's name,
	// and attributing that transfer to the edge closed both at once.
	const rateCeiling = 4.0
	if rate > rateCeiling {
		t.Errorf("%.2f%% of functions flagged in fixtures the runtime says are clean, over the "+
			"%.1f%% ceiling — the walk is reporting leaks the oracle contradicts:\n  by op: %s\n  worst: %s",
			rate, rateCeiling, topCounts(byKind, 6), topCounts(byFunc, 10))
	}

	// The other half. A walk that gave up on everything would flag
	// nothing, so the ceiling above only means something beside a floor
	// on how much it actually modelled.
	//
	// Two different coverage figures, deliberately separate. `skipped`
	// is the WALK declining a function and is the one this gate owns.
	// `liftFailed` is `ssa.LiftFromIR` declining one, which is #7803 and
	// is not the walk's to fix — but it bounds what any answer here can
	// cover, so it is floored too. It is much worse after the pass
	// battery than before it (360 against 37): the battery produces
	// value-typed `OpBlock`s, which the lift refuses outright, so the
	// program the backend emits is markedly harder to lift than the raw
	// lowering every existing lift measurement was taken on.
	if skipped > funcs/20 {
		t.Errorf("the walk declined %d of %d lifted functions — a low flag rate over a walk "+
			"that gave up is not evidence of anything", skipped, funcs)
	}
	if cov := 100 * float64(funcs) / float64(funcs+liftFailed); cov < 55 {
		t.Errorf("only %.2f%% of functions lifted (%d of %d) — below this the corpus answer "+
			"describes a minority of the program", cov, funcs, funcs+liftFailed)
	}
}

// cleanCensusFixtures reads the census pin and returns the fixtures it
// records as leaking nothing.
//
// Reading the committed file rather than re-running the census keeps
// this test independent of a toolchain: the oracle is already measured
// and banked, and re-deriving it here would make one test's failure
// mean two different things.
func cleanCensusFixtures(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "conformance-leak-census.txt"))
	if err != nil {
		t.Fatalf("open the leak census pin: %v", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("malformed census row %q", line)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("malformed census count in %q: %v", line, err)
		}
		if n == 0 {
			out = append(out, fields[0])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// lowerFixtureForCertify lowers one fixture the way the census emits it
// — x86-64, reclaim on — and stops short of codegen.
//
// `ast.RcFreeEnabled` is what puts the releases in the op stream at all,
// so lowering without it would hand the walk a program with nothing to
// balance and every allocation would read as a leak. That is a config
// difference the walk cannot see, which is why it is set here rather
// than assumed.
func lowerFixtureForCertify(t *testing.T, name string) (*ir.Program, bool) {
	t.Helper()
	main := filepath.Join(conformanceCases, name, "main.fern")
	p, _, err := modload.Load(main)
	if err != nil {
		return nil, false
	}
	if err := constfold.Fold(p, nil); err != nil {
		return nil, false
	}
	info, err := checker.Check(p)
	if err != nil {
		return nil, false
	}
	if err := monomorph.Run(p, info); err != nil {
		return nil, false
	}
	treeshake.Run(p, info)

	ast.CodegenMu.Lock()
	defer ast.CodegenMu.Unlock()
	prevTwoWord, prevRc := ast.TwoWordOverride, ast.RcFreeEnabled
	ast.TwoWordOverride, ast.RcFreeEnabled = false, true
	defer func() { ast.TwoWordOverride, ast.RcFreeEnabled = prevTwoWord, prevRc }()

	ip, err := ir.LowerWith(p, info, 8, ir.DynSupported(), ir.DynRcSupported())
	if err != nil {
		return nil, false
	}
	nativePassBattery(ip)
	return ip, true
}

// nativePassBattery mirrors the IR passes `x86_64.emitCollecting` runs
// between lowering and codegen.
//
// It is here because the oracle observes the program the BACKEND emits,
// and the passes below change what there is to own. The one that showed
// this was `InlineZeroCaptureClosures`: a zero-capture closure passed as
// a function argument is rewritten to `OpConstFunc`, a static `.rodata`
// cell, so the 32-byte `__fern_alloc_rc1` block the raw lowering builds
// does not exist in the emitted program at all. Walking the raw lowering
// reported 419 closures as leaked, every one of them an object the
// backend had already deleted — a difference between two programs, read
// as a defect in the analysis.
//
// Duplicating the order is the weak part of this and it is deliberate
// rather than overlooked: the alternative is a shared entry point in
// `internal/ir`, which is the right shape and is a refactor of a hot
// backend path with byte-identical-output risk. Recorded as the
// follow-up in `docs/rc-log/`.
func nativePassBattery(ip *ir.Program) {
	ir.TailCallOptimize(ip)
	ir.Inline(ip)
	ir.Defunctionalise(ip, 8)
	ir.ElideClosurePair(ip, 8)
	ir.InlineZeroCaptureClosures(ip)
	ir.Inline(ip)
	ir.FuseTee(ip)
	ir.FlattenBranches(ip)
	ir.EliminateDeadCode(ip)
	ir.OptimizeCleanup(ip)
	cullDeadFuncs(ip)
}

// cullDeadFuncs drops the functions the backend does not emit.
//
// Without it the walk reports findings in code the binary does not
// contain, and the oracle — which observes a running program — cannot
// contradict them. It is not a filter over the findings: a function
// nothing calls is not part of the program the census measured.
func cullDeadFuncs(ip *ir.Program) {
	var extras []string
	for _, vt := range ip.Vtables {
		for _, m := range vt.Methods {
			extras = append(extras, m.Func)
		}
		if vt.Drop != "" {
			extras = append(extras, vt.Drop)
		}
	}
	for _, fn := range ip.Funcs {
		if strings.HasPrefix(fn.Name, "__drop_dyn_") {
			extras = append(extras, fn.Name)
		}
	}
	live := ir.LiveFunctionsWithAliases(ip, ir.CodegenAliases, extras...)
	if live == nil {
		return
	}
	kept := ip.Funcs[:0]
	for _, fn := range ip.Funcs {
		if live[fn.Name] {
			kept = append(kept, fn)
		}
	}
	ip.Funcs = kept
}

// liftErrClass reduces a lift failure to the shape that caused it, so
// the histogram groups rather than listing one row per op index.
func liftErrClass(msg string) string {
	msg = strings.TrimPrefix(msg, "ssa.LiftFromIR: ")
	if i := strings.Index(msg, " at op["); i >= 0 {
		return msg[:i]
	}
	if i := strings.Index(msg, ":"); i >= 0 {
		return msg[:i]
	}
	return msg
}

// topCounts renders the n largest entries of a histogram.
func topCounts(m map[string]int, n int) string {
	type row struct {
		k string
		v int
	}
	rows := make([]row, 0, len(m))
	for k, v := range m {
		rows = append(rows, row{k, v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].v != rows[j].v {
			return rows[i].v > rows[j].v
		}
		return rows[i].k < rows[j].k
	})
	if len(rows) > n {
		rows = rows[:n]
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s x%d", r.k, r.v))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

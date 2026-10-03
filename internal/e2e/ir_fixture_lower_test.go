package e2e

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/treeshake"
)

// lowerFixtureForCertify lowers one fixture the way the native x86-64
// backend emits it — reclaim on — and stops short of codegen.
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
// It is here because the program the BACKEND emits is what an analysis
// must be measured on, and the passes below change what there is to own. The one that showed
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
	ir.EliminateDeadCode(ip)
	ir.FlattenBranches(ip)
	ir.OptimizeCleanup(ip)
	cullDeadFuncs(ip)
}

// cullDeadFuncs drops the functions the backend does not emit.
//
// Without it an analysis reports on code the binary does not contain. It
// is not a filter over findings: a function nothing calls is not part of
// the program.
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

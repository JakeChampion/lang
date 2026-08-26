package lint

import (
	"fmt"
	"strconv"

	"github.com/jakechampion/lang/internal/ast"
)

// DefaultMaxComplexity is the threshold the rule reports above when nothing
// configures it. Ten is McCabe's own recommendation and what most linters
// that ship this check settle on; a function past it has more independent
// paths than a reader can hold at once, and more than a test suite is
// likely to cover.
const DefaultMaxComplexity = 10

func init() { register(func() Rule { return &Complexity{Max: DefaultMaxComplexity} }) }

// Complexity reports functions whose cyclomatic complexity exceeds Max.
//
// Cyclomatic complexity is McCabe's count of linearly independent paths
// through a function: one for the entry path, plus one for every point the
// control flow can fork. What counts as a fork is the whole content of the
// metric, so this rule pins it explicitly rather than leaving it to a
// traversal's accidents — see Score.
type Complexity struct {
	// Max is the highest complexity a function may have without being
	// reported. A function scoring exactly Max is fine.
	Max int
}

func (*Complexity) Name() string { return "cyclomatic-complexity" }

func (*Complexity) Doc() string {
	return "function has more independent control-flow paths than the configured limit"
}

func (*Complexity) DefaultSeverity() Severity { return Warn }

func (c *Complexity) Options() map[string]string {
	return map[string]string{"max": strconv.Itoa(c.Max)}
}

func (c *Complexity) SetOption(key, value string) error {
	if key != "max" {
		return fmt.Errorf("unknown option %q (want max)", key)
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("max: %q is not a number", value)
	}
	if n < 1 {
		return fmt.Errorf("max: must be at least 1, got %d", n)
	}
	c.Max = n
	return nil
}

func (c *Complexity) Check(p *Pass) {
	for _, fn := range p.Prog.Funcs {
		if fn.Body == nil {
			// A body-less decl is a WIT import binding — no control
			// flow of its own to score.
			continue
		}
		score := Score(fn)
		if score <= c.Max {
			continue
		}
		p.Report(fn.Pos(),
			fmt.Sprintf("function `%s` has a cyclomatic complexity of %d, over the limit of %d", DisplayName(fn), score, c.Max),
			"split out the branch-heavy part, or raise the limit for this site with `// fern-lint: allow cyclomatic-complexity`")
	}
}

// DisplayName is how a function is named in a finding: `Type.method` for a
// method, the plain name otherwise. The checker's mangled `__method_T_m`
// spelling is an implementation detail no report should leak, and the
// receiver is still present at parse time, which is where lints run.
func DisplayName(fn *ast.FuncDecl) string {
	if fn.Receiver != nil {
		if st, ok := fn.Receiver.Type.(ast.StructType); ok && st.Name != "" {
			return st.Name + "." + fn.Name
		}
	}
	if fn.MethodRecv != "" && fn.MethodSimpleName != "" {
		return fn.MethodRecv + "." + fn.MethodSimpleName
	}
	return fn.Name
}

// Score computes fn's cyclomatic complexity: 1 for the entry path plus one
// per fork below. A function with no branching scores 1.
//
// What forks, and what deliberately does not:
//
//	if / if-expression      +1 each; an `else` is not a fork of its own,
//	                        and an `else if` nests a second If, so the
//	                        chain counts once per condition on its own.
//	while / for / foreach   +1 — the loop's exit test.
//	loop                    +1 — an unconditional loop still adds a back
//	                        edge, and its exits are `break`s inside.
//	match arm               +1 per arm that can fail to match; a `_`
//	                        wildcard is the fall-through, not a test.
//	match guard (`when`)    +1 — a second condition after the pattern.
//	&& / ||                 +1 each — short-circuiting is a branch.
//	?                       +1 — an early return on the error path.
//
//	assert(…)               0. It desugars to an If, but it asserts a
//	                        precondition rather than steering the reader
//	                        down a second path, and `-O` deletes it.
//	todo                    0. Its `loop { … }` desugar is a stub marker.
//	break / continue /      0. They leave a path the fork that created it
//	return                  already counted.
//
// A lambda's body counts INTO its enclosing function. An inline closure is
// code the reader walks through on the way past, so hiding a branch behind
// one should not make the function look simpler than it reads.
func Score(fn *ast.FuncDecl) int {
	n := 1
	ast.Walk(fn, func(node ast.Node) bool {
		n += forks(node)
		return true
	})
	return n
}

// forks is how much one node adds to the score. Split out from Score so the
// per-node model is one readable table and so tests can pin single nodes.
func forks(node ast.Node) int {
	switch n := node.(type) {
	case *ast.If:
		if n.IsAssert {
			return 0
		}
		return 1
	case *ast.IfExpr:
		return 1
	case *ast.While, *ast.For, *ast.ForEach:
		return 1
	case *ast.Loop:
		if n.IsTodo {
			return 0
		}
		return 1
	case *ast.Binary:
		if n.Op == "&&" || n.Op == "||" {
			return 1
		}
		return 0
	case *ast.TryOp:
		return 1
	case *ast.Match:
		return armForks(n.Arms)
	case *ast.MatchExpr:
		return exprArmForks(n.Arms)
	}
	return 0
}

// armForks scores a statement match's arms. Guards are counted here rather
// than as a Binary-style expression fork because a guard's fork is the arm
// falling through to the next one, which the guard expression itself does
// not show.
func armForks(arms []*ast.MatchArm) int {
	n := 0
	for _, a := range arms {
		if !a.IsWildcard {
			n++
		}
		if a.Guard != nil {
			n++
		}
	}
	return n
}

func exprArmForks(arms []*ast.MatchExprArm) int {
	n := 0
	for _, a := range arms {
		if !a.IsWildcard {
			n++
		}
		if a.Guard != nil {
			n++
		}
	}
	return n
}

// An independent well-formedness checker for the IR.
//
// The point is that this is NOT the compiler. Every backend trusts the
// IR it is handed, so a lowering bug becomes machine code that is
// structurally wrong — a local index past the end of the frame, a
// branch depth that leaves the enclosing scope, a call with the wrong
// argument count — and the first symptom is a SIGSEGV or a silent
// miscompile several stages downstream. The closure-dispatch cluster
// (#5001 / #5007 / #5009 / #5026) is the worked example: IR that lowered
// without complaint and crashed at run time.
//
// Verify turns that class into an error that names the malformed op. It
// is the same idea as LLVM's verifier, or the external kernel checkers
// that re-validate what Lean's kernel emitted: a second, simpler program
// whose only job is to disbelieve the first.
//
// This file checks STRUCTURE — the invariants a backend would have to
// re-derive to emit anything at all. Its sibling verifystack.go checks
// operand-stack discipline, the wasm-validator half. Verify runs both.
package ir

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/caps"
)

// isBuiltin reports whether name is a user-callable builtin the backends
// provide. internal/caps is the authoritative inventory — every builtin
// must appear in BuiltinCaps (capability-gated) or Ungated, and its own
// completeness tests enforce that — so consulting it here means a new
// builtin cannot quietly become an unverifiable callee.
func isBuiltin(name string) bool {
	if _, ok := caps.BuiltinCaps[name]; ok {
		return true
	}
	return caps.Ungated[name]
}

// Problem is one violation, located precisely enough to fix.
type Problem struct {
	Func string // function the op belongs to
	Op   int    // index into Func.Ops, or -1 for a whole-function problem
	Kind OpKind
	Msg  string
}

func (p Problem) Error() string {
	if p.Op < 0 {
		return fmt.Sprintf("%s: %s", p.Func, p.Msg)
	}
	return fmt.Sprintf("%s: op %d (%s): %s", p.Func, p.Op, p.Kind, p.Msg)
}

// Verify checks every function in the program and returns every problem
// found, in program order, along with how much of the program the stack
// half could model. No problems means the IR is sound as far as these
// two passes can tell — not that it is correct.
//
// The Coverage is not optional decoration. The structural half applies
// to every function, but the stack half skips whatever it cannot model
// rather than guessing (see verifystack.go), so an empty problem list
// means nothing without knowing how much was looked at.
func Verify(p *Program) ([]Problem, Coverage) {
	known := map[string]*Func{}
	for _, f := range p.Funcs {
		known[f.Name] = f
	}
	externNames := map[string]bool{}
	externs := map[string]*ExternFunc{}
	for _, e := range p.Externs {
		externNames[e.Name] = true
		externs[e.Name] = e
	}

	var out []Problem
	cov := Coverage{Funcs: len(p.Funcs)}
	for _, f := range p.Funcs {
		out = append(out, verifyFunc(f, known, externNames)...)

		stackProblems, bail := verifyStack(f, known, externs, p.PtrW)
		if bail != "" {
			cov.skip(f.Name, bail)
			continue
		}
		cov.Modelled++
		out = append(out, stackProblems...)
	}
	return out, cov
}

// ctrl is one open structured-control scope.
type ctrl struct {
	kind   OpKind // OpBlock, OpLoop or OpIf
	at     int    // op index that opened it
	elseAt int    // op index of its OpElse, or -1
}

func verifyFunc(f *Func, known map[string]*Func, externs map[string]bool) []Problem {
	var out []Problem
	report := func(i int, k OpKind, format string, args ...any) {
		out = append(out, Problem{Func: f.Name, Op: i, Kind: k, Msg: fmt.Sprintf(format, args...)})
	}

	// Locals are one flat index space: params, then declared locals,
	// then the lowering pass's synthetic scratch slots.
	nLocals := len(f.Params) + len(f.Locals) + len(f.ScratchTypes)

	var stack []ctrl
	for i, op := range f.Ops {
		switch op.Kind {
		case OpLoadLocal, OpStoreLocal, OpTeeLocal:
			if op.I32 < 0 || int(op.I32) >= nLocals {
				report(i, op.Kind, "local index %d is outside the frame (%d params + %d locals + %d scratch = %d slots)",
					op.I32, len(f.Params), len(f.Locals), len(f.ScratchTypes), nLocals)
			}

		case OpBlock, OpLoop, OpIf:
			stack = append(stack, ctrl{kind: op.Kind, at: i, elseAt: -1})

		case OpElse:
			switch {
			case len(stack) == 0:
				report(i, op.Kind, "else with no open scope")
			case stack[len(stack)-1].kind != OpIf:
				report(i, op.Kind, "else closes a %s opened at op %d, not an if",
					stack[len(stack)-1].kind, stack[len(stack)-1].at)
			case stack[len(stack)-1].elseAt >= 0:
				report(i, op.Kind, "second else for the if opened at op %d (first at op %d)",
					stack[len(stack)-1].at, stack[len(stack)-1].elseAt)
			default:
				stack[len(stack)-1].elseAt = i
			}

		case OpEnd:
			if len(stack) == 0 {
				report(i, op.Kind, "end with no open scope")
				continue
			}
			stack = stack[:len(stack)-1]

		case OpBr, OpBrIf:
			// A branch names a scope by RELATIVE depth: 0 is the
			// innermost open scope. Depth == len(stack) targets the
			// implicit function-body scope, which is how a lowered
			// `return`-shaped branch leaves the outermost block; anything
			// beyond that has no target at all, and a backend will emit a
			// jump to whatever happens to be there.
			if op.I32 < 0 || int(op.I32) > len(stack) {
				report(i, op.Kind, "branch depth %d has no target — only %d scopes are open",
					op.I32, len(stack))
			}

		case OpCallDirect, OpCallDirectPair:
			if op.I32 < 0 {
				// Report the impossible count and stop: an arity
				// comparison against it would only add noise.
				report(i, op.Kind, "negative argument count %d", op.I32)
				continue
			}
			callee, isDefined := known[op.Str]
			switch {
			case op.Str == "":
				report(i, op.Kind, "call with no callee name")
			case isDefined:
				// The arg count is the immediate; a closure target also
				// receives its environment pointer, so the callee may
				// declare one more parameter than the call site pushes.
				if n := int(op.I32); n != len(callee.Params) && n != len(callee.Params)-1 {
					report(i, op.Kind, "calls %s with %d args, but it declares %d parameters",
						op.Str, n, len(callee.Params))
				}
			case externs[op.Str]:
				// A body-less @import; its arity is checked at the WIT seam.
			case isBuiltin(op.Str):
				// A backend-provided builtin (print, map_new, now_ns, …).
			case strings.HasPrefix(op.Str, "__"):
				// A runtime helper the backend provides rather than the
				// program. Reserved prefix, so this cannot hide a typo in a
				// user-defined name.
			default:
				report(i, op.Kind, "calls %q, which is not a defined function, an extern, a builtin, or a __-prefixed runtime helper",
					op.Str)
			}

		case OpCallIndirect, OpCallDyn, OpCallClosureDirect:
			if op.I32 < 0 {
				report(i, op.Kind, "negative argument count %d", op.I32)
			}
		}
	}

	for _, c := range stack {
		out = append(out, Problem{
			Func: f.Name, Op: c.at, Kind: c.kind,
			Msg: "scope is never closed — the function ends with it still open",
		})
	}
	return out
}

// FormatProblems renders problems for a test failure, capped so a
// systemic break reports a readable sample rather than thousands of
// lines.
func FormatProblems(ps []Problem, max int) string {
	sorted := append([]Problem(nil), ps...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].Func != sorted[b].Func {
			return sorted[a].Func < sorted[b].Func
		}
		return sorted[a].Op < sorted[b].Op
	})
	var b strings.Builder
	for i, p := range sorted {
		if i == max {
			fmt.Fprintf(&b, "\n    … and %d more", len(sorted)-max)
			break
		}
		fmt.Fprintf(&b, "\n    %s", p.Error())
	}
	return b.String()
}

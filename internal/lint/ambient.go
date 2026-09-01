package lint

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

func init() { register(func() Rule { return &AmbientCapability{} }) }

// AmbientCapability reports a handler that reaches a host effect around the
// `Platform` bag it was handed.
//
// A handler's second parameter is its capability bag
// (docs/PLATFORM-RESEARCH.md Rec §1): `std/platform` puts the log sink, the
// clock, the environment, and entropy on it as methods, so what a handler
// can reach is what it was given. The free functions still resolve inside a
// handler body — nothing in the type system stops `eprint` — and a handler
// that calls one has an effect no caller can substitute, which is what
// breaks mock platforms in tests and per-target capability sets.
//
// Scope is the handler body itself. The rule works off the parse tree with
// no call graph, so an effect inside a helper the handler calls is out of
// reach — that is also the shape whose fix is to pass the bag along, which
// this rule could not tell had been done.
type AmbientCapability struct{}

func (*AmbientCapability) Name() string { return "ambient-capability" }

func (*AmbientCapability) Doc() string {
	return "handler reaches a host effect around the capability bag it was given"
}

func (*AmbientCapability) DefaultSeverity() Severity { return Warn }

// bagMethods maps an ambient builtin to the `std/platform` method that is
// the same effect through the bag. Only exact equivalents belong here: a
// suggestion that changes what the call DOES (a different clock, a
// different stream) is worse than no suggestion at all.
var bagMethods = map[string]string{
	"eprint":       "log",
	"now_unix_ms":  "now_ms",
	"monotonic_ns": "elapsed_ns",
	"env":          "env",
	"random_i32":   "random_i32",
}

func (a *AmbientCapability) Check(p *Pass) {
	for _, fn := range p.Prog.Funcs {
		bag, ok := handlerBag(fn)
		if !ok {
			continue
		}
		shadowed := boundNames(fn)
		ast.Walk(fn, func(node ast.Node) bool {
			call, ok := node.(*ast.Call)
			if !ok {
				return true
			}
			id, ok := call.Callee.(*ast.Ident)
			if !ok {
				return true
			}
			method, ok := bagMethods[id.Name]
			if !ok || shadowed[id.Name] {
				return true
			}
			args := "(…)"
			if len(call.Args) == 0 {
				args = "()"
			}
			p.Report(id.Pos(),
				fmt.Sprintf("handler `%s` calls `%s` directly instead of through its `%s` capability bag", fn.Name, id.Name, bag),
				fmt.Sprintf("call `%s.%s%s` (`import \"std/platform\";`) so the effect comes from the platform the handler was handed", bag, method, args),
				0)
			return true
		})
	}
}

// handlerBag reports the name of fn's capability-bag parameter, and whether
// fn is a handler at all: a top-level `handle` whose second parameter is a
// `Platform`. Anything else — a helper, a `main`, a `handle` of some
// unrelated shape — has no bag to route an effect through, so the rule has
// nothing to suggest.
func handlerBag(fn *ast.FuncDecl) (string, bool) {
	if fn.Name != "handle" || fn.Receiver != nil || fn.Body == nil || len(fn.Params) < 2 {
		return "", false
	}
	st, ok := fn.Params[1].Type.(ast.StructType)
	if !ok || st.Name != "Platform" {
		return "", false
	}
	return fn.Params[1].Name, true
}

// boundNames is every name the handler binds itself — parameters, locals,
// lambda parameters. A function-valued local named after a builtin is the
// one way `eprint(…)` in a handler body is not the builtin at all, and the
// parse tree carries no resolution to ask.
func boundNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	for _, p := range fn.Params {
		out[p.Name] = true
	}
	ast.Walk(fn, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Var:
			out[n.Name] = true
		case *ast.Lambda:
			for _, p := range n.Params {
				out[p.Name] = true
			}
		}
		return true
	})
	return out
}

// BagMethods is the builtin-to-method table the rule reports against,
// copied so a caller cannot edit the rule's own. The tests use it to pin
// the suggestions against `std/platform`'s method list.
func BagMethods() map[string]string {
	out := make(map[string]string, len(bagMethods))
	for builtin, method := range bagMethods {
		out[builtin] = method
	}
	return out
}

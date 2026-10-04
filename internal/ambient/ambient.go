// Package ambient is the ambient-effect rule (E080): a function handed a
// platform reaches every host effect through the platform, never around
// it.
//
// The bag is the handler's whole capability surface (docs/PLATFORM-RESEARCH.md
// Rec §1): `std/platform` puts the log sink, the clocks, the environment and
// entropy on it as methods, and std/fetch adds outbound HTTP. The free
// functions still resolve inside a handler body, so nothing in the type system
// stops a bare `eprint` — and a handler that calls one has an effect no caller
// can substitute, which is what breaks a mock platform in a test and what
// std/sim cannot script. The rule is what makes the bag a boundary: a handler
// that compiles is a handler that simulates.
//
// It runs over the call graph internal/effects builds, so an effect inside a
// helper the handler calls is charged to the handler, with the call chain in
// the message. A handler is any top-level function with a parameter of type
// `platform.Platform`, found by type rather than by position or by the name `handle`.
// A platform's own methods (`Host`'s `log` and its siblings) are the
// sanctioned route, so the walk stops at them.
//
// The walk follows named calls, methods and function values named in a body,
// the same over-approximation the tree-shaker uses; a call through a value
// whose target the walk cannot name is not charged. That is the shape the
// self-host mirror (examples/self_host/ambient.fern) computes too, so both
// compilers refuse the same programs.
package ambient

import (
	"fmt"
	"github.com/jakechampion/lang/internal/checker"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/effects"
	"github.com/jakechampion/lang/internal/platforms"
)

// Violation is one host effect a handler reaches around its bag: the
// capability, the builtin that carries it, and the call chain from the
// handler down to that builtin (function names as the combined program
// spells them, so modload-mangled for imported modules).
type Violation struct {
	Handler    string
	Bag        string
	Builtin    string
	Capability string
	Chain      []string
	Pos        ast.Position
	FuncModule string
}

// bagMethods maps an ambient builtin to the `std/platform` method that is
// the same effect through the bag. Only exact equivalents belong here: a
// suggestion that changes what the call DOES (a different clock, a
// different stream) is worse than no suggestion at all.
var bagMethods = map[string]string{
	"eprint":       "log",
	"now_unix_ms":  "now_ms",
	"monotonic_ns": "elapsed_ns",
	"env":          "env",
	"config_get":   "config",
	"random_i32":   "random_i32",
}

// BagMethods returns the builtin-to-method table the rule suggests
// against, copied so a caller cannot edit the rule's own. The tests pin
// it against `std/platform`'s method list.
func BagMethods() map[string]string {
	out := make(map[string]string, len(bagMethods))
	for builtin, method := range bagMethods {
		out[builtin] = method
	}
	return out
}

// Message renders the violation's text without a position. entryModule is
// the module path modload stamps on the entry file's functions; a handler
// declared elsewhere says so, since its position cannot index the file
// being reported on.
func (v Violation) Message(entryModule string) string {
	where := ""
	if v.FuncModule != "" && v.FuncModule != entryModule {
		where = fmt.Sprintf(" (declared in module %q)", v.FuncModule)
	}
	fix := "nothing on the bag stands in for it"
	if method, ok := bagMethods[v.Builtin]; ok {
		fix = fmt.Sprintf("call `%s.%s(…)` (`import \"std/platform\";`) so the effect comes from the platform the handler was handed", v.Bag, method)
	}
	return fmt.Sprintf("handler `%s`%s reaches `%s` (`%s`) around its platform `%s`: %s; %s",
		v.Handler, where, v.Builtin, v.Capability, v.Bag, strings.Join(v.Chain, " -> "), fix)
}

// BagParam reports the name of fn's platform parameter, and whether fn is a
// handler at all. A platform's own methods are not handlers: they are the
// route the rule sends a handler down.
func BagParam(fn *ast.FuncDecl) (string, bool) {
	if IsBagMethod(fn) {
		return "", false
	}
	for _, p := range fn.Params {
		if checker.IsPlatformParam(fn, p) {
			return p.Name, true
		}
	}
	return "", false
}

// IsBagMethod reports whether fn is a method of a `platform.Platform`
// implementation.
func IsBagMethod(fn *ast.FuncDecl) bool {
	return fn.ImplTrait == "platform__Platform"
}

// Enforce reports every host effect a handler in prog reaches around its
// bag: one violation per (handler, capability), naming the builtin and the
// shortest call chain that reaches it. prog must have been through
// checker.Check, which is when method calls resolve to their hoisted names.
// Results are in handler declaration order, then capability order.
func Enforce(prog *ast.Program) []Violation {
	table := platforms.GatedBuiltins()
	g := effects.Build(prog, func(name string) bool {
		_, gated := table[name]
		return gated
	})
	// The bag's methods are leaves: what they reach is the effect the
	// handler was given. A call the walk cannot name charges nothing (see
	// the package comment), so the indirect row is emptied too.
	for name, fn := range g.Funcs {
		if IsBagMethod(fn) {
			delete(g.Edges, name)
			delete(g.Builtins, name)
		}
	}
	g.Indirect = map[string]bool{}
	g.Escaping = map[string]bool{}
	g.EscapingBuiltins = nil
	sol := effects.Solve(g, table)

	var out []Violation
	for _, name := range g.Order {
		fn := g.Funcs[name]
		bag, ok := BagParam(fn)
		if !ok || fn.Body == nil {
			continue
		}
		row := sol.Rows[name]
		if row == 0 {
			continue
		}
		chains := effects.Witness(g, sol, name, row)
		sort.SliceStable(chains, func(i, j int) bool { return chains[i].Label < chains[j].Label })
		for _, c := range chains {
			out = append(out, Violation{
				Handler:    name,
				Bag:        bag,
				Builtin:    c.Path[len(c.Path)-1],
				Capability: c.Label,
				Chain:      c.Path,
				Pos:        fn.P,
				FuncModule: fn.BodyModule(),
			})
		}
	}
	return out
}

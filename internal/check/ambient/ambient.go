package ambient

import (
	"fmt"
	"github.com/jakechampion/lang/internal/check/checker"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/check/effects"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

type Violation struct {
	Handler    string
	Bag        string
	Builtin    string
	Capability string
	Chain      []string
	Pos        ast.Position
	FuncModule string
}

var bagMethods = map[string]string{
	"eprint":       "log",
	"now_unix_ms":  "now_ms",
	"monotonic_ns": "elapsed_ns",
	"env":          "env",
	"config_get":   "config",
	"random_i32":   "random_i32",
	"sleep_ms":     "sleep",
	"sleep_ns":     "sleep",
	"stat":         "stat",
}

func BagMethods() map[string]string {
	out := make(map[string]string, len(bagMethods))
	for builtin, method := range bagMethods {
		out[builtin] = method
	}
	return out
}

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

func IsBagMethod(fn *ast.FuncDecl) bool {
	return fn.ImplTrait == "platform__Platform"
}

func Enforce(prog *ast.Program) []Violation {
	table := platforms.GatedBuiltins()
	g := effects.Build(prog, func(name string) bool {
		_, gated := table[name]
		return gated
	})
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

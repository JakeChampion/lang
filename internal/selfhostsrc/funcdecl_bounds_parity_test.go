package selfhostsrc

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// `bound_traits` runs PARALLEL to `type_params` — examples/self_host/parser.fern
// states it on the field: "bound_traits[i] holds the bound for type_params[i]".
// A rebuild that carries one and clears the other leaves a decl still declaring
// `T` while claiming it is unbounded.
//
// Eight such rebuilds accumulated (#7181, after #7173 fixed the first). None was
// observable at the time: the two readers — the checker's E021 call-site bound
// rule and the printer's `[T: Ord]` rendering — both run BEFORE these sites. In
// the CLI, `check_module` and `annotate_module` run at fern.fern:2216 and :2276,
// while `module_with_builtins` (which runs the parse-time defer lowering, one of
// the offenders) runs at :2333; `-fmt` uses parse_module_verbatim and reaches
// none of them. So the bug was real, latent, and untestable by behaviour — it
// would surface the day a reader moved later in the pipeline, as a bounded
// generic silently accepted where it should be rejected.
//
// That is what this test is for. It reads the Fern source as data and pins the
// INVARIANT rather than an observable, so a ninth such rebuild fails here
// instead of waiting for a pipeline reorder to turn it into a wrong answer. It
// deliberately builds nothing: the price has to be low enough to run on every
// change to these files.
//
// The fix shape is struct spread — `FuncDecl { ...fn, body: … }` cannot drop the
// next field either, which is the property worth keeping. A rebuild that
// legitimately has no type params (a monomorphised clone) spells
// `type_params: notp` next to `bound_traits: notp`, where the pairing is
// visible and deliberate; that is fine, and so is carrying both.
//
// The pairing breaks in TWO directions, and the second one is not the mirror of
// the first in the source text, so it needs its own check: `clone_bg` cleared
// `type_params` for a monomorphised clone while keeping the generic's
// `bound_traits`, leaving bounds indexed against parameters that no longer
// exist. A test that only looks at literals carrying `type_params` forward is
// blind to it by construction.
//
// Matching the literal alone was not enough: `finalize_impl_method` laundered
// `type_params` through a local and lost the impl block's bounds unseen (#7224).
// So a `type_params: <local>` counts as carrying a declaration too, when that
// local is declared from one in the same function. One level only — that is the
// shape the code actually uses, and chasing further would need real dataflow.
var selfHostSources = []string{
	"../../examples/self_host/parser.fern",
	"../../examples/self_host/irlower.fern",
	"../../examples/self_host/constfold.fern",
}

// carriesTypeParams matches `type_params: <ident>.type_params`, i.e. a rebuild
// carrying an EXISTING declaration's type parameters forward directly. A fresh
// list (`type_params: notp`) does not match.
var carriesTypeParams = regexp.MustCompile(`type_params:\s*\w+\.type_params`)

// clearsTypeParams matches a rebuild that writes a FRESH type-param list —
// `type_params: notp`, the monomorphised-clone shape — rather than carrying one
// forward. `[]` counts too; `<ident>.type_params` does not. A bare `\w+` also
// matches a local DERIVED from a declaration, which is not fresh at all, so the
// inverse check below pairs this with readsDerivedLocal.
var clearsTypeParams = regexp.MustCompile(`type_params:\s*(\[\]|\w+)(?:,|\s|$)`)

// carriesBoundTraits matches `bound_traits: <ident>.bound_traits`, a bound list
// taken off an existing declaration.
var carriesBoundTraits = regexp.MustCompile(`bound_traits:\s*\w+\.bound_traits`)

// typeParamsLocal captures the local a literal reads its type params from, for
// the indirect case: `type_params: mtps`.
var typeParamsLocal = regexp.MustCompile(`type_params:\s*(\w+)\s*[,}]`)

// derivedLocal matches a local declared FROM a declaration's type params —
// `var mtps: string[] = m.type_params;`. Such a local carries the declaration
// forward just as the field access does.
var derivedLocal = regexp.MustCompile(`var\s+(\w+)\s*:\s*string\[\]\s*=\s*\w+\.type_params`)

// fernFuncStart matches a top-level Fern function header, used to bound the
// search for a derived local to the function the literal sits in — two
// functions may both spell a local `mtps` while only one derives it.
var fernFuncStart = regexp.MustCompile(`^(pub )?function\b`)

// funcDeclOpen matches the opening of a `FuncDecl { … }` literal, qualified or
// not. The match index is where the literal starts, so braces earlier on the
// line do not enter the balance.
var funcDeclOpen = regexp.MustCompile(`\bFuncDecl\s*\{`)

func TestFuncDeclRebuildsKeepBoundTraits(t *testing.T) {
	for _, path := range selfHostSources {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			// A function header ends `): parser.FuncDecl {` — same characters as
			// a literal, but the brace opens a body, so scanning from it swallows
			// every literal inside and reports them a second time.
			if strings.HasPrefix(strings.TrimSpace(line), "function ") {
				continue
			}
			loc := funcDeclOpen.FindStringIndex(line)
			if loc == nil {
				continue
			}
			lit, end := funcDeclLiteral(lines, i, loc[0])
			derived := readsDerivedLocal(lines, i, lit)
			// The INVERSE desync: a rebuild that writes a fresh type-param list
			// but keeps the source's bounds. `clone_bg` did this — a
			// monomorphised clone has no type params, so the generic's bounds
			// are indexed against parameters that no longer exist. The check
			// below cannot see it: it only looks at literals that CARRY
			// type_params forward. A derived local is excluded because it is not
			// a fresh list — it is the indirect form of carrying one.
			if clearsTypeParams.MatchString(lit) && !derived && carriesBoundTraits.MatchString(lit) {
				t.Errorf("%s:%d-%d: this FuncDecl rebuild writes a fresh `type_params` but keeps "+
					"`bound_traits` from the source declaration, so the bounds are indexed against "+
					"type parameters the rebuild does not have.\n"+
					"Clear both — they are parallel arrays.\n%s",
					path, i+1, end+1, lit)
			}
			if !carriesTypeParams.MatchString(lit) && !derived {
				continue
			}
			if strings.Contains(lit, "bound_traits: []") {
				t.Errorf("%s:%d-%d: this FuncDecl rebuild carries `type_params` from an existing "+
					"declaration but clears `bound_traits`, so the decl still declares its type "+
					"parameters while claiming they are unbounded.\n"+
					"Use struct spread — `FuncDecl { ...<src>, body: … }` — which carries both and "+
					"cannot drop the next field added to FuncDecl either.\n%s",
					path, i+1, end+1, lit)
			}
		}
	}
}

// readsDerivedLocal reports whether the literal takes its `type_params` from a
// local that the enclosing function declared from another declaration's — the
// indirection that hid #7224's two sites.
func readsDerivedLocal(lines []string, i int, lit string) bool {
	m := typeParamsLocal.FindStringSubmatch(lit)
	if m == nil {
		return false
	}
	for j := i; j >= 0; j-- {
		if d := derivedLocal.FindStringSubmatch(lines[j]); d != nil && d[1] == m[1] {
			return true
		}
		if fernFuncStart.MatchString(lines[j]) {
			return false
		}
	}
	return false
}

// funcDeclLiteral returns the text of the `FuncDecl { … }` literal starting at
// column `col` of line `i`, and the index of its last line. Brace-balanced
// rather than line-counted, because these literals span one to seven lines.
func funcDeclLiteral(lines []string, i, col int) (string, int) {
	var b strings.Builder
	depth := 0
	started := false
	for j := i; j < len(lines) && j < i+24; j++ {
		text := lines[j]
		if j == i {
			text = text[col:]
		}
		b.WriteString(text)
		b.WriteString("\n")
		for _, ch := range text {
			switch ch {
			case '{':
				depth++
				started = true
			case '}':
				depth--
			}
		}
		if started && depth <= 0 {
			return b.String(), j
		}
	}
	return b.String(), i
}

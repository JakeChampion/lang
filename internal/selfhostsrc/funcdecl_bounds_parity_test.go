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
// `type_params: notp` next to `bound_traits: []`, where the pairing is visible
// and deliberate; those are not flagged, because they do not carry a declaration
// forward.
//
// KNOWN BLIND SPOT: matching on the literal only, this misses a rebuild that
// launders `type_params` through a local first. `finalize_impl_method` does
// exactly that and loses the impl block's bounds — #7224, which widens this
// test to follow one level of indirection as part of its fix.
var selfHostSources = []string{
	"../../examples/self_host/parser.fern",
	"../../examples/self_host/irlower.fern",
	"../../examples/self_host/constfold.fern",
}

// carriesTypeParams matches `type_params: <ident>.type_params`, i.e. a rebuild
// carrying an EXISTING declaration's type parameters forward. A fresh list
// (`type_params: notp`) does not match.
var carriesTypeParams = regexp.MustCompile(`type_params:\s*\w+\.type_params`)

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
			if !carriesTypeParams.MatchString(lit) {
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

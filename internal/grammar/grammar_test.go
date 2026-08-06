// Unit coverage for the grammar. The differential gate proves the
// grammar derives everything the parser accepts; these snippets pin the
// constructs that were WRONG in the first draft, so a later edit that
// regresses one fails here with a one-line reproduction instead of as a
// stuck-token report on a 7000-line self-host source.
package grammar

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/lexer"
	"github.com/jakechampion/lang/internal/parser"
)

func derives(t *testing.T, g *Grammar, src string) (bool, string) {
	t.Helper()
	toks, _, err := lexer.Tokenize(src)
	if err != nil {
		t.Fatalf("lex %q: %v", src, err)
	}
	ok, stuck := g.Match(toks)
	if ok {
		return true, ""
	}
	return false, Context(toks, stuck)
}

// TestGrammarDerivesConstruct covers one construct per case. Every entry
// here failed the first draft of spec/grammar.ebnf; the comment on each
// says what the draft got wrong.
func TestGrammarDerivesConstruct(t *testing.T) {
	g := loadGrammar(t)

	cases := []struct{ name, src string }{
		// A shared '[' … ']' around an inner choice does not backtrack in
		// a PEG: `i` matched as a type argument, then '-' failed.
		{"index with arithmetic", `function main(): i32 { return arr[i - 1]; }`},
		{"slice", `function main(): i32 { return xs[0:n]; }`},
		{"explicit type args", `function main(): i32 { return pick[i32](xs, 0); }`},
		{"type args, trailing comma", `function main(): i32 { return pick[i32,](xs, 0,); }`},

		// The draft had no struct-update spread.
		{"struct update spread", `function main(): i32 { var b: P = P { ...a, x: 40 }; return 0; }`},

		// Bounds and attribute args take qualified names.
		{"qualified bound", `function eq[T: cmp.Eq + cmp.Display](a: T): i32 { return 0; }`},
		{"qualified attr arg", `@derive(cmp.Debug) struct Point { x: i32 }`},

		// A block in expression position may end in a bare expression.
		{"value block", `function main(): i32 { var x = if (c) { 1 } else { 2 }; return x; }`},
		{"value block, statements then value", `function main(): i32 { var x = if (c) { f(); 1 } else { 2 }; return x; }`},

		// `default` is a keyword, and the one keyword usable as a name.
		{"default as member", `function main(): i32 { var w: W = W.default(); return 0; }`},
		{"default as declared name", `trait Default { function default(): Self; }`},

		// `(i32, i32)[]` — array of tuples.
		{"array of tuples", `function f(): (i32, i32)[] { return xs; }`},
		{"array of tuples, local", `function main(): i32 { var p: (K, V)[] = q; return 0; }`},

		// `own` is a modifier AND an ordinary name.
		{"own as modifier", `function f(own xs: string[]): i32 { return 0; }`},
		{"own as parameter name", `function f(rl: i32, own: string[]): i32 { return 0; }`},

		// A stdlib module whose name is a primitive-type keyword.
		{"primitive-named module call", `function main(): i32 { if (string.from_codepoint(1) == "a") { return 1; } return 0; }`},

		// A destructuring arrow-lambda parameter.
		{"destructuring lambda param", `function main(): i32 { var g = ((lo, hi): (i32, i32)) => hi - lo; return 0; }`},
		{"destructuring function param", `function main(): i32 { var f = function((x, y): (i32, i32)): i32 { return x * y; }; return 0; }`},

		// A block is an expression in its own right, not only as an if/match
		// branch (docs/BLOCK-EXPRESSIONS.md). Nothing in the repo used a
		// standalone one until conformance/cases/diag_e061 existed.
		{"standalone block expression", `function main(): i32 { var x: i32 = { var a: i32 = 1; a + 1 }; return x; }`},

		// Match expressions with guards, on one line.
		{"match expr", `function main(): i32 { var a = match (p) { (1, b) => b * 10, (x, _) => x }; return a; }`},
		{"match expr with guard", `function main(): i32 { var b = match (q) { (0, y) => y, (x, y) when x == y => x + y, (x, y) => x - y }; return b; }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Guard against testing a snippet the real parser rejects:
			// that would pin the grammar to something outside the language.
			if _, err := parser.Parse(tc.src); err != nil {
				t.Fatalf("the parser rejects this snippet, so it cannot pin the grammar: %v", err)
			}
			if ok, stuck := derives(t, g, tc.src); !ok {
				t.Errorf("grammar cannot derive it, stuck at:\n    %s", stuck)
			}
		})
	}
}

// TestGrammarRejects pins the other direction on a handful of shapes.
// The grammar is a deliberate superset of the parser (see the package
// comment), so this cannot be exhaustive — but a grammar that derives
// arbitrary token soup would pass the differential gate while saying
// nothing, and these catch that.
func TestGrammarRejects(t *testing.T) {
	g := loadGrammar(t)

	cases := []struct{ name, src string }{
		{"token soup", `) } => ; ; [`},
		{"unclosed block", `function main(): i32 { return 0;`},
		{"statement outside a declaration", `return 0;`},
		{"missing semicolon", `function main(): i32 { return 0 }`},
		{"binary operator with no right operand", `function main(): i32 { return 1 + ; }`},
		{"struct field with no type", `struct P { x }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ok, _ := derives(t, g, tc.src); ok {
				t.Errorf("grammar derives %q, which is not Fern", tc.src)
			}
		})
	}
}

func TestGrammarParseErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"missing =", "Program { TopLevel } ;", "missing `=`"},
		{"undefined rule", "Program = Nope ;", "undefined rule(s): Nope"},
		{"duplicate rule", "A = 'x' ; A = 'y' ;", "defined twice"},
		{"empty alternative", "A = 'x' | ;", "empty alternative"},
		{"unclosed group", "A = ( 'x' ;", `missing ")"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

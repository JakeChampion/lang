package grammar

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/lexer"
	"github.com/jakechampion/lang/internal/syntax/parser"
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

func TestGrammarDerivesConstruct(t *testing.T) {
	g := loadGrammar(t)

	cases := []struct{ name, src string }{
		{"index with arithmetic", `function main(): i32 { return arr[i - 1]; }`},

		{"arrow lambda with a return type", `function main(): i32 { let f = (x: i32): i32 => x + 1; return 0; }`},
		{"nullary arrow lambda with a return type", `function main(): i32 { let f = (): i32 => 1; return 0; }`},
		{"arrow lambda returning a tuple", `function main(): i32 { let f = (x: i32): (i32, i32) => g(x); return 0; }`},
		{"arrow lambda returning a function type", `function main(): i32 { let f = (x: i32): (i32) => i32 => g(x); return 0; }`},
		{"arrow lambda with an owning parameter", `function main(): i32 { let f = (st: i32, own a: string[]): string[] => a; return 0; }`},
		{"arrow lambda with an owning first parameter", `function main(): i32 { let f = (own a: string[]): string[] => a; return 0; }`},
		{"slice", `function main(): i32 { return xs[0:n]; }`},

		{"if let value after a diverging branch", `function f(): i32 { let n = if let 1 = value { return 2; } else { if let 3 = value { return 4; } else { value } }; return n; }`},
		{"if let value", `function f(): i32 { return if let Some(n) = source() { n } else { 0 }; }`},
		{"if let value chain", `function f(): i32 { let n = if let Some(n) = source() { n } else if let Other(n) = source() { n } else { 0 }; return n; }`},
		{"statement match bare arms", `function f(): i32 { match (n) { 1 => print("one"), _ => print("other"), } return 0; }`},
		{"statement match value blocks", `function f(): i32 { match (n) { 1 => { let x = 1; print(x) }, _ => {} } return 0; }`},
		{"if let empty body", `function f(): i32 { if let Some(_) = value {} else { return 1; } return 0; }`},
		{"if let qualified empty body", `function f(): i32 { if let option.Some(_) = record.value {} else { return 1; } return 0; }`},
		{"if let empty body without else", `function f(): i32 { if let Some(_) = value {} return 0; }`},
		{"if let binary source empty body", `function f(): i32 { if let 1 = left + right {} else { return 1; } return 0; }`},
		{"if let unary source empty body", `function f(): i32 { if let true = !flag {} return 0; }`},
		{"if let bare branches", `function f(): i32 { if let Some(x) = value return x; else return 0; }`},
		{"if bare branches", `function f(): i32 { if (flag) return 1; else return 0; }`},
		{"for empty body", `function f(): i32 { for x in items {} return 0; }`},
		{"for qualified empty body", `function f(): i32 { for x in state.items {} return 0; }`},
		{"match empty body", `function f(): i32 { match (value) {} return 0; }`},
		{"match qualified empty body", `function f(): i32 { match (record.value) {} return 0; }`},
		{"match struct literal source", `function f(): i32 { match (Box {}) { _ => { return 1; } } return 0; }`},
		{"struct literal outside header", `function f(): i32 { let value = Box {}; return 0; }`},
		{"explicit type args", `function main(): i32 { return pick[i32](xs, 0); }`},
		{"type args, trailing comma", `function main(): i32 { return pick[i32,](xs, 0,); }`},

		{"struct update spread", `function main(): i32 { let b: P = P { ...a, x: 40 }; return 0; }`},

		{"qualified bound", `function eq[T: cmp.Eq + cmp.Display](a: T): i32 { return 0; }`},
		{"qualified attr arg", `@derive(cmp.Debug) struct Point { x: i32 }`},

		{"value block", `function main(): i32 { let x = if (c) { 1 } else { 2 }; return x; }`},
		{"value block, statements then value", `function main(): i32 { let x = if (c) { f(); 1 } else { 2 }; return x; }`},

		{"default as member", `function main(): i32 { let w: W = W.default(); return 0; }`},
		{"default as declared name", `trait Default { function default(): Self; }`},

		{"array of tuples", `function f(): (i32, i32)[] { return xs; }`},
		{"array of tuples, local", `function main(): i32 { let p: (K, V)[] = q; return 0; }`},

		{"for struct pattern", `function main(): i32 { for P { x, y } in ps { f(x, y); } return 0; }`},
		{"for struct pattern, rename and rest", `function main(): i32 { for P { x: a, .. } in ps { f(a); } return 0; }`},
		{"for @ struct pattern", `function main(): i32 { for w @ P { x, y } in ps { f(w, x, y); } return 0; }`},
		{"for @ tuple pattern", `function main(): i32 { for w @ (a, b) in ts { f(w, a, b); } return 0; }`},
		{"let @ struct destructure", `function main(): i32 { let w @ P { x, y } = p; return w.x + x + y; }`},
		{"let @ tuple destructure", `function main(): i32 { let w @ (a, b) = t; return w.0 + a + b; }`},

		{"own as modifier", `function f(own xs: string[]): i32 { return 0; }`},
		{"own as parameter name", `function f(rl: i32, own: string[]): i32 { return 0; }`},
		{"own lambda parameter", `function main(): i32 { let g: (own i32[]) => i32 = (own xs: i32[]) => xs.len(); return 0; }`},

		{"primitive-named module call", `function main(): i32 { if (string.from_codepoint(1) == "a") { return 1; } return 0; }`},

		{"destructuring lambda param", `function main(): i32 { let g = ((lo, hi): (i32, i32)) => hi - lo; return 0; }`},
		{"destructuring function param", `function main(): i32 { let f = ((x, y): (i32, i32)): i32 => { return x * y; }; return 0; }`},

		{"standalone block expression", `function main(): i32 { let x: i32 = { let a: i32 = 1; a + 1 }; return x; }`},

		{"match expr", `function main(): i32 { let a = match (p) { (1, b) => b * 10, (x, _) => x }; return a; }`},
		{"match expr with guard", `function main(): i32 { let b = match (q) { (0, y) => y, (x, y) when x == y => x + y, (x, y) => x - y }; return b; }`},

		{"char literal", `function main(): i32 { let c: char = 'x'; return 0; }`},
		{"byte literal", `function main(): i32 { let b: u8 = b'['; return 0; }`},
		{"char literal escape", `function main(): i32 { let c: char = '\u{1F600}'; return 0; }`},
		{"byte literal pattern", `function main(): i32 { match (s[0]) { b'[' => { return 1; }, _ => { return 0; } } return 0; }`},
		{"char literal pattern", `function main(): i32 { match (c) { 'a' => { return 1; }, _ => { return 0; } } return 0; }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parser.Parse(tc.src); err != nil {
				t.Fatalf("the parser rejects this snippet, so it cannot pin the grammar: %v", err)
			}
			if ok, stuck := derives(t, g, tc.src); !ok {
				t.Errorf("grammar cannot derive it, stuck at:\n    %s", stuck)
			}
		})
	}
}

func TestGrammarRejects(t *testing.T) {
	g := loadGrammar(t)

	cases := []struct{ name, src string }{
		{"visible struct field", `struct P { pub x: i32 }`},
		{"plain binding with else", `function f(): i32 { let n = 1 else { return 0; }; return n; }`},
		{"struct binding with else", `struct P { x: i32 } function f(p: P): i32 { let P { x } = p else { return 0; }; return x; }`},
		{"tuple binding with else", `function f(p: (i32, i32)): i32 { let (x, y) = p else { return 0; }; return x; }`},
		{"while without a block", `function f(): i32 { while (false) return 1; return 0; }`},
		{"loop without a block", `function f(): i32 { loop return 1; }`},
		{"for without a block", `function f(): i32 { for x in [1] return x; return 0; }`},
		{"statement match missing comma", `function f(): i32 { match (n) { 1 => {} _ => {} } return 0; }`},
		{"expression match missing comma", `function f(): i32 { return match (n) { 1 => 2 _ => 3 }; }`},
		{"if let value missing else", `function f(): i32 { return if let Some(n) = value { n }; }`},
		{"token soup", `) } => ; ; [`},
		{"unclosed block", `function main(): i32 { return 0;`},
		{"statement outside a declaration", `return 0;`},
		{"missing semicolon", `function main(): i32 { return 0 }`},
		{"binary operator with no right operand", `function main(): i32 { return 1 + ; }`},
		{"struct field with no type", `struct P { x }`},
		{"match statement missing parentheses", `function f(): i32 { match value {} return 0; }`},
		{"match expression missing parentheses", `function f(): i32 { return match value { _ => 0 }; }`},

		{"arrow lambda, untyped param", `function main(): i32 { let f = (x) => x + 1; return 0; }`},
		{"arrow lambda, untyped params", `function main(): i32 { let f = (x, y) => x; return 0; }`},

		{"single-type alias", `type Id = Point;`},
		{"break with a value", `function main(): i32 { while (true) { break 5; } return 0; }`},
		{"struct literal field shorthand", `function main(): i32 { let p: P = P { x }; return 0; }`},
		{"if expression with unbraced branches", `function main(): i32 { let a: i32 = if (true) 1 else 2; return a; }`},
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

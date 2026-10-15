package e2ecompiler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// `fern -lsp`'s references, rename (compiler/lsprefs.fern), semantic tokens
// (compiler/lsptokens.fern) and code actions (lsp.fern's code_actions, from
// the fixes the checker attaches to its diagnostics). The cases are the Go
// server's (references_test.go, the rename tests beside it,
// semantic_tokens_test.go, codeaction_test.go and workspace_test.go's
// VariantReferencesStayInTheirEnum and SemanticTokensCoverOnlyTheRequestedFile),
// with the answers written out as data. Where this server answers otherwise the
// case says so: a declaration is placed on its name, and a request on a
// declaration's name answers as one on a use; a local is the binding a name
// reads, not every like-named local of the function, and a parameter's
// declaration is one of its places; includeDeclaration is honoured; a rename
// to a name that is not an identifier, or of something with no declaration or
// declared in the stdlib, is refused; semantic tokens classify declarations
// and leave out what they cannot classify; and a code action names its
// diagnostic as the client sent it.

// refPlace is a place a references or rename answer names: a file of the
// tree, relative to it, and a range in it.
type refPlace struct {
	file string
	at   lspRange
}

func place(file string, line, c0, c1 int) refPlace {
	return refPlace{file, rng(line, c0, c1)}
}

func sortedPlaces(ps []refPlace) []refPlace {
	out := append([]refPlace{}, ps...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.file != b.file {
			return a.file < b.file
		}
		if a.at.Start.Line != b.at.Start.Line {
			return a.at.Start.Line < b.at.Start.Line
		}
		return a.at.Start.Character < b.at.Start.Character
	})
	return out
}

// relFile is the file a URI names, relative to dir.
func relFile(t *testing.T, dir, uri string) string {
	t.Helper()
	prefix := fileURI(dir) + "/"
	if !strings.HasPrefix(uri, prefix) {
		return uri
	}
	return strings.TrimPrefix(uri, prefix)
}

// refsProbe asks for the references to the name at line, char of doc and
// wants `want`, in any order.
func refsProbe(dir, name, doc string, line, char int, withDecl bool, want ...refPlace) lspProbe {
	params := at(line, char)
	params["context"] = lspMsg{"includeDeclaration": withDecl}
	return lspProbe{name: "references/" + name, method: "textDocument/references", doc: doc, params: params, check: func(t *testing.T, result json.RawMessage) {
		var locs []lspLocation
		decode(t, result, &locs)
		got := []refPlace{}
		for _, l := range locs {
			got = append(got, refPlace{relFile(t, dir, l.URI), l.Range})
		}
		if want == nil {
			want = []refPlace{}
		}
		if g, w := sortedPlaces(got), sortedPlaces(want); !reflect.DeepEqual(g, w) {
			t.Errorf("references = %+v\nwant         %+v", g, w)
		}
	}}
}

// renameProbe renames the name at line, char of doc to newName and wants an
// edit replacing each of `want` with it, or, with none, null.
func renameProbe(dir, name, doc string, line, char int, newName string, want ...refPlace) lspProbe {
	params := at(line, char)
	params["newName"] = newName
	return lspProbe{name: "rename/" + name, method: "textDocument/rename", doc: doc, params: params, check: func(t *testing.T, result json.RawMessage) {
		if len(want) == 0 {
			if string(result) != "null" {
				t.Errorf("rename = %s, want null", result)
			}
			return
		}
		var edit struct {
			Changes map[string][]lspEdit `json:"changes"`
		}
		decode(t, result, &edit)
		got := []refPlace{}
		for uri, edits := range edit.Changes {
			for _, e := range edits {
				if e.NewText != newName {
					t.Errorf("edit in %s writes %q, want %q", uri, e.NewText, newName)
				}
				got = append(got, refPlace{relFile(t, dir, uri), e.Range})
			}
		}
		if g, w := sortedPlaces(got), sortedPlaces(want); !reflect.DeepEqual(g, w) {
			t.Errorf("rename edits = %+v\nwant           %+v", g, w)
		}
	}}
}

// TestSelfHostLSPReferencesX86_64 holds references and rename to the Go
// server's cases (references_test.go), plus the shapes they leave out: a
// declaration's own name, includeDeclaration, a parameter, shadowing, a local
// shadowing a function, a built-in variant, non-ASCII before a name, a
// literate document, and the renames that are refused.
func TestSelfHostLSPReferencesX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"local.fern":   "function main(): i32 {\n  let x = 1;\n  return x + x;\n}\n",
		"top.fern":     "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, add(2, 3)); }\n",
		"blank.fern":   "function main(): i32 { return 0; }\n",
		"plus.fern":    "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n",
		"method.fern":  "struct Point { x: i32, y: i32 }\nfunction (p: Point) sum(): i32 { return p.x + p.y; }\nfunction main(): i32 {\n  let p: Point = Point { x: 1, y: 2 };\n  return p.sum();\n}\n",
		"field.fern":   "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: Point = Point { x: 3, y: 4 };\n  return p.x;\n}\n",
		"variant.fern": "enum Color { Red, Green, Blue }\nfunction main(): i32 {\n  let c: Color = Red;\n  match (c) { Red => { return 1; }, _ => { return 0; } }\n}\n",
		"shadow.fern":  "function main(): i32 {\n  let x: i32 = 1;\n  if (x > 0) {\n    let x: i32 = 2;\n    return x;\n  }\n  return x;\n}\n",
		"global.fern":  "function f(): i32 { return 1; }\nfunction main(): i32 {\n  let f: i32 = 2;\n  return f;\n}\nfunction g(): i32 { return f(); }\n",
		"opt.fern":     "function main(): i32 {\n  let o: Option[i32] = Some(1);\n  match (o) { Some(n) => { return n; }, None => { return 0; } }\n}\n",
		"std.fern":     "import \"std/string\";\nfunction main(): i32 {\n  let r: string = string.repeat_char(1 as char, 2);\n  return r.len();\n}\n",
		"utf.fern":     "function main(): i32 {\n  let s: string = \"é😀\"; let count: i32 = 1;\n  return count + 1;\n}\n",
		// A variant at every depth of a pattern: a nested match arm, a match
		// expression, a nested `if let` and `let … else`, and constructions.
		"nest.fern": "enum Shape { Circle(i32), Square(i32) }\nstruct Box { s: Shape }\nfunction area(o: Option[Shape]): i32 {\n  match (o) {\n    Some(Circle(r)) => { return r; },\n    Some(Square(w)) => { return w * w; },\n    None => { return 0; }\n  }\n}\nfunction pair(a: Shape, b: Shape): i32 {\n  let n: i32 = match (a) { Circle(r) => r, _ => 0 };\n  if let Some(Circle(q)) = Some(b) {\n    return q + n;\n  }\n  let Some(Square(z)) = Some(a) else { return 1; };\n  return z;\n}\nfunction main(): i32 {\n  return area(Some(Circle(2))) + pair(Shape.Circle(1), Square(2));\n}\n",
		// A chunk referenced two columns in, so its columns move.
		"chunks.fern.md": "# Doc\n\n```fern\n<<*>>=\nfunction helper(n: i32): i32 { return n + 1; }\nfunction main(): i32 {\n  <<body>>\n}\n```\n\nThe body.\n\n```fern\n<<body>>=\nlet v: i32 = helper(41);\nreturn v;\n```\n",
	})
	open := []string{"local.fern", "top.fern", "blank.fern", "plus.fern", "method.fern", "field.fern", "variant.fern", "shadow.fern", "global.fern", "opt.fern", "std.fern", "utf.fern", "nest.fern", "chunks.fern.md"}
	probeLSP(t, s, dir, open, []lspProbe{
		// TestReferences_LocalVar: the declaration and both uses.
		refsProbe(dir, "local-var", "local.fern", 2, 9, true, place("local.fern", 1, 6, 7), place("local.fern", 2, 9, 10), place("local.fern", 2, 13, 14)),
		// TestReferences_TopLevelFunction: the declaration — its name, where
		// the Go server placed it at the `function` keyword — and both calls.
		refsProbe(dir, "top-level-function", "top.fern", 1, 31, true, place("top.fern", 0, 9, 12), place("top.fern", 1, 30, 33), place("top.fern", 1, 37, 40)),
		// TestReferences_NoneAtBlankSpot: a literal, and a keyword.
		refsProbe(dir, "none-on-a-literal", "blank.fern", 0, 30, true),
		refsProbe(dir, "none-on-a-keyword", "blank.fern", 0, 25, true),
		// The Go server answered nothing on a declaration's own name.
		refsProbe(dir, "from-the-declaration", "top.fern", 0, 10, true, place("top.fern", 0, 9, 12), place("top.fern", 1, 30, 33), place("top.fern", 1, 37, 40)),
		// The Go server ignored includeDeclaration.
		refsProbe(dir, "without-the-declaration", "top.fern", 1, 31, false, place("top.fern", 1, 30, 33), place("top.fern", 1, 37, 40)),
		// The Go server left a parameter's declaration out.
		refsProbe(dir, "parameter", "top.fern", 0, 13, true, place("top.fern", 0, 13, 14), place("top.fern", 0, 43, 44)),
		// The Go server gave all five `x`s for either binding.
		refsProbe(dir, "outer-binding", "shadow.fern", 6, 9, true, place("shadow.fern", 1, 6, 7), place("shadow.fern", 2, 6, 7), place("shadow.fern", 6, 9, 10)),
		refsProbe(dir, "inner-binding", "shadow.fern", 4, 11, true, place("shadow.fern", 3, 8, 9), place("shadow.fern", 4, 11, 12)),
		refsProbe(dir, "function-not-the-local-shadowing-it", "global.fern", 0, 9, true, place("global.fern", 0, 9, 10), place("global.fern", 5, 27, 28)),
		// Option's variants have no declaration to place.
		refsProbe(dir, "built-in-variant", "opt.fern", 1, 24, true, place("opt.fern", 1, 23, 27), place("opt.fern", 2, 14, 18)),
		refsProbe(dir, "arm-binding", "opt.fern", 2, 19, true, place("opt.fern", 2, 19, 20), place("opt.fern", 2, 34, 35)),
		refsProbe(dir, "non-ascii-before", "utf.fern", 2, 10, true, place("utf.fern", 1, 29, 34), place("utf.fern", 2, 9, 14)),
		refsProbe(dir, "literate", "chunks.fern.md", 14, 14, true, place("chunks.fern.md", 4, 9, 15), place("chunks.fern.md", 14, 13, 19)),
		// The Go server found a variant only where it heads an arm's pattern.
		refsProbe(dir, "variant-in-nested-patterns", "nest.fern", 0, 14, true,
			place("nest.fern", 0, 13, 19), place("nest.fern", 4, 9, 15), place("nest.fern", 10, 27, 33), place("nest.fern", 11, 14, 20), place("nest.fern", 18, 19, 25), place("nest.fern", 18, 44, 50)),
		refsProbe(dir, "from-a-nested-pattern", "nest.fern", 4, 10, true,
			place("nest.fern", 0, 13, 19), place("nest.fern", 4, 9, 15), place("nest.fern", 10, 27, 33), place("nest.fern", 11, 14, 20), place("nest.fern", 18, 19, 25), place("nest.fern", 18, 44, 50)),
		refsProbe(dir, "variant-in-a-nested-let-else", "nest.fern", 14, 12, true, place("nest.fern", 0, 26, 32), place("nest.fern", 5, 9, 15), place("nest.fern", 14, 11, 17), place("nest.fern", 18, 55, 61)),
		refsProbe(dir, "binding-in-a-nested-if-let", "nest.fern", 11, 21, true, place("nest.fern", 11, 21, 22), place("nest.fern", 12, 11, 12)),
		// TestRename_LocalVar.
		renameProbe(dir, "local-var", "local.fern", 2, 9, "renamed", place("local.fern", 1, 6, 7), place("local.fern", 2, 9, 10), place("local.fern", 2, 13, 14)),
		// TestRename_TopLevelFunction.
		renameProbe(dir, "top-level-function", "plus.fern", 1, 31, "plus", place("plus.fern", 0, 9, 12), place("plus.fern", 1, 30, 33)),
		// TestRename_MethodCall: the declaration and the call.
		renameProbe(dir, "method-call", "method.fern", 4, 13, "total", place("method.fern", 1, 20, 23), place("method.fern", 4, 11, 14)),
		// TestRename_StructField: the declaration, the literal's field and the
		// access.
		renameProbe(dir, "struct-field", "field.fern", 3, 11, "horiz", place("field.fern", 0, 15, 16), place("field.fern", 2, 25, 26), place("field.fern", 3, 11, 12)),
		// TestRename_EnumVariant: the declaration, the literal and the arm.
		renameProbe(dir, "enum-variant", "variant.fern", 2, 17, "Crimson", place("variant.fern", 0, 13, 16), place("variant.fern", 2, 17, 20), place("variant.fern", 3, 14, 17)),
		renameProbe(dir, "struct-from-its-declaration", "method.fern", 0, 8, "Pt", place("method.fern", 0, 7, 12), place("method.fern", 1, 13, 18), place("method.fern", 3, 9, 14), place("method.fern", 3, 17, 22)),
		renameProbe(dir, "literate", "chunks.fern.md", 15, 7, "w", place("chunks.fern.md", 14, 4, 5), place("chunks.fern.md", 15, 7, 8)),
		renameProbe(dir, "non-ascii-before", "utf.fern", 1, 30, "total", place("utf.fern", 1, 29, 34), place("utf.fern", 2, 9, 14)),
		renameProbe(dir, "variant-in-nested-patterns", "nest.fern", 11, 15, "Round",
			place("nest.fern", 0, 13, 19), place("nest.fern", 4, 9, 15), place("nest.fern", 10, 27, 33), place("nest.fern", 11, 14, 20), place("nest.fern", 18, 19, 25), place("nest.fern", 18, 44, 50)),
		// Refused: a keyword, a name that is not an identifier, no name, a
		// variant with no declaration, and a function the stdlib declares.
		renameProbe(dir, "to-a-keyword", "local.fern", 2, 9, "let"),
		renameProbe(dir, "to-a-non-identifier", "local.fern", 2, 9, "9x"),
		renameProbe(dir, "to-nothing", "local.fern", 2, 9, ""),
		renameProbe(dir, "built-in-variant", "opt.fern", 1, 24, "Just"),
		renameProbe(dir, "stdlib-function", "std.fern", 2, 26, "rep"),
	})
}

// TestSelfHostLSPReferencesWorkspaceX86_64 is references across modules:
// workspace_test.go's VariantReferencesStayInTheirEnum and the references half
// of PositionSearchDoesNotCrossFiles, and every kind of name referred to from
// another module — a type, a field, a function, a method, and a variant
// written in a match arm, an `if let` and a construction.
func TestSelfHostLSPReferencesWorkspaceX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.fern": "pub enum Kind { Text }\npub function ka(): Kind { return Text; }\n",
		"b.fern": "pub enum Kind { Text }\npub function kb(): Kind { return Text; }\n",
		"main.fern": "import \"./a\";\n" +
			"import \"./b\";\n" +
			"function main(): i32 { let x: a.Kind = a.ka(); let y: b.Kind = b.kb(); return 0; }\n",
		"util.fern": "pub struct Point { x: i32, y: i32 }\npub enum Shape { Circle(i32), Square(i32), Dot }\npub function foo(p: Point): i32 { return p.x; }\npub function (p: Point) norm(): i32 { return p.x + p.y; }\n",
		"use.fern": "import \"./util\";\n" +
			"function origin(): util.Point { return util.Point { x: 0, y: 0 }; }\n" +
			"function area(s: util.Shape): i32 {\n" +
			"  match (s) {\n" +
			"    util.Circle(r) => { return r * 3; },\n" +
			"    util.Square(w) => { return w * w; },\n" +
			"    _ => { return 0; }\n" +
			"  }\n" +
			"}\n" +
			"function main(): i32 {\n" +
			"  let p: util.Point = origin();\n" +
			"  let s: util.Shape = util.Circle(2);\n" +
			"  if let util.Square(n) = s {\n" +
			"    return n;\n" +
			"  }\n" +
			"  return util.foo(p) + p.norm() + area(util.Dot) + p.x;\n" +
			"}\n",
	})
	probeLSP(t, s, dir, []string{"main.fern", "a.fern", "b.fern", "use.fern"}, []lspProbe{
		// Each file's `Text` is its own module's variant: its declaration
		// and its use, in that file.
		refsProbe(dir, "a-variant", "a.fern", 1, 33, true, place("a.fern", 0, 16, 20), place("a.fern", 1, 33, 37)),
		refsProbe(dir, "b-variant", "b.fern", 1, 33, true, place("b.fern", 0, 16, 20), place("b.fern", 1, 33, 37)),
		// main.fern's import line is shorter than a's `return Text;` line.
		refsProbe(dir, "no-search-in-siblings", "main.fern", 1, 33, true),
		refsProbe(dir, "imported-enum", "main.fern", 2, 33, true, place("a.fern", 0, 9, 13), place("a.fern", 1, 19, 23), place("main.fern", 2, 32, 36)),
		refsProbe(dir, "imported-type", "use.fern", 10, 16, true,
			place("use.fern", 1, 24, 29), place("use.fern", 1, 44, 49), place("use.fern", 10, 14, 19),
			place("util.fern", 0, 11, 16), place("util.fern", 2, 20, 25), place("util.fern", 3, 17, 22)),
		refsProbe(dir, "imported-field", "use.fern", 15, 53, true,
			place("use.fern", 1, 52, 53), place("use.fern", 15, 53, 54),
			place("util.fern", 0, 19, 20), place("util.fern", 2, 43, 44), place("util.fern", 3, 47, 48)),
		refsProbe(dir, "imported-function", "use.fern", 15, 15, true, place("use.fern", 15, 14, 17), place("util.fern", 2, 13, 16)),
		refsProbe(dir, "imported-method", "use.fern", 15, 26, true, place("use.fern", 15, 25, 29), place("util.fern", 3, 24, 28)),
		refsProbe(dir, "variant-from-a-construction", "use.fern", 11, 28, true, place("use.fern", 4, 9, 15), place("use.fern", 11, 27, 33), place("util.fern", 1, 17, 23)),
		refsProbe(dir, "variant-from-an-if-let", "use.fern", 12, 15, true, place("use.fern", 5, 9, 15), place("use.fern", 12, 14, 20), place("util.fern", 1, 30, 36)),
		refsProbe(dir, "variant-from-an-arm", "use.fern", 5, 11, true, place("use.fern", 5, 9, 15), place("use.fern", 12, 14, 20), place("util.fern", 1, 30, 36)),
		refsProbe(dir, "if-let-binding", "use.fern", 12, 21, true, place("use.fern", 12, 21, 22), place("use.fern", 13, 11, 12)),
		renameProbe(dir, "imported-function", "use.fern", 15, 15, "bar", place("use.fern", 15, 14, 17), place("util.fern", 2, 13, 16)),
		renameProbe(dir, "imported-type", "use.fern", 1, 26, "Pt",
			place("use.fern", 1, 24, 29), place("use.fern", 1, 44, 49), place("use.fern", 10, 14, 19),
			place("util.fern", 0, 11, 16), place("util.fern", 2, 20, 25), place("util.fern", 3, 17, 22)),
	})
}

// semToken is a decoded semantic token: its position, length and type.
type semToken struct {
	line, char, len int
	kind            string
}

func (s semToken) String() string {
	return fmt.Sprintf("%d:%d+%d %s", s.line, s.char, s.len, s.kind)
}

func tok(line, char, n int, kind string) semToken { return semToken{line, char, n, kind} }

// semTypes is the Go server's legend (semantic_tokens.go), which this server
// must advertise in the same order.
var semTypes = []string{"function", "method", "struct", "enum", "enumMember", "parameter", "variable", "property", "type", "keyword", "namespace"}

// decodeSemantic undoes LSP's encoding: each token's line relative to the
// one before, and its start relative to the one before on the same line.
func decodeSemantic(t *testing.T, data []int) []semToken {
	t.Helper()
	if len(data)%5 != 0 {
		t.Fatalf("data length %d is not a multiple of 5: %v", len(data), data)
	}
	out := []semToken{}
	line, char := 0, 0
	for i := 0; i < len(data); i += 5 {
		if data[i] != 0 {
			char = 0
		}
		line += data[i]
		char += data[i+1]
		if data[i+3] < 0 || data[i+3] >= len(semTypes) || data[i+4] != 0 {
			t.Fatalf("token %d has type %d, modifiers %d", i/5, data[i+3], data[i+4])
		}
		out = append(out, semToken{line, char, data[i+2], semTypes[data[i+3]]})
	}
	return out
}

func tokensProbe(name, doc string, want ...semToken) lspProbe {
	return lspProbe{name: "tokens/" + name, method: "textDocument/semanticTokens/full", doc: doc, params: lspMsg{}, check: func(t *testing.T, result json.RawMessage) {
		var r struct {
			Data []int `json:"data"`
		}
		decode(t, result, &r)
		if r.Data == nil {
			t.Fatalf("answered %s, want a data array", result)
		}
		if got := decodeSemantic(t, r.Data); !reflect.DeepEqual(got, append([]semToken{}, want...)) {
			t.Errorf("tokens = %v\nwant     %v", got, want)
		}
	}}
}

// TestSelfHostLSPSemanticTokensX86_64 holds semantic tokens to the Go
// server's cases (semantic_tokens_test.go, and workspace_test.go's
// SemanticTokensCoverOnlyTheRequestedFile), with every token written out: the
// legend, a simple program and its encoding, every token type there is, a
// workspace's entry, non-ASCII before a name, and a literate document. The Go
// server classified only what a function body names and each type an
// annotation writes, and anything it could not classify as a variable; this
// one also classifies each declared name, parameter and binding, and leaves
// out what it cannot classify.
func TestSelfHostLSPSemanticTokensX86_64(t *testing.T) {
	s := buildLSPServers(t)

	t.Run("legend", func(t *testing.T) {
		frames, _ := runLSP(t, s.selfHost(), session())
		var got struct {
			Capabilities struct {
				SemanticTokensProvider *struct {
					Legend struct {
						TokenTypes     []string `json:"tokenTypes"`
						TokenModifiers []string `json:"tokenModifiers"`
					} `json:"legend"`
					Full bool `json:"full"`
				} `json:"semanticTokensProvider"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(response(t, frames, "1").Result, &got); err != nil {
			t.Fatal(err)
		}
		p := got.Capabilities.SemanticTokensProvider
		if p == nil || !reflect.DeepEqual(p.Legend.TokenTypes, semTypes) || p.Legend.TokenModifiers == nil || len(p.Legend.TokenModifiers) != 0 || !p.Full {
			t.Errorf("semanticTokensProvider = %+v, want the Go server's legend, no modifiers, full", p)
		}
	})

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"simple.fern": "function add(a: i32, b: i32): i32 { return a + b; }\n",
		"kinds.fern": "import \"./lib\";\n" +
			"struct Point { x: i32 }\n" +
			"enum Color { Red, Green }\n" +
			"function (p: Point) get(): i32 { return p.x; }\n" +
			"function first[T](xs: T[]): T { return xs[0]; }\n" +
			"function main(): i32 {\n" +
			"  let p: Point = Point { x: 1 };\n" +
			"  let c: Color = Color.Red;\n" +
			"  match (c) { Red => { return p.get() + first([1]); }, _ => { return lib.one(); } }\n" +
			"}\n",
		"lib.fern": "pub function one(): i32 { return 1; }\n",
		"a.fern":   "pub enum Kind { Text }\npub function ka(): Kind { return Text; }\n",
		"b.fern":   "pub enum Kind { Text }\npub function kb(): Kind { return Text; }\n",
		"main.fern": "import \"./a\";\n" +
			"import \"./b\";\n" +
			"function main(): i32 { let x: a.Kind = a.ka(); let y: b.Kind = b.kb(); return 0; }\n",
		"utf.fern":       "function main(): i32 {\n  let s: string = \"é😀\"; let count: i32 = 1;\n  return count;\n}\n",
		"nest.fern":      "enum Shape { Circle(i32), Square }\nfunction f(o: Option[Shape]): i32 {\n  match (o) { Some(Circle(r)) => { return r; }, _ => { return 0; } }\n}\n",
		"chunks.fern.md": "# Doc\n\n```fern\n<<*>>=\nfunction helper(n: i32): i32 { return n + 1; }\nfunction main(): i32 {\n  <<body>>\n}\n```\n\nThe body.\n\n```fern\n<<body>>=\nlet v: i32 = helper(41);\nreturn v;\n```\n",
	})
	probeLSP(t, s, dir, []string{"simple.fern", "kinds.fern", "main.fern", "utf.fern", "nest.fern", "chunks.fern.md"}, []lspProbe{
		// TestSemanticTokens_EmitsForSimpleProgram / DeltaEncoded: every token
		// after the first on its line is relative to the one before.
		{name: "tokens/delta-encoded", method: "textDocument/semanticTokens/full", doc: "simple.fern", params: lspMsg{}, check: func(t *testing.T, result json.RawMessage) {
			var r struct {
				Data []int `json:"data"`
			}
			decode(t, result, &r)
			if want := []int{0, 9, 3, 0, 0, 0, 4, 1, 5, 0, 0, 8, 1, 5, 0, 0, 22, 1, 5, 0, 0, 4, 1, 5, 0}; !reflect.DeepEqual(r.Data, want) {
				t.Errorf("data = %v, want %v", r.Data, want)
			}
		}},
		tokensProbe("simple-program", "simple.fern", tok(0, 9, 3, "function"), tok(0, 13, 1, "parameter"), tok(0, 21, 1, "parameter"), tok(0, 43, 1, "parameter"), tok(0, 47, 1, "parameter")),
		tokensProbe("every-type", "kinds.fern",
			tok(1, 7, 5, "struct"), tok(1, 15, 1, "property"),
			tok(2, 5, 5, "enum"), tok(2, 13, 3, "enumMember"), tok(2, 18, 5, "enumMember"),
			tok(3, 10, 1, "parameter"), tok(3, 13, 5, "struct"), tok(3, 20, 3, "method"), tok(3, 40, 1, "parameter"), tok(3, 42, 1, "property"),
			tok(4, 9, 5, "function"), tok(4, 15, 1, "type"), tok(4, 18, 2, "parameter"), tok(4, 22, 1, "type"), tok(4, 28, 1, "type"), tok(4, 39, 2, "parameter"),
			tok(5, 9, 4, "function"),
			tok(6, 6, 1, "variable"), tok(6, 9, 5, "struct"), tok(6, 17, 5, "struct"), tok(6, 25, 1, "property"),
			tok(7, 6, 1, "variable"), tok(7, 9, 5, "enum"), tok(7, 17, 5, "enum"), tok(7, 23, 3, "enumMember"),
			tok(8, 9, 1, "variable"), tok(8, 14, 3, "enumMember"), tok(8, 30, 1, "variable"), tok(8, 32, 3, "method"), tok(8, 40, 5, "function"), tok(8, 69, 3, "namespace"), tok(8, 73, 3, "function")),
		// SemanticTokensCoverOnlyTheRequestedFile: a and b, which main.fern
		// imports, carry names on lines 1 and 2 that must not be emitted.
		tokensProbe("only-the-requested-file", "main.fern",
			tok(2, 9, 4, "function"), tok(2, 27, 1, "variable"), tok(2, 30, 1, "namespace"), tok(2, 32, 4, "enum"), tok(2, 39, 1, "namespace"), tok(2, 41, 2, "function"),
			tok(2, 51, 1, "variable"), tok(2, 54, 1, "namespace"), tok(2, 56, 4, "enum"), tok(2, 63, 1, "namespace"), tok(2, 65, 2, "function")),
		// é is one UTF-16 unit and 😀 two, so `count` starts at 29, not at its
		// byte offset 32.
		// A variant inside a nested pattern is a variant too.
		tokensProbe("nested-pattern", "nest.fern",
			tok(0, 5, 5, "enum"), tok(0, 13, 6, "enumMember"), tok(0, 26, 6, "enumMember"),
			tok(1, 9, 1, "function"), tok(1, 11, 1, "parameter"), tok(1, 14, 6, "enum"), tok(1, 21, 5, "enum"),
			tok(2, 9, 1, "parameter"), tok(2, 14, 4, "enumMember"), tok(2, 19, 6, "enumMember"), tok(2, 26, 1, "variable"), tok(2, 42, 1, "variable")),
		tokensProbe("non-ascii-before", "utf.fern", tok(0, 9, 4, "function"), tok(1, 6, 1, "variable"), tok(1, 29, 5, "variable"), tok(2, 9, 5, "variable")),
		tokensProbe("literate", "chunks.fern.md",
			tok(4, 9, 6, "function"), tok(4, 16, 1, "parameter"), tok(4, 38, 1, "parameter"), tok(5, 9, 4, "function"),
			tok(14, 4, 1, "variable"), tok(14, 13, 6, "function"), tok(15, 7, 1, "variable")),
	})
}

// lspFix is a diagnostic's data: the fix a code action applies.
type lspFix struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
	Title   string   `json:"title"`
}

// fixedDiags opens each of files in dir in one server and returns what it
// published for each, every diagnostic as the raw JSON a client echoes.
func fixedDiags(t *testing.T, s lspServers, dir string, files []string) map[string][]json.RawMessage {
	t.Helper()
	var msgs []lspMsg
	for _, name := range files {
		msgs = append(msgs, lspOpen(fileURI(filepath.Join(dir, name)), string(mustRead(t, filepath.Join(dir, name)))))
	}
	frames, code := runLSP(t, s.selfHost(), session(msgs...))
	if code != 0 {
		t.Fatalf("server exited %d", code)
	}
	out := map[string][]json.RawMessage{}
	for _, f := range frames {
		if f.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p struct {
			URI         string            `json:"uri"`
			Diagnostics []json.RawMessage `json:"diagnostics"`
		}
		if err := json.Unmarshal(f.Params, &p); err != nil {
			t.Fatal(err)
		}
		out[relFile(t, dir, p.URI)] = p.Diagnostics
	}
	return out
}

// fixOf is the fix diagnostic `d` carries, if any.
func fixOf(t *testing.T, d json.RawMessage) (lspFix, bool) {
	t.Helper()
	var v struct {
		Data *lspFix `json:"data"`
	}
	if err := json.Unmarshal(d, &v); err != nil {
		t.Fatal(err)
	}
	if v.Data == nil {
		return lspFix{}, false
	}
	return *v.Data, true
}

// TestSelfHostLSPCodeActionX86_64 holds code actions to codeaction_test.go:
// a fix a diagnostic carries in its data comes back as a quick fix whose edit
// corrects the program, and a diagnostic with none gives none. It covers each
// diagnostic the checker attaches a fix to — a misspelt name read, called or
// assigned, a retired name, a misspelt field of a literal, and a misspelt
// field or method of an access — and the fix's span in UTF-16 units and in a
// literate document. A fix whose span does not hold the name it replaces (a
// struct pattern's field, reported where the arm starts) is not published.
func TestSelfHostLSPCodeActionX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	files := map[string]string{
		"kount.fern":   "function main(): i32 { let count = 1; return kount; }\n",
		"call.fern":    "function helper(n: i32): i32 { return n; }\nfunction main(): i32 { return halper(1); }\n",
		"assign.fern":  "function main(): i32 {\n  let count: i32 = 1;\n  kount = 2;\n  return count;\n}\n",
		"retired.fern": "function main(): i32 { return string_from_bytes([]).len(); }\n",
		"literal.fern": "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: Point = Point { x: 1, yy: 2 };\n  return p.x;\n}\n",
		"member.fern":  "struct Point { x: i32, y: i32 }\nfunction (p: Point) push(n: i32): i32 { return p.x + n; }\nfunction main(): i32 {\n  let p: Point = Point { x: 1, y: 2 };\n  return p.puzh(2) + p.yy;\n}\n",
		"utf.fern":     "function main(): i32 {\n  let s: string = \"é😀\"; let count: i32 = 1; return kount;\n}\n",
		"pattern.fern": "struct P { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: P = P { x: 1, y: 2 };\n  match (p) {\n    P { x, yy } => { return x; }\n  }\n}\n",
		"type.fern":    "function main(): i32 {\n  let x: i32 = \"s\";\n  return x;\n}\n",
		// The misspelt read is the chunk's, referenced two columns in.
		"chunks.fern.md": "# Doc\n\n```fern\n<<*>>=\nfunction main(): i32 {\n  <<body>>\n}\n```\n\n```fern\n<<body>>=\nlet count: i32 = 1;\nreturn kount;\n```\n",
	}
	writeTree(t, dir, files)
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	published := fixedDiags(t, s, dir, names)

	type want struct {
		file  string
		fix   lspFix
		fixed string // the edited line holds this
	}
	fix := func(line, c0, c1 int, text, title string) lspFix {
		return lspFix{rng(line, c0, c1), text, title}
	}
	wants := []want{
		{"kount.fern", fix(0, 45, 50, "count", "replace `kount` with `count`"), "return count;"},
		{"call.fern", fix(1, 30, 36, "helper", "replace `halper` with `helper`"), "return helper(1);"},
		{"assign.fern", fix(2, 2, 7, "count", "replace `kount` with `count`"), "  count = 2;"},
		{"retired.fern", fix(0, 30, 47, "string_from_bytes_unchecked", "replace `string_from_bytes` with `string_from_bytes_unchecked` (or `std/utf8.from_bytes`, which validates and returns `Option[string]`)"), "return string_from_bytes_unchecked([])"},
		{"literal.fern", fix(2, 31, 33, "y", "replace `yy` with `y`"), "Point { x: 1, y: 2 }"},
		{"member.fern", fix(4, 11, 15, "push", "replace `puzh` with `push`"), "return p.push(2)"},
		{"member.fern", fix(4, 23, 25, "y", "replace `yy` with `y`"), "+ p.y;"},
		{"utf.fern", fix(1, 52, 57, "count", "replace `kount` with `count`"), "return count;"},
		{"chunks.fern.md", fix(12, 7, 12, "count", "replace `kount` with `count`"), "return count;"},
	}
	for _, w := range wants {
		t.Run(w.file+"/"+w.fix.NewText, func(t *testing.T) {
			var diag json.RawMessage
			for _, d := range published[w.file] {
				if f, ok := fixOf(t, d); ok && f.Range == w.fix.Range {
					diag = d
					if f != w.fix {
						t.Errorf("published fix %+v, want %+v", f, w.fix)
					}
				}
			}
			if diag == nil {
				t.Fatalf("no diagnostic of %s carries a fix at %+v: %s", w.file, w.fix.Range, published[w.file])
			}
			// Echo the diagnostic as a client does, and apply the action's edit.
			uri := fileURI(filepath.Join(dir, w.file))
			req := lspMsg{"id": 2, "method": "textDocument/codeAction", "params": lspMsg{
				"textDocument": lspMsg{"uri": uri},
				"range":        w.fix.Range,
				"context":      lspMsg{"diagnostics": []json.RawMessage{diag}},
			}}
			frames, _ := runLSP(t, s.selfHost(), session(req))
			var actions []struct {
				Title       string            `json:"title"`
				Kind        string            `json:"kind"`
				Diagnostics []json.RawMessage `json:"diagnostics"`
				Edit        struct {
					Changes map[string][]lspEdit `json:"changes"`
				} `json:"edit"`
			}
			decode(t, response(t, frames, "2").Result, &actions)
			if len(actions) != 1 {
				t.Fatalf("got %d actions, want 1: %+v", len(actions), actions)
			}
			a := actions[0]
			if a.Kind != "quickfix" || a.Title != w.fix.Title {
				t.Errorf("action is %q %q, want quickfix %q", a.Kind, a.Title, w.fix.Title)
			}
			if len(a.Diagnostics) != 1 || !jsonEqual(t, a.Diagnostics[0], diag) {
				t.Errorf("action names %s, want the diagnostic as sent: %s", a.Diagnostics, diag)
			}
			edits := a.Edit.Changes[uri]
			if len(edits) != 1 || len(a.Edit.Changes) != 1 {
				t.Fatalf("edit = %+v, want one edit of %s", a.Edit.Changes, uri)
			}
			e := edits[0]
			lines := strings.Split(files[w.file], "\n")
			ln := []rune(lines[e.Range.Start.Line])
			u16 := utf16Prefix(ln, e.Range.Start.Character)
			end := utf16Prefix(ln, e.Range.End.Character)
			fixed := string(ln[:u16]) + e.NewText + string(ln[end:])
			if !strings.Contains(fixed, w.fixed) {
				t.Errorf("applied edit gives %q, want it to hold %q", fixed, w.fixed)
			}
		})
	}

	t.Run("no-fix", func(t *testing.T) {
		for _, file := range []string{"type.fern", "pattern.fern"} {
			if len(published[file]) == 0 {
				t.Fatalf("%s published no diagnostic", file)
			}
			for _, d := range published[file] {
				if f, ok := fixOf(t, d); ok {
					t.Errorf("%s published a fix %+v for %s", file, f, d)
				}
			}
		}
		// TestCodeActionNoFixData: a diagnostic without fix data is no action.
		req := lspMsg{"id": 2, "method": "textDocument/codeAction", "params": lspMsg{
			"textDocument": lspMsg{"uri": "file:///t"},
			"range":        rng(0, 0, 1),
			"context":      lspMsg{"diagnostics": []lspMsg{{"range": rng(0, 0, 1), "message": "type mismatch"}}},
		}}
		frames, _ := runLSP(t, s.selfHost(), session(req))
		if got := string(response(t, frames, "2").Result); got != "[]" {
			t.Errorf("codeAction = %s, want []", got)
		}
	})
}

// utf16Prefix is how many runes of `line` its first `units` UTF-16 units hold.
func utf16Prefix(line []rune, units int) int {
	n := 0
	for i, r := range line {
		if n >= units {
			return i
		}
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return len(line)
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

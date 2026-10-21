package e2ecompiler

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -lsp`'s hover and go-to-definition (compiler/lspquery.fern), with the
// answers written out as data: a definition lands on the declared name, a
// definition in the stdlib names the stdlib file, and a constant, a lambda's
// parameter and an enum-qualified variant have a hover.

type lspHover struct {
	Contents struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"contents"`
	Range lspRange `json:"range"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

// lspAsk is one hover or definition request and its expected answer. For a
// hover, `want` is the code shown, `prose` the doc comment shown under it, and
// `at` the range of the name hovered; for
// a definition, `file` is the file the location names — relative to the
// tree, or absolute — and `at` its range. An empty `want` (hover) or `file`
// (definition) expects null.
type lspAsk struct {
	name   string
	doc    string
	define bool
	line   int
	char   int
	want   string
	prose  string
	file   string
	at     lspRange
}

func rng(line, c0, c1 int) lspRange {
	return lspRange{lspPos{line, c0}, lspPos{line, c1}}
}

func hoverAsk(name, doc string, line, char int, want string, at lspRange) lspAsk {
	return lspAsk{name: name, doc: doc, line: line, char: char, want: want, at: at}
}

func defAsk(name, doc string, line, char int, file string, at lspRange) lspAsk {
	return lspAsk{name: name, doc: doc, define: true, line: line, char: char, file: file, at: at}
}

func lspPosition(id int, method, uri string, line, char int) lspMsg {
	return lspMsg{"id": id, "method": method, "params": lspMsg{"textDocument": lspMsg{"uri": uri}, "position": lspMsg{"line": line, "character": char}}}
}

// askLSP opens `open` (names in dir, read from disk) in one server, sends
// every request, and holds each answer to its expectation.
func askLSP(t *testing.T, s lspServers, dir string, open []string, asks []lspAsk) {
	t.Helper()
	var msgs []lspMsg
	for _, name := range open {
		msgs = append(msgs, lspOpen(fileURI(filepath.Join(dir, name)), string(mustRead(t, filepath.Join(dir, name)))))
	}
	for i, a := range asks {
		method := "textDocument/hover"
		if a.define {
			method = "textDocument/definition"
		}
		msgs = append(msgs, lspPosition(100+i, method, fileURI(filepath.Join(dir, a.doc)), a.line, a.char))
	}
	frames, code := runLSP(t, s.selfHost(), session(msgs...))
	if code != 0 {
		t.Fatalf("server exited %d", code)
	}
	for i, a := range asks {
		kind := "hover/"
		if a.define {
			kind = "definition/"
		}
		t.Run(kind+a.name, func(t *testing.T) {
			f := response(t, frames, jsonInt(100+i))
			if f.Error != nil {
				t.Fatalf("answered an error: %+v", f.Error)
			}
			if a.define {
				checkDefinition(t, dir, a, f.Result)
			} else {
				checkHover(t, a, f.Result)
			}
		})
	}
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func checkHover(t *testing.T, a lspAsk, result json.RawMessage) {
	t.Helper()
	if a.want == "" {
		if string(result) != "null" {
			t.Errorf("hover = %s, want null", result)
		}
		return
	}
	var h lspHover
	if err := json.Unmarshal(result, &h); err != nil || string(result) == "null" {
		t.Fatalf("hover = %s, want %q (%v)", result, a.want, err)
	}
	want := "```fern\n" + a.want + "\n```"
	if a.prose != "" {
		want += "\n\n" + a.prose
	}
	if h.Contents.Kind != "markdown" || h.Contents.Value != want {
		t.Errorf("hover shows %s %q, want markdown %q", h.Contents.Kind, h.Contents.Value, want)
	}
	if h.Range != a.at {
		t.Errorf("hover range = %+v, want %+v", h.Range, a.at)
	}
}

func checkDefinition(t *testing.T, dir string, a lspAsk, result json.RawMessage) {
	t.Helper()
	if a.file == "" {
		if string(result) != "null" {
			t.Errorf("definition = %s, want null", result)
		}
		return
	}
	var loc lspLocation
	if err := json.Unmarshal(result, &loc); err != nil || string(result) == "null" {
		t.Fatalf("definition = %s, want a location in %s (%v)", result, a.file, err)
	}
	u, err := url.Parse(loc.URI)
	if err != nil || u.Scheme != "file" {
		t.Fatalf("definition uri %q is not a file URI (%v)", loc.URI, err)
	}
	want := a.file
	if !filepath.IsAbs(want) {
		want = filepath.Join(dir, want)
	}
	if u.Path != filepath.ToSlash(want) {
		t.Errorf("definition in %s, want %s", u.Path, want)
	}
	if loc.Range != a.at {
		t.Errorf("definition range = %+v, want %+v", loc.Range, a.at)
	}
}

// TestSelfHostLSPHoverX86_64 holds hover and definition on one document:
// locals, functions, structs, fields, methods and variants, non-ASCII before
// the name, the built-in Option, a generic function, a constant, scoped
// bindings and a document position no name covers.
func TestSelfHostLSPHoverX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	const nonASCII = "function main(): i32 {\n  let s: string = \"é日本😀\"; let x: i32 = s.len(); return x;\n}\n"
	writeTree(t, dir, map[string]string{
		"local.fern":   "function main(): i32 {\n  let x: i32 = 7;\n  return x;\n}\n",
		"param.fern":   "function add(a: i32, b: i32): i32 {\n  return a + b;\n}\n",
		"call.fern":    "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n",
		"literal.fern": "struct Point { x: i32, y: i32 }\nfunction origin(): Point { return Point { x: 0, y: 0 }; }\n",
		"point.fern":   "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: Point = Point { x: 0, y: 0 };\n  return p.x;\n}\n",
		"color.fern":   "enum Color { Red, Green, Blue }\nfunction main(): i32 {\n  let c: Color = Red;\n  return 0;\n}\n",
		"method.fern": "struct Point { x: i32, y: i32 }\n" +
			"function (p: Point) sum(): i32 { return p.x + p.y; }\n" +
			"function main(): i32 {\n  let p: Point = Point { x: 3, y: 4 };\n  return p.sum();\n}\n",
		"chain.fern": "struct Inner { v: i32 }\nstruct Outer { inner: Inner }\n" +
			"function main(): i32 {\n  let o: Outer = Outer { inner: Inner { v: 5 } };\n  return o.inner.v;\n}\n",
		"variant.fern":  "enum Color { Red, Green, Blue }\nfunction main(): i32 { let c: Color = Red; return 0; }\n",
		"number.fern":   "function main(): i32 { return 0; }\n",
		"blank.fern":    "function main(): i32 {\n   \n  return 0;\n}\n",
		"shadow.fern":   "function f(x: i32): i32 {\n  let x: string = \"hi\";\n  return x.len();\n}\n",
		"unknown.fern":  "function main(): i32 { return undeclared_xyz; }\n",
		"nonascii.fern": nonASCII,
		"builtin.fern": "const LIMIT: i32 = 10;\n" +
			"function id[T](x: T): T { return x; }\n" +
			"function main(): i32 {\n" +
			"  let o: Option[i32] = Some(1);\n" +
			"  return id(3) + LIMIT;\n" +
			"}\n",
		"doc.fern": "// Point is a spot on the plane.\nstruct Point { x: i32, y: i32 }\n\n" +
			"// add sums two numbers.\n// It never overflows silently.\nfunction add(a: i32, b: i32): i32 { return a + b; }\n" +
			"// A comment with a blank line under it documents nothing.\n\n" +
			"function main(): i32 {\n  let n: i32 = add(1, 2);\n  let p: Point = Point { x: n, y: 0 };\n  return p.x + main2();\n}\n" +
			"function main2(): i32 { return 0; }\n",
		"scoped.fern": "enum Shape { Circle(f64), Square(i32) }\n" +
			"function area(s: Shape): f64 {\n" +
			"  match (s) {\n" +
			"    Circle(r) => { return r * r; },\n" +
			"    Square(w) => { return (w * w) as f64; }\n" +
			"  }\n" +
			"  return 0.0;\n" +
			"}\n" +
			"function main(): i32 {\n" +
			"  let total: i32 = 0;\n" +
			"  for word in [\"a\", \"bb\"] {\n" +
			"    total = total + word.len();\n" +
			"  }\n" +
			"  let f = (k: i32): i32 => k + total;\n" +
			"  let c: Shape = Shape.Circle(1.5);\n" +
			"  return f(1);\n" +
			"}\n",
	})
	// The x read at the end of nonascii.fern's line 1, after one 2-byte, two
	// 3-byte and one 4-byte character: 1 + 1 + 1 + 2 UTF-16 units.
	naLines := strings.Split(nonASCII, "\n")
	naUse := utf16Column(naLines, 2, strings.LastIndex(naLines[1], "x")+1)
	naDecl := utf16Column(naLines, 2, strings.Index(naLines[1], "x:")+1)
	if naUse == strings.LastIndex(naLines[1], "x") {
		t.Fatal("nonascii.fern's UTF-16 and byte columns agree — the case stopped testing the conversion")
	}
	asks := []lspAsk{
		hoverAsk("local", "local.fern", 2, 9, "(var) x: i32", rng(2, 9, 10)),
		hoverAsk("parameter", "param.fern", 1, 9, "(parameter) a: i32", rng(1, 9, 10)),
		hoverAsk("function-call", "call.fern", 1, 31, "function add(i32, i32): i32", rng(1, 30, 33)),
		hoverAsk("struct-literal", "literal.fern", 1, 34, "struct Point { x: i32, y: i32 }", rng(1, 34, 39)),
		hoverAsk("annotation-struct", "point.fern", 2, 9, "struct Point { x: i32, y: i32 }", rng(2, 9, 14)),
		hoverAsk("annotation-enum", "color.fern", 2, 9, "enum Color { Red, Green, Blue }", rng(2, 9, 14)),
		hoverAsk("field", "point.fern", 3, 11, "(field) x: i32", rng(3, 11, 12)),
		hoverAsk("method", "method.fern", 4, 13, "(method on Point) function sum(): i32", rng(4, 11, 14)),
		hoverAsk("receiver", "method.fern", 1, 40, "(parameter) p: Point", rng(1, 40, 41)),
		hoverAsk("chained-field", "chain.fern", 4, 17, "(field) v: i32", rng(4, 17, 18)),
		hoverAsk("variant", "variant.fern", 1, 39, "(variant of Color) Red", rng(1, 38, 41)),
		hoverAsk("number-literal", "number.fern", 0, 30, "", lspRange{}),
		hoverAsk("whitespace", "blank.fern", 1, 1, "", lspRange{}),
		hoverAsk("local-shadows-parameter", "shadow.fern", 2, 9, "(var) x: string", rng(2, 9, 10)),
		hoverAsk("builtin-method", "shadow.fern", 2, 12, "(method on string) function len(): i32", rng(2, 11, 14)),
		hoverAsk("declared-name", "local.fern", 1, 6, "", lspRange{}),
		hoverAsk("declaration-keyword", "point.fern", 0, 2, "", lspRange{}),
		hoverAsk("struct-declaration-name", "point.fern", 0, 8, "", lspRange{}),
		hoverAsk("unknown-name", "unknown.fern", 0, 32, "", lspRange{}),
		hoverAsk("non-ascii-before", "nonascii.fern", 1, naUse, "(var) x: i32", rng(1, naUse, naUse+1)),
		hoverAsk("option-variant", "builtin.fern", 3, 24, "(variant of Option) Some(T)", rng(3, 23, 27)),
		hoverAsk("option-annotation", "builtin.fern", 3, 10, "enum Option[T] { Some(T), None }", rng(3, 9, 15)),
		hoverAsk("generic-function", "builtin.fern", 4, 10, "function id(T): T", rng(4, 9, 11)),
		hoverAsk("constant", "builtin.fern", 4, 17, "(const) LIMIT: i32", rng(4, 17, 22)),
		hoverAsk("match-arm-binding", "scoped.fern", 3, 26, "(var) r: f64", rng(3, 26, 27)),
		hoverAsk("match-arm-other-binding", "scoped.fern", 4, 27, "(var) w: i32", rng(4, 27, 28)),
		hoverAsk("for-binding", "scoped.fern", 11, 21, "(var) word: string", rng(11, 20, 24)),
		// Nor for a lambda's parameter, or a variant qualified by its enum.
		hoverAsk("lambda-parameter", "scoped.fern", 13, 27, "(parameter) k: i32", rng(13, 27, 28)),
		hoverAsk("captured-local", "scoped.fern", 13, 32, "(var) total: i32", rng(13, 31, 36)),
		hoverAsk("enum-qualified-variant", "scoped.fern", 14, 24, "(variant of Shape) Circle(f64)", rng(14, 23, 29)),
		hoverAsk("closure-local", "scoped.fern", 15, 9, "(var) f: (i32) => i32", rng(15, 9, 10)),
		// A declaration's doc comment shows under its signature.
		{name: "documented-function", doc: "doc.fern", line: 9, char: 16, want: "function add(i32, i32): i32", prose: "add sums two numbers.\nIt never overflows silently.", at: rng(9, 15, 18)},
		{name: "documented-struct", doc: "doc.fern", line: 10, char: 10, want: "struct Point { x: i32, y: i32 }", prose: "Point is a spot on the plane.", at: rng(10, 9, 14)},
		hoverAsk("undocumented-function", "doc.fern", 11, 17, "function main2(): i32", rng(11, 15, 20)),
		// A parameter is declared on its function's line; the comment above it
		// documents the function, not the parameter.
		hoverAsk("parameter-of-documented-function", "doc.fern", 5, 43, "(parameter) a: i32", rng(5, 43, 44)),

		// A definition lands on the declared name, not on `let`.
		defAsk("local", "local.fern", 2, 9, "local.fern", rng(1, 6, 7)),
		defAsk("parameter", "param.fern", 1, 9, "param.fern", rng(0, 13, 14)),
		defAsk("function-call", "call.fern", 1, 31, "call.fern", rng(0, 9, 12)),
		defAsk("struct-literal", "literal.fern", 1, 34, "literal.fern", rng(0, 7, 12)),
		defAsk("annotation", "point.fern", 2, 9, "point.fern", rng(0, 7, 12)),
		// On the field, not its struct.
		defAsk("field", "point.fern", 3, 11, "point.fern", rng(0, 15, 16)),
		defAsk("method", "method.fern", 4, 13, "method.fern", rng(1, 20, 23)),
		defAsk("receiver", "method.fern", 1, 40, "method.fern", rng(1, 10, 11)),
		defAsk("chained-field", "chain.fern", 4, 17, "chain.fern", rng(0, 15, 16)),
		defAsk("variant", "variant.fern", 1, 39, "variant.fern", rng(0, 13, 16)),
		defAsk("number-literal", "number.fern", 0, 30, "", lspRange{}),
		defAsk("unknown-name", "unknown.fern", 0, 32, "", lspRange{}),
		defAsk("local-shadows-parameter", "shadow.fern", 2, 9, "shadow.fern", rng(1, 6, 7)),
		defAsk("non-ascii-before", "nonascii.fern", 1, naUse, "nonascii.fern", rng(1, naDecl, naDecl+1)),
		// Some has no declaration to land on.
		defAsk("option-variant", "builtin.fern", 3, 24, "", lspRange{}),
		defAsk("generic-function", "builtin.fern", 4, 10, "builtin.fern", rng(1, 9, 11)),
		defAsk("constant", "builtin.fern", 4, 17, "builtin.fern", rng(0, 6, 11)),
		defAsk("match-arm-binding", "scoped.fern", 3, 26, "scoped.fern", rng(3, 11, 12)),
		defAsk("for-binding", "scoped.fern", 11, 21, "scoped.fern", rng(10, 6, 10)),
		defAsk("lambda-parameter", "scoped.fern", 13, 27, "scoped.fern", rng(13, 11, 12)),
		defAsk("enum-qualified-variant", "scoped.fern", 14, 24, "scoped.fern", rng(0, 13, 19)),
	}
	open := []string{"local.fern", "param.fern", "call.fern", "literal.fern", "point.fern", "color.fern", "method.fern", "chain.fern",
		"variant.fern", "number.fern", "blank.fern", "shadow.fern", "unknown.fern", "nonascii.fern", "builtin.fern", "scoped.fern", "doc.fern"}
	askLSP(t, s, dir, open, asks)

	// A document the server does not have open, and a request without a
	// position, are answered null rather than with an error.
	t.Run("not-open", func(t *testing.T) {
		frames, _ := runLSP(t, s.selfHost(), session(
			lspPosition(2, "textDocument/hover", fileURI(filepath.Join(dir, "local.fern")), 2, 9),
			lspPosition(3, "textDocument/definition", fileURI(filepath.Join(dir, "local.fern")), 2, 9),
			lspOpen(fileURI(filepath.Join(dir, "local.fern")), "function main(): i32 {\n  let x: i32 = 7;\n  return x;\n}\n"),
			lspMsg{"id": 4, "method": "textDocument/hover", "params": lspMsg{"textDocument": lspMsg{"uri": fileURI(filepath.Join(dir, "local.fern"))}}},
		))
		for _, id := range []string{"2", "3", "4"} {
			if f := response(t, frames, id); string(f.Result) != "null" || f.Error != nil {
				t.Errorf("request %s answered result=%s error=%+v, want null", id, f.Result, f.Error)
			}
		}
	})
}

// TestSelfHostLSPHoverWorkspaceX86_64 is hover and definition across
// modules: a name declared in another module is described as its
// module declares it and located in that module's file, and a position is
// only ever searched for in the document the request names.
func TestSelfHostLSPHoverWorkspaceX86_64(t *testing.T) {
	s := buildLSPServers(t)

	t.Run("cross-module", func(t *testing.T) {
		// A space in the directory is a %20 in the location's URI.
		dir := filepath.Join(t.TempDir(), "a project")
		writeTree(t, dir, map[string]string{
			"util.fern": "pub struct Point { x: i32, y: i32 }\npub function foo(): i32 { return 42; }\n",
			"main.fern": "import \"./util\";\n" +
				"function origin(): util.Point { return util.Point { x: 0, y: 0 }; }\n" +
				"function main(): i32 { let p: util.Point = origin(); return p.x + util.foo(); }\n",
		})
		askLSP(t, s, dir, []string{"main.fern"}, []lspAsk{
			// CrossModuleStructDefinition: the annotation `util.Point`, the
			// cursor on `Point`, and on its qualifier.
			defAsk("annotation-definition", "main.fern", 2, 35, "util.fern", rng(0, 11, 16)),
			defAsk("qualifier-definition", "main.fern", 2, 31, "util.fern", rng(0, 11, 16)),
			hoverAsk("annotation-hover", "main.fern", 2, 35, "struct util__Point { x: i32, y: i32 }", rng(2, 30, 40)),
			hoverAsk("return-annotation", "main.fern", 1, 24, "struct util__Point { x: i32, y: i32 }", rng(1, 19, 29)),
			hoverAsk("qualified-literal", "main.fern", 1, 47, "struct util__Point { x: i32, y: i32 }", rng(1, 39, 49)),
			defAsk("qualified-literal", "main.fern", 1, 47, "util.fern", rng(0, 11, 16)),
			// CrossModuleCallJump.
			hoverAsk("qualified-call", "main.fern", 2, 72, "(from util) function foo(): i32", rng(2, 66, 74)),
			defAsk("qualified-call", "main.fern", 2, 72, "util.fern", rng(1, 13, 16)),
			hoverAsk("field-of-imported-struct", "main.fern", 2, 62, "(field) x: i32", rng(2, 62, 63)),
			defAsk("field-of-imported-struct", "main.fern", 2, 62, "util.fern", rng(0, 19, 20)),
		})
		frames, _ := runLSP(t, s.selfHost(), session(
			lspOpen(fileURI(filepath.Join(dir, "main.fern")), string(mustRead(t, filepath.Join(dir, "main.fern")))),
			lspPosition(2, "textDocument/definition", fileURI(filepath.Join(dir, "main.fern")), 2, 72),
		))
		var loc lspLocation
		if err := json.Unmarshal(response(t, frames, "2").Result, &loc); err != nil || !strings.Contains(loc.URI, "/a%20project/util.fern") {
			t.Errorf("definition uri = %q (%v), want the space percent-encoded", loc.URI, err)
		}
	})

	// collidingVariantWorkspace's modules: a and b each declare `Kind { Text }`
	// and use `Text` at the same line and column, and main imports both.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.fern": "pub enum Kind { Text }\npub function ka(): Kind { return Text; }\n",
		"b.fern": "pub enum Kind { Text }\npub function kb(): Kind { return Text; }\n",
		"main.fern": "import \"./a\";\n" +
			"import \"./b\";\n" +
			"function main(): i32 { let x: a.Kind = a.ka(); let y: b.Kind = b.kb(); return 0; }\n",
		// own.fern declares a Kind of its own beside a's, so a bare `Text` is
		// its own variant and `a.Text` is a's.
		"own.fern": "import \"./a\";\n" +
			"enum Kind { Text, Other }\n" +
			"function main(): i32 {\n  let k: Kind = Text;\n  let x: a.Kind = a.Text;\n  return 0;\n}\n",
	})
	t.Run("colliding-variants", func(t *testing.T) {
		askLSP(t, s, dir, []string{"main.fern", "a.fern", "b.fern", "own.fern"}, []lspAsk{
			// VariantHoverNamesTheStampedEnum / VariantDefinitionPicksTheStampedEnum:
			// each file's `Text` is its own module's variant.
			hoverAsk("a-variant", "a.fern", 1, 33, "(variant of Kind) Text", rng(1, 33, 37)),
			defAsk("a-variant", "a.fern", 1, 33, "a.fern", rng(0, 16, 20)),
			hoverAsk("b-variant", "b.fern", 1, 33, "(variant of Kind) Text", rng(1, 33, 37)),
			defAsk("b-variant", "b.fern", 1, 33, "b.fern", rng(0, 16, 20)),
			hoverAsk("own-variant", "own.fern", 3, 17, "(variant of Kind) Text", rng(3, 16, 20)),
			defAsk("own-variant", "own.fern", 3, 17, "own.fern", rng(1, 12, 16)),
			hoverAsk("imported-variant", "own.fern", 4, 21, "(variant of a__Kind) Text", rng(4, 18, 24)),
			defAsk("imported-variant", "own.fern", 4, 21, "a.fern", rng(0, 16, 20)),
			hoverAsk("imported-call", "main.fern", 2, 41, "(from a) function ka(): a__Kind", rng(2, 39, 43)),
			defAsk("imported-call", "main.fern", 2, 41, "a.fern", rng(1, 13, 15)),
			// PositionSearchDoesNotCrossFiles: main.fern's import line is
			// shorter than a's `return Text;` line, and its `Kind` annotation
			// position is a's and b's, not its own.
			hoverAsk("no-search-in-siblings", "main.fern", 1, 33, "", lspRange{}),
			defAsk("no-search-in-siblings", "main.fern", 1, 33, "", lspRange{}),
			hoverAsk("no-type-in-siblings", "main.fern", 1, 19, "", lspRange{}),
			hoverAsk("own-annotation", "main.fern", 2, 30, "enum a__Kind { Text }", rng(2, 30, 36)),
		})
	})
}

// TestSelfHostLSPHoverLiterateAndStdlibX86_64 places answers in a literate
// document, whose positions are its tangled module's moved back onto it, and
// in the stdlib, whose definitions name the stdlib file.
func TestSelfHostLSPHoverLiterateAndStdlibX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		// litFeatureDoc: lines 3 and 4 tangle to lines 1 and 2.
		"t.fern.md": "```fern\n<<*>>=\nfunction greeting(): i32 { return 7; }\nfunction main(): i32 { return greeting(); }\n```\n",
		// A chunk referenced two columns in, so its columns move.
		"chunks.fern.md": "# Doc\n\n```fern\n<<*>>=\nfunction helper(n: i32): i32 { return n + 1; }\nfunction main(): i32 {\n  <<body>>\n}\n```\n\nThe body.\n\n```fern\n<<body>>=\nlet v: i32 = helper(41);\nreturn v;\n```\n",
		"array.fern":     "import \"std/array\";\nfunction main(): i32 {\n  let xs: i32[] = [1, 2];\n  let m: Option[i32] = xs.avg();\n  return xs.len();\n}\n",
		"std.fern":       "import \"std/string\";\nfunction main(): i32 {\n  let s: string = \"abc\";\n  let r: string = string.repeat_char(1 as char, 2);\n  if (s.starts_with(\"a\")) {\n    return r.len();\n  }\n  return 0;\n}\n",
	})
	stringFern := filepath.Join(s.stdlib, "std", "string.fern")
	arrayFern := filepath.Join(s.stdlib, "std", "array.fern")
	declAt := func(file, decl, name string) lspRange {
		for i, l := range strings.Split(string(mustRead(t, file)), "\n") {
			if strings.HasPrefix(l, decl) {
				c := strings.Index(l, name)
				return rng(i, c, c+len(name))
			}
		}
		t.Fatalf("%s no longer declares %q", file, decl)
		return lspRange{}
	}
	// docAbove is the comment run ending on the line above a declaration,
	// each line's `//` and surrounding space trimmed: what a hover shows.
	docAbove := func(file, decl string) string {
		lines := strings.Split(string(mustRead(t, file)), "\n")
		for i, l := range lines {
			if !strings.HasPrefix(l, decl) {
				continue
			}
			var run []string
			for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "//"); j-- {
				run = append([]string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[j]), "//"))}, run...)
			}
			return strings.Join(run, "\n")
		}
		t.Fatalf("%s no longer declares %q", file, decl)
		return ""
	}
	documented := func(a lspAsk, file, decl string) lspAsk {
		a.prose = docAbove(file, decl)
		if a.prose == "" {
			t.Fatalf("%s's %q lost its doc comment; hover the documented declaration another case names", file, decl)
		}
		return a
	}
	askLSP(t, s, dir, []string{"t.fern.md", "chunks.fern.md", "std.fern", "array.fern"}, []lspAsk{
		defAsk("literate-definition", "t.fern.md", 3, 33, "t.fern.md", rng(2, 9, 17)),
		hoverAsk("literate-hover", "t.fern.md", 3, 33, "function greeting(): i32", rng(3, 30, 38)),
		hoverAsk("moved-chunk-call", "chunks.fern.md", 14, 14, "function helper(i32): i32", rng(14, 13, 19)),
		defAsk("moved-chunk-call", "chunks.fern.md", 14, 14, "chunks.fern.md", rng(4, 9, 15)),
		hoverAsk("moved-chunk-local", "chunks.fern.md", 15, 7, "(var) v: i32", rng(15, 7, 8)),
		defAsk("moved-chunk-local", "chunks.fern.md", 15, 7, "chunks.fern.md", rng(14, 4, 5)),
		hoverAsk("prose", "chunks.fern.md", 10, 2, "", lspRange{}),
		documented(hoverAsk("stdlib-method", "std.fern", 4, 10, "(method on string) function starts_with(string): boolean", rng(4, 8, 19)), stringFern, "pub function (s: string) starts_with("),
		defAsk("stdlib-method", "std.fern", 4, 10, stringFern, declAt(stringFern, "pub function (s: string) starts_with(", "starts_with")),
		documented(hoverAsk("stdlib-function", "std.fern", 3, 28, "(from string) function repeat_char(char, i32): string", rng(3, 18, 36)), stringFern, "pub function repeat_char("),
		defAsk("stdlib-function", "std.fern", 3, 28, stringFern, declAt(stringFern, "pub function repeat_char(", "repeat_char")),
		// std/array's `__method_Array_avg`, and the built-in `len`, which has
		// no declaration.
		documented(hoverAsk("array-method", "array.fern", 3, 27, "(method on Array) function avg(): Option[i32]", rng(3, 26, 29)), arrayFern, "pub function __method_Array_avg("),
		defAsk("array-method", "array.fern", 3, 27, arrayFern, declAt(arrayFern, "pub function __method_Array_avg(", "__method_Array_avg")),
		hoverAsk("array-len", "array.fern", 4, 13, "(method on Array) function len(): i32", rng(4, 12, 15)),
		defAsk("array-len", "array.fern", 4, 13, "", lspRange{}),
	})
}

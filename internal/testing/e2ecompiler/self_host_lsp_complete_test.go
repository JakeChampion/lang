package e2ecompiler

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/lexer"
)

type lspItem struct {
	Label  string `json:"label"`
	Kind   int    `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

type lspCompletion struct {
	IsIncomplete *bool     `json:"isIncomplete"`
	Items        []lspItem `json:"items"`
}

type lspSignature struct {
	Label      string `json:"label"`
	Parameters []struct {
		Label [2]int `json:"label"`
	} `json:"parameters"`
}

type lspSignatureHelp struct {
	Signatures      []lspSignature `json:"signatures"`
	ActiveSignature int            `json:"activeSignature"`
	ActiveParameter int            `json:"activeParameter"`
}

type lspSymbol struct {
	Name           string      `json:"name"`
	Detail         string      `json:"detail,omitempty"`
	Kind           int         `json:"kind"`
	Range          lspRange    `json:"range"`
	SelectionRange lspRange    `json:"selectionRange"`
	Children       []lspSymbol `json:"children,omitempty"`
}

type lspInlay struct {
	Position lspPos `json:"position"`
	Label    string `json:"label"`
	Kind     int    `json:"kind"`
}

type lspProbe struct {
	name   string
	method string
	doc    string
	params lspMsg
	check  func(t *testing.T, result json.RawMessage)
}

func probeLSP(t *testing.T, s lspServers, dir string, open []string, probes []lspProbe) {
	t.Helper()
	var msgs []lspMsg
	for _, name := range open {
		msgs = append(msgs, lspOpen(fileURI(filepath.Join(dir, name)), string(mustRead(t, filepath.Join(dir, name)))))
	}
	for i, p := range probes {
		params := lspMsg{"textDocument": lspMsg{"uri": fileURI(filepath.Join(dir, p.doc))}}
		for k, v := range p.params {
			params[k] = v
		}
		msgs = append(msgs, lspMsg{"id": 100 + i, "method": p.method, "params": params})
	}
	frames, code := runLSP(t, s.selfHost(), session(msgs...))
	if code != 0 {
		t.Fatalf("server exited %d", code)
	}
	for i, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			f := response(t, frames, jsonInt(100+i))
			if f.Error != nil {
				t.Fatalf("answered an error: %+v", f.Error)
			}
			p.check(t, f.Result)
		})
	}
}

func at(line, char int) lspMsg {
	return lspMsg{"position": lspMsg{"line": line, "character": char}}
}

func decode(t *testing.T, result json.RawMessage, v any) {
	t.Helper()
	if string(result) == "null" {
		t.Fatalf("answered null, want %T", v)
	}
	if err := json.Unmarshal(result, v); err != nil {
		t.Fatalf("answer %s is not a %T: %v", result, v, err)
	}
}

func completeAt(name, doc string, line, char int, check func(*testing.T, []lspItem)) lspProbe {
	return lspProbe{name: "completion/" + name, method: "textDocument/completion", doc: doc, params: at(line, char), check: func(t *testing.T, result json.RawMessage) {
		var c lspCompletion
		decode(t, result, &c)
		if c.IsIncomplete == nil || *c.IsIncomplete {
			t.Errorf("isIncomplete = %v, want false: the list is the whole list", c.IsIncomplete)
		}
		for i := 1; i < len(c.Items); i++ {
			if c.Items[i-1].Label > c.Items[i].Label {
				t.Fatalf("items not sorted: %q before %q", c.Items[i-1].Label, c.Items[i].Label)
			}
		}
		check(t, c.Items)
	}}
}

var builtinItems = []lspItem{
	{"Err", 20, "(variant of Result) Err(E)"},
	{"None", 20, "(variant of Option) None"},
	{"Ok", 20, "(variant of Result) Ok(T)"},
	{"Option", 13, "enum Option"},
	{"Result", 13, "enum Result"},
	{"Some", 20, "(variant of Option) Some(T)"},
}

func scopeItems(own ...lspItem) func(*testing.T, []lspItem) {
	want := append(append([]lspItem{}, builtinItems...), own...)
	sort.SliceStable(want, func(i, j int) bool { return want[i].Label < want[j].Label })
	return func(t *testing.T, items []lspItem) {
		t.Helper()
		var names, keywords []lspItem
		for _, it := range items {
			if it.Kind == 14 {
				keywords = append(keywords, it)
			} else {
				names = append(names, it)
			}
		}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("names offered:\n  %+v\nwant\n  %+v", names, want)
		}
		var kw []lspItem
		for _, k := range lexer.Keywords() {
			kw = append(kw, lspItem{Label: k, Kind: 14})
		}
		if !reflect.DeepEqual(keywords, kw) {
			t.Errorf("keywords offered:\n  %+v\nwant the lexer's\n  %+v", keywords, kw)
		}
	}
}

func exactItems(want ...lspItem) func(*testing.T, []lspItem) {
	return func(t *testing.T, items []lspItem) {
		t.Helper()
		if want == nil {
			want = []lspItem{}
		}
		if items == nil {
			items = []lspItem{}
		}
		if !reflect.DeepEqual(items, want) {
			t.Errorf("items:\n  %+v\nwant\n  %+v", items, want)
		}
	}
}

func someItems(want ...lspItem) func(*testing.T, []lspItem) {
	return func(t *testing.T, items []lspItem) {
		t.Helper()
		for _, w := range want {
			found := false
			for _, it := range items {
				found = found || it == w
			}
			if !found {
				t.Errorf("%+v missing from %d items", w, len(items))
			}
		}
		for _, it := range items {
			if it.Kind == 14 {
				t.Errorf("keyword %q offered after a `.`", it.Label)
			}
		}
	}
}

func TestSelfHostLSPCompletionX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	const memberLine = `  let s: string = "é→🐛"; let m: i32 = s.`
	writeTree(t, dir, map[string]string{
		"body.fern":    "function add(a: i32, b: i32): i32 {\n  let sum: i32 = a + b;\n  return sum;\n}\n",
		"local.fern":   "function main(): i32 {\n  let n: i32 = 7;\n  return n;\n}\n",
		"top.fern":     "function f(): i32 { return 0; }\n\nstruct Point { x: i32, y: i32 }\n",
		"enum.fern":    "enum Color { Red, Green, Blue }\nfunction main(): i32 { return 0; }\n",
		"sorted.fern":  "function main(): i32 { return 0; }\n",
		"message.fern": "function add(a: i32, b: i32): i32 {\n  return a + b;\n}\n",
		"t.fern.md":    "```fern\n<<*>>=\nfunction greeting(): i32 { return 7; }\nfunction main(): i32 { return greeting(); }\n```\n",
		"past.fern":    "function f(): i32 { let t: i32 = 0; return t; }\n\n",
		"scoped.fern": "enum Shape { Circle(f64), Square(i32) }\n" +
			"function area(s: Shape, x: i32): f64 {\n" +
			"  let x: string = \"hi\";\n" +
			"  for w in [\"a\"] {\n" +
			"    let n = w.len();\n" +
			"  }\n" +
			"  let f = (k: i32): i32 => k + 1;\n" +
			"  match (s) {\n" +
			"    Circle(r) => { return r; },\n" +
			"    Square(q) => { return 0.0; }\n" +
			"  }\n" +
			"  return 0.0;\n" +
			"}\n" +
			"const LIMIT: i32 = 10;\n",
		"util.fern": "pub struct Point { x: i32 }\npub enum Kind { Text }\npub function foo(a: i32): i32 { return a; }\nfunction hidden(): i32 { return 1; }\npub const MAX: i32 = 9;\n",
		"members.fern": "import \"std/string\";\n" +
			"import \"./util\";\n" +
			"struct Point { x: i32, y: i32 }\n" +
			"function (p: Point) sum(): i32 { return p.x + p.y; }\n" +
			"enum Shape { Circle(f64), Square(i32) }\n" +
			"function main(): i32 {\n" +
			"  let p: Point = Point { x: 1, y: 2 };\n" +
			memberLine + "len();\n" +
			"  let n: i32 = p.sum() + util.foo(1) + m;\n" +
			"  let c: Shape = Shape.Square(2);\n" +
			"  let xs: i32[] = [1];\n" +
			"  return n + xs.len();\n" +
			"}\n",
		"unparsed.fern": "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: Point = Point { x: 1, y: 2 };\n  return p.;\n}\n",
	})
	sDot := utf16Column(strings.Split(memberLine, "\n"), 1, len(memberLine)+1)
	if sDot == len(memberLine) {
		t.Fatal("the member line's UTF-16 and byte columns agree — the case stopped testing the conversion")
	}
	pointMembers := exactItems(lspItem{"sum", 2, "function sum(): i32"}, lspItem{"x", 5, "i32"}, lspItem{"y", 5, "i32"})
	probes := []lspProbe{
		completeAt("in-function-body", "body.fern", 2, 2, scopeItems(
			lspItem{"a", 6, "(parameter) i32"}, lspItem{"add", 3, "function add(i32, i32): i32"},
			lspItem{"b", 6, "(parameter) i32"}, lspItem{"sum", 6, "i32"})),
		completeAt("local-carries-type", "local.fern", 2, 2, scopeItems(
			lspItem{"main", 3, "function main(): i32"}, lspItem{"n", 6, "i32"})),
		completeAt("at-top-level", "top.fern", 1, 0, scopeItems(
			lspItem{"Point", 22, "struct Point"}, lspItem{"f", 3, "function f(): i32"})),
		completeAt("includes-enum-variants", "enum.fern", 1, 24, scopeItems(
			lspItem{"Blue", 20, "(variant of Color) Blue"}, lspItem{"Color", 13, "enum Color"},
			lspItem{"Green", 20, "(variant of Color) Green"}, lspItem{"Red", 20, "(variant of Color) Red"},
			lspItem{"main", 3, "function main(): i32"})),
		completeAt("sorted-by-label", "sorted.fern", 0, 25, scopeItems(lspItem{"main", 3, "function main(): i32"})),
		completeAt("handle-message", "message.fern", 1, 2, scopeItems(
			lspItem{"a", 6, "(parameter) i32"}, lspItem{"add", 3, "function add(i32, i32): i32"}, lspItem{"b", 6, "(parameter) i32"})),
		completeAt("literate", "t.fern.md", 3, 33, scopeItems(
			lspItem{"greeting", 3, "function greeting(): i32"}, lspItem{"main", 3, "function main(): i32"})),

		completeAt("past-a-function", "past.fern", 1, 0, scopeItems(lspItem{"f", 3, "function f(): i32"})),
		completeAt("scoped-bindings", "scoped.fern", 11, 2, scopeItems(
			lspItem{"Circle", 20, "(variant of Shape) Circle(f64)"}, lspItem{"LIMIT", 21, "(const) i32"},
			lspItem{"Shape", 13, "enum Shape"}, lspItem{"Square", 20, "(variant of Shape) Square(i32)"},
			lspItem{"area", 3, "function area(Shape, i32): f64"}, lspItem{"f", 6, "(i32) => i32"},
			lspItem{"k", 6, "(parameter) i32"}, lspItem{"n", 6, "i32"}, lspItem{"q", 6, "i32"},
			lspItem{"r", 6, "f64"}, lspItem{"s", 6, "(parameter) Shape"}, lspItem{"w", 6, "string"},
			lspItem{"x", 6, "string"}, lspItem{"x", 6, "(parameter) i32"})),

		completeAt("struct-members", "members.fern", 8, 17, pointMembers),
		completeAt("member-being-written", "members.fern", 8, 18, pointMembers),
		completeAt("module-exports", "members.fern", 8, 30, exactItems(
			lspItem{"Kind", 13, "enum Kind"}, lspItem{"MAX", 21, "(const) i32"}, lspItem{"Point", 22, "struct Point"},
			lspItem{"Text", 20, "(variant of util__Kind) Text"}, lspItem{"foo", 3, "function foo(i32): i32"})),
		completeAt("enum-variants", "members.fern", 9, 23, exactItems(
			lspItem{"Circle", 20, "(variant of Shape) Circle(f64)"}, lspItem{"Square", 20, "(variant of Shape) Square(i32)"})),
		completeAt("array-members", "members.fern", 11, 16, someItems(lspItem{"len", 2, "function len(): i32"})),
		completeAt("string-members-after-non-ascii", "members.fern", 7, sDot, someItems(
			lspItem{"len", 2, "function len(): i32"}, lspItem{"starts_with", 2, "function starts_with(string): boolean"})),
		completeAt("unparsed-access", "unparsed.fern", 3, 11, exactItems(lspItem{"x", 5, "i32"}, lspItem{"y", 5, "i32"})),
	}
	open := []string{"body.fern", "local.fern", "top.fern", "enum.fern", "sorted.fern", "message.fern", "t.fern.md", "past.fern", "scoped.fern", "members.fern", "unparsed.fern"}
	probeLSP(t, s, dir, open, probes)

	frames, _ := runLSP(t, s.selfHost(), session(lspPosition(2, "textDocument/completion", fileURI(filepath.Join(dir, "body.fern")), 2, 2)))
	if got := string(response(t, frames, "2").Result); got != `{"isIncomplete":false,"items":[]}` {
		t.Errorf("completion in a document not open = %s, want an empty list", got)
	}
}

func signatureAt(name, doc string, line, char int, label string, params [][2]int, active int) lspProbe {
	return lspProbe{name: "signature/" + name, method: "textDocument/signatureHelp", doc: doc, params: at(line, char), check: func(t *testing.T, result json.RawMessage) {
		if label == "" {
			if string(result) != "null" {
				t.Errorf("signature help = %s, want null", result)
			}
			return
		}
		var sh lspSignatureHelp
		decode(t, result, &sh)
		if len(sh.Signatures) != 1 {
			t.Fatalf("got %d signatures, want 1: %s", len(sh.Signatures), result)
		}
		var got [][2]int
		for _, p := range sh.Signatures[0].Parameters {
			got = append(got, p.Label)
		}
		if sh.Signatures[0].Label != label || !reflect.DeepEqual(got, params) || sh.ActiveSignature != 0 || sh.ActiveParameter != active {
			t.Errorf("signature help = %s, want %q with parameters %v, parameter %d active", result, label, params, active)
		}
	}}
}

func TestSelfHostLSPSignatureHelpX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	const utfLine = `  let n: i32 = add("é→🐛", `
	writeTree(t, dir, map[string]string{
		"call.fern":    "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n",
		"nested.fern":  "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(add(1, 2), 3); }\n",
		"outside.fern": "function main(): i32 { return 0; }\n",
		"unknown.fern": "function main(): i32 { return undeclared_fn(1, 2); }\n",
		"string.fern":  "function fmt(a: string, b: i32): i32 { return b; }\nfunction main(): i32 { return fmt(\"a, b, c\", 5); }\n",
		"utf16.fern":   "function add(s: string, n: i32): i32 { return n; }\nfunction main(): i32 {\n" + utfLine + "1);\n  return n;\n}\n",
		"util.fern":    "pub function foo(a: i32): i32 { return a; }\n",
		"more.fern": "import \"./util\";\n" +
			"struct Point { x: i32, y: i32 }\n" +
			"function (p: Point) sum(): i32 { return p.x + p.y; }\n" +
			"function id[T](x: T): T { return x; }\n" +
			"function add(a: i32, b: i32): i32 { return a + b; }\n" +
			"function main(): i32 {\n" +
			"  let p: Point = Point { x: 1, y: 2 };\n" +
			"  let s: string = \"abc\";\n" +
			"  let f = (k: i32): i32 => k;\n" +
			"  return p.sum() + util.foo(1) + s.len() + id(3) + add(1, 2, 3) + add([1, 2].len(), 3) + f(1);\n" +
			"}\n",
		"open.fern": "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 {\n  let n: i32 = add(1, \n  return 0;\n}\n",
	})
	addParams := [][2]int{{13, 16}, {18, 21}}
	strParams := [][2]int{{13, 19}, {21, 24}}
	ret := "  return p.sum() + util.foo(1) + s.len() + id(3) + add(1, 2, 3) + add([1, 2].len(), 3) + f(1);"
	after := func(sub string) int { return strings.Index(ret, sub) + len(sub) }
	probes := []lspProbe{
		signatureAt("first-arg", "call.fern", 1, 34, "function add(i32, i32): i32", addParams, 0),
		signatureAt("second-arg", "call.fern", 1, 37, "function add(i32, i32): i32", addParams, 1),
		signatureAt("nested-call-tracks-inner", "nested.fern", 1, 38, "function add(i32, i32): i32", addParams, 0),
		signatureAt("outside-any-call", "outside.fern", 0, 20, "", nil, 0),
		signatureAt("unknown-function", "unknown.fern", 0, 45, "", nil, 0),
		signatureAt("string-with-comma-ignored", "string.fern", 1, 45, "function fmt(string, i32): i32", strParams, 1),
		signatureAt("linked-to-func-sig", "call.fern", 1, 34, "function add(i32, i32): i32", addParams, 0),
		signatureAt("handle-message", "call.fern", 1, 37, "function add(i32, i32): i32", addParams, 1),
		signatureAt("position-is-utf16", "utf16.fern", 2, utf16Column([]string{utfLine}, 1, len(utfLine)+1), "function add(string, i32): i32", strParams, 1),

		signatureAt("method", "more.fern", 9, after("p.sum("), "function sum(): i32", nil, 0),
		signatureAt("module-function", "more.fern", 9, after("util.foo("), "function foo(i32): i32", [][2]int{{13, 16}}, 0),
		signatureAt("builtin-method", "more.fern", 9, after("s.len("), "function len(): i32", nil, 0),
		signatureAt("generic", "more.fern", 9, after("id("), "function id(T): T", [][2]int{{12, 13}}, 0),
		signatureAt("more-arguments-than-parameters", "more.fern", 9, after("add(1, 2, "), "function add(i32, i32): i32", addParams, 1),
		signatureAt("call-that-does-not-parse", "open.fern", 2, 22, "function add(i32, i32): i32", addParams, 1),
		signatureAt("inside-brackets", "more.fern", 9, after("add([1, "), "", nil, 0),
		signatureAt("local-closure", "more.fern", 9, after("f("), "", nil, 0),
	}
	probeLSP(t, s, dir, []string{"call.fern", "nested.fern", "outside.fern", "unknown.fern", "string.fern", "utf16.fern", "more.fern", "open.fern"}, probes)
}

func symbolsOf(name, doc string, want []lspSymbol) lspProbe {
	return lspProbe{name: "symbols/" + name, method: "textDocument/documentSymbol", doc: doc, check: func(t *testing.T, result json.RawMessage) {
		var got []lspSymbol
		decode(t, result, &got)
		if want == nil {
			want = []lspSymbol{}
		}
		if got == nil {
			got = []lspSymbol{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("symbols:\n  %+v\nwant\n  %+v", got, want)
		}
	}}
}

func span(l0, c0, l1, c1 int) lspRange { return lspRange{lspPos{l0, c0}, lspPos{l1, c1}} }

func member(name, detail string, kind int, r lspRange) lspSymbol {
	return lspSymbol{Name: name, Detail: detail, Kind: kind, Range: r, SelectionRange: r}
}

func TestSelfHostLSPDocumentSymbolsX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"decls.fern":    "struct Point { x: i32, y: i32 }\nenum Color { Red, Green }\nfunction main(): i32 { return 0; }\n",
		"fields.fern":   "struct Point { x: i32, y: i32 }\nfunction main(): i32 { return 0; }\n",
		"variants.fern": "enum Color { Red, Green, Blue }\nfunction main(): i32 { return 0; }\n",
		"plain.fern":    "function main(): i32 { return 0; }\n",
		"more.fern": "pub struct Box[T] { v: T }\n" +
			"const LIMIT: i32 = 10;\n" +
			"const S: string = \"é→🐛\"; const K: i32 = 1;\n" +
			"pub function id[T](x: T): T {\n" +
			"  return x;\n" +
			"}\n" +
			"function (b: Box[i32]) get(): i32 { return b.v; }\n" +
			"enum Shape { Circle(f64), Square(i32) }\n",
		"chunks.fern.md": "# Doc\n\n```fern\n<<*>>=\nfunction helper(n: i32): i32 { return n + 1; }\nfunction main(): i32 {\n  <<body>>\n}\n```\n\nThe body.\n\n```fern\n<<body>>=\nlet v = helper(41);\nreturn v;\n```\n",
		"util.fern":      "pub function foo(): i32 { return 42; }\n",
		"main.fern":      "import \"./util\";\nfunction main(): i32 { return util.foo(); }\n",
	})
	mainAt := func(line int) lspSymbol {
		return lspSymbol{Name: "main", Detail: "function main(): i32", Kind: 12, Range: span(line, 0, line, 34), SelectionRange: span(line, 9, line, 13)}
	}
	point := lspSymbol{Name: "Point", Detail: "struct", Kind: 23, Range: span(0, 0, 0, 31), SelectionRange: span(0, 7, 0, 12), Children: []lspSymbol{
		member("x", "i32", 8, span(0, 15, 0, 16)), member("y", "i32", 8, span(0, 23, 0, 24)),
	}}
	probes := []lspProbe{
		symbolsOf("top-level-decls", "decls.fern", []lspSymbol{mainAt(2), point, {Name: "Color", Detail: "enum", Kind: 10, Range: span(1, 0, 1, 25), SelectionRange: span(1, 5, 1, 10), Children: []lspSymbol{
			member("Red", "", 22, span(1, 13, 1, 16)), member("Green", "", 22, span(1, 18, 1, 23)),
		}}}),
		symbolsOf("struct-has-field-children", "fields.fern", []lspSymbol{mainAt(1), point}),
		symbolsOf("enum-has-variant-children", "variants.fern", []lspSymbol{mainAt(1), {Name: "Color", Detail: "enum", Kind: 10, Range: span(0, 0, 0, 31), SelectionRange: span(0, 5, 0, 10), Children: []lspSymbol{
			member("Red", "", 22, span(0, 13, 0, 16)), member("Green", "", 22, span(0, 18, 0, 23)), member("Blue", "", 22, span(0, 25, 0, 29)),
		}}}),
		symbolsOf("skips-internal-names", "plain.fern", []lspSymbol{mainAt(0)}),
		symbolsOf("generic-method-constant-pub", "more.fern", []lspSymbol{
			{Name: "id", Detail: "function id(T): T", Kind: 12, Range: span(3, 0, 5, 1), SelectionRange: span(3, 13, 3, 15)},
			{Name: "get", Detail: "function get(): i32", Kind: 6, Range: span(6, 0, 6, 49), SelectionRange: span(6, 23, 6, 26)},
			{Name: "Box", Detail: "struct", Kind: 23, Range: span(0, 0, 0, 26), SelectionRange: span(0, 11, 0, 14), Children: []lspSymbol{member("v", "T", 8, span(0, 20, 0, 21))}},
			{Name: "Shape", Detail: "enum", Kind: 10, Range: span(7, 0, 7, 39), SelectionRange: span(7, 5, 7, 10), Children: []lspSymbol{
				member("Circle", "", 22, span(7, 13, 7, 19)), member("Square", "", 22, span(7, 26, 7, 32)),
			}},
			{Name: "LIMIT", Detail: "i32", Kind: 14, Range: span(1, 0, 1, 22), SelectionRange: span(1, 6, 1, 11)},
			{Name: "S", Detail: "string", Kind: 14, Range: span(2, 0, 2, 25), SelectionRange: span(2, 6, 2, 7)},
			{Name: "K", Detail: "i32", Kind: 14, Range: span(2, 26, 2, 43), SelectionRange: span(2, 32, 2, 33)},
		}),
		symbolsOf("literate", "chunks.fern.md", []lspSymbol{
			{Name: "helper", Detail: "function helper(i32): i32", Kind: 12, Range: span(4, 0, 4, 46), SelectionRange: span(4, 9, 4, 15)},
			{Name: "main", Detail: "function main(): i32", Kind: 12, Range: span(5, 0, 7, 1), SelectionRange: span(5, 9, 5, 13)},
		}),
		symbolsOf("imports-are-not-listed", "main.fern", []lspSymbol{{Name: "main", Detail: "function main(): i32", Kind: 12, Range: span(1, 0, 1, 43), SelectionRange: span(1, 9, 1, 13)}}),
	}
	probeLSP(t, s, dir, []string{"decls.fern", "fields.fern", "variants.fern", "plain.fern", "more.fern", "chunks.fern.md", "main.fern"}, probes)

	frames, _ := runLSP(t, s.selfHost(), session(lspMsg{"id": 2, "method": "textDocument/documentSymbol", "params": lspMsg{"textDocument": lspMsg{"uri": fileURI(filepath.Join(dir, "plain.fern"))}}}))
	if got := string(response(t, frames, "2").Result); got != "[]" {
		t.Errorf("symbols of a document not open = %s, want []", got)
	}
}

func inlayIn(name, doc string, r lspRange, want ...lspInlay) lspProbe {
	return lspProbe{name: "inlay/" + name, method: "textDocument/inlayHint", doc: doc, params: lspMsg{"range": r}, check: func(t *testing.T, result json.RawMessage) {
		var got []lspInlay
		decode(t, result, &got)
		if want == nil {
			want = []lspInlay{}
		}
		if got == nil {
			got = []lspInlay{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("hints:\n  %+v\nwant\n  %+v", got, want)
		}
	}}
}

func hint(line, char int, label string) lspInlay {
	return lspInlay{Position: lspPos{line, char}, Label: label, Kind: 1}
}

func TestSelfHostLSPInlayHintsX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	const nonASCII = `  let s: string = "é→🐛"; let n = s.len();`
	writeTree(t, dir, map[string]string{
		"inferred.fern":  "function main(): i32 {\n  let x = 7;\n  return x;\n}\n",
		"annotated.fern": "function main(): i32 {\n  let x: i32 = 7;\n  return x;\n}\n",
		"two.fern":       "function main(): i32 {\n  let x = 1;\n  let y = 2;\n  return x + y;\n}\n",
		"shapes.fern": "enum Shape { Circle(f64), Square(i32) }\n" +
			"function main(): i32 {\n" +
			"  let (a, b) = (1, \"s\");\n" +
			"  let f = (k: i32): i32 => k + a;\n" +
			"  for w in [\"a\"] {\n" +
			"    let m = w.len();\n" +
			"  }\n" +
			"  match (Shape.Circle(1.5)) {\n" +
			"    Circle(r) => { let rr = r * r; },\n" +
			"    Square(_) => {}\n" +
			"  }\n" +
			nonASCII + "\n" +
			"  return f(1);\n" +
			"}\n",
		"util.fern":      "pub enum Kind { Text }\n",
		"main.fern":      "import \"./util\";\nfunction main(): i32 {\n  let k = util.Text;\n  return 0;\n}\n",
		"chunks.fern.md": "# Doc\n\n```fern\n<<*>>=\nfunction helper(n: i32): i32 { return n + 1; }\nfunction main(): i32 {\n  <<body>>\n}\n```\n\nThe body.\n\n```fern\n<<body>>=\nlet v = helper(41);\nreturn v;\n```\n",
	})
	whole := span(0, 0, 9999, 0)
	naEnd := utf16Column([]string{nonASCII}, 1, strings.Index(nonASCII, "n =")+2)
	probes := []lspProbe{
		inlayIn("inferred-var", "inferred.fern", whole, hint(1, 7, ": i32")),
		inlayIn("skips-annotated", "annotated.fern", whole),
		inlayIn("filters-by-range", "two.fern", span(2, 0, 2, 100), hint(2, 7, ": i32")),
		inlayIn("range-end-included", "two.fern", span(1, 0, 2, 7), hint(1, 7, ": i32"), hint(2, 7, ": i32")),
		inlayIn("range-end-excluded", "two.fern", span(1, 8, 2, 6)),
		inlayIn("shapes", "shapes.fern", whole,
			hint(2, 8, ": i32"), hint(2, 11, ": string"), hint(3, 7, ": (i32) => i32"),
			hint(5, 9, ": i32"), hint(8, 25, ": f64"), hint(11, naEnd, ": i32")),
		inlayIn("imported-type", "main.fern", whole, hint(2, 7, ": util.Kind")),
		inlayIn("literate", "chunks.fern.md", whole, hint(14, 5, ": i32")),
	}
	probeLSP(t, s, dir, []string{"inferred.fern", "annotated.fern", "two.fern", "shapes.fern", "main.fern", "chunks.fern.md"}, probes)

	frames, _ := runLSP(t, s.selfHost(), session(lspMsg{"id": 2, "method": "textDocument/inlayHint", "params": lspMsg{"textDocument": lspMsg{"uri": fileURI(filepath.Join(dir, "two.fern"))}, "range": whole}}))
	if got := string(response(t, frames, "2").Result); got != "[]" {
		t.Errorf("hints in a document not open = %s, want []", got)
	}
}

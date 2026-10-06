package literate

import (
	"strings"
	"testing"
)

// filesByPath indexes a TangleFiles result for convenient assertions.
func filesByPath(t *testing.T, src string) map[string]FileResult {
	t.Helper()
	results, err := Parse(src).TangleFiles()
	if err != nil {
		t.Fatalf("TangleFiles: %v", err)
	}
	m := map[string]FileResult{}
	for _, r := range results {
		m[r.Path] = r
	}
	return m
}

// A document with two `file=` blocks tangles to two modules; shared
// chunk definitions resolve into whichever file references them.
func TestTangleFilesTwoModules(t *testing.T) {
	src := strings.Join([]string{
		"# Two modules",
		"",
		"```fern file=main.fern entry",
		`import "./util";`,
		"fn main() { util.greet(); }",
		"```",
		"",
		"The helper lives in its own module, assembled from a chunk:",
		"",
		"```fern file=util.fern",
		"<<greet>>",
		"```",
		"```fern",
		"<<greet>>=",
		"pub fn greet() {}",
		"```",
	}, "\n")
	files := filesByPath(t, src)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %v", len(files), files)
	}
	if got := files["main.fern"].Code; got != "import \"./util\";\nfn main() { util.greet(); }" {
		t.Errorf("main.fern =\n%s", got)
	}
	if got := files["util.fern"].Code; got != "pub fn greet() {}" {
		t.Errorf("util.fern = %q, want the expanded greet chunk", got)
	}
	if !files["main.fern"].IsEntry {
		t.Error("main.fern should be marked entry")
	}
	if files["util.fern"].IsEntry {
		t.Error("util.fern should not be entry")
	}
}

// Same path in multiple `file=` blocks concatenates in document order.
func TestTangleFilesConcatenateSamePath(t *testing.T) {
	src := strings.Join([]string{
		"```fern file=app.fern",
		"line one",
		"```",
		"Some prose between the two halves of app.fern:",
		"```fern file=app.fern",
		"line two",
		"```",
	}, "\n")
	files := filesByPath(t, src)
	if got := files["app.fern"].Code; got != "line one\nline two" {
		t.Errorf("app.fern = %q, want concatenated halves", got)
	}
}

// OutputFiles preserves first-definition order; HasFiles distinguishes
// multi-file documents from single-`<<*>>` ones.
func TestOutputFilesOrderAndHasFiles(t *testing.T) {
	multi := Parse("```fern file=z.fern\na\n```\n```fern file=a.fern\nb\n```\n")
	if !multi.HasFiles() {
		t.Fatal("HasFiles should be true for a file= document")
	}
	got := multi.OutputFiles()
	if strings.Join(got, ",") != "z.fern,a.fern" {
		t.Errorf("OutputFiles = %v, want [z.fern a.fern] (definition order)", got)
	}
	single := Parse("```fern\n<<*>>=\nfn main() {}\n```\n")
	if single.HasFiles() {
		t.Error("HasFiles should be false for a <<*>> document")
	}
}

// The per-file line map records the document line each generated line
// came from, so diagnostics can point back at the right `.fern.md` line.
func TestTangleFilesLineMap(t *testing.T) {
	src := strings.Join([]string{
		"```fern file=main.fern", // line 1
		"fn main() {",            // line 2
		"    <<body>>",           // line 3 (indented reference)
		"}",                      // line 4
		"```",                    // line 5
		"```fern",                // line 6
		"<<body>>=",              // line 7
		"return 0;",              // line 8
		"```",                    // line 9
	}, "\n")
	files := filesByPath(t, src)
	lm := files["main.fern"].LineMap
	if len(lm) != 3 {
		t.Fatalf("line map len = %d, want 3", len(lm))
	}
	// generated line 2 ("    return 0;") ← document line 8, +4 indent.
	if lm[1].Lit != 8 || lm[1].ColShift != 4 {
		t.Errorf("lineMap[1] = %+v, want {Lit:8 ColShift:4}", lm[1])
	}
}

func TestEntryFileResolution(t *testing.T) {
	// Explicit entry marker wins.
	e, err := Parse("```fern file=a.fern\nx\n```\n```fern file=b.fern entry\ny\n```\n").EntryFile()
	if err != nil || e != "b.fern" {
		t.Errorf("marked entry: got %q err %v, want b.fern", e, err)
	}
	// Single file needs no marker.
	e, err = Parse("```fern file=only.fern\nx\n```\n").EntryFile()
	if err != nil || e != "only.fern" {
		t.Errorf("single file: got %q err %v, want only.fern", e, err)
	}
	// Ambiguous: multiple files, none marked → error.
	if _, err := Parse("```fern file=a.fern\nx\n```\n```fern file=b.fern\ny\n```\n").EntryFile(); err == nil {
		t.Error("expected an error for multiple files with no entry marker")
	}
}

// An undefined chunk referenced from a file-root is reported against
// the document, at the reference's line.
func TestTangleFilesUndefinedRef(t *testing.T) {
	src := "```fern file=main.fern\n<<missing>>\n```\n"
	_, err := Parse(src).TangleFiles()
	if err == nil {
		t.Fatal("expected an undefined-chunk error")
	}
	pe, ok := err.(*Error)
	if !ok || pe.Position().Line != 2 {
		t.Errorf("error = %v (type %T), want *Error at line 2", err, err)
	}
}

// TangleChunk expands a single named chunk (and its transitive refs),
// not the <<*>> root; an undefined chunk errors.
func TestTangleChunk(t *testing.T) {
	src := "```fern\n<<*>>=\n<<g>>\n```\n```fern\n<<g>>=\nfn g() {\n    <<inner>>\n}\n```\n```fern\n<<inner>>=\nreturn 7;\n```\n"
	doc := Parse(src)
	code, _, err := doc.TangleChunk("g")
	if err != nil {
		t.Fatalf("TangleChunk: %v", err)
	}
	if code != "fn g() {\n    return 7;\n}" {
		t.Errorf("TangleChunk(g) =\n%s", code)
	}
	if _, _, err := doc.TangleChunk("missing"); err == nil {
		t.Error("expected an error for an undefined chunk")
	}
}

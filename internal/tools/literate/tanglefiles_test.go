package literate

import (
	"strings"
	"testing"
)

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

func TestTangleFilesLineMap(t *testing.T) {
	src := strings.Join([]string{
		"```fern file=main.fern",
		"fn main() {",
		"    <<body>>",
		"}",
		"```",
		"```fern",
		"<<body>>=",
		"return 0;",
		"```",
	}, "\n")
	files := filesByPath(t, src)
	lm := files["main.fern"].LineMap
	if len(lm) != 3 {
		t.Fatalf("line map len = %d, want 3", len(lm))
	}
	if lm[1].Lit != 8 || lm[1].ColShift != 4 {
		t.Errorf("lineMap[1] = %+v, want {Lit:8 ColShift:4}", lm[1])
	}
}

func TestEntryFileResolution(t *testing.T) {
	e, err := Parse("```fern file=a.fern\nx\n```\n```fern file=b.fern entry\ny\n```\n").EntryFile()
	if err != nil || e != "b.fern" {
		t.Errorf("marked entry: got %q err %v, want b.fern", e, err)
	}
	e, err = Parse("```fern file=only.fern\nx\n```\n").EntryFile()
	if err != nil || e != "only.fern" {
		t.Errorf("single file: got %q err %v, want only.fern", e, err)
	}
	if _, err := Parse("```fern file=a.fern\nx\n```\n```fern file=b.fern\ny\n```\n").EntryFile(); err == nil {
		t.Error("expected an error for multiple files with no entry marker")
	}
}

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

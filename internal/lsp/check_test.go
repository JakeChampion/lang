package lsp

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
)

const constProgram = "const LIMIT: i32 = 10;\nfunction main(): i32 {\n  return LIMIT;\n}\n"

// The diagnostics are `fern -check`'s, so a use of a top-level const is its
// value rather than an undefined name (#11314) — in every mode. The program
// the cursor features read keeps its consts.
func TestDiagnostics_ConstsAreFolded(t *testing.T) {
	s, uri := openWorkspaceFile(t, t.TempDir(), "main.fern", constProgram)
	docs := map[string]*docState{"workspace": s.docs[uri]}
	single := NewServer()
	single.updateDoc("file:///t", constProgram)
	docs["single-file"] = single.docs["file:///t"]
	for mode, doc := range docs {
		if len(doc.diags) != 0 {
			t.Errorf("%s: published %+v", mode, doc.diags)
		}
		if doc.prog == nil || len(doc.prog.Consts) != 1 {
			t.Errorf("%s: the cursor features' program lost its const", mode)
		}
	}
	if got := literateDiagnosticsFor("```fern\n<<*>>=\n" + constProgram + "```\n"); len(got) != 0 {
		t.Errorf("literate: published %+v", got)
	}
}

// What the gates after the type check find is published: here the
// ambient-effect rule, at the handler.
func TestDiagnostics_IncludeTheGatesAfterTheTypeCheck(t *testing.T) {
	src := "import \"std/http\";\nimport \"std/platform\";\nfunction helper(): void { eprint(\"hit\"); }\nfunction handle(req: HttpRequest, plat: platform.Platform): HttpResponse {\n  helper();\n  return http.ok(\"\");\n}\nfunction main(): i32 { return 0; }\n"
	s, uri := openWorkspaceFile(t, t.TempDir(), "main.fern", src)
	ds := s.docs[uri].diags
	if len(ds) != 1 || ds[0].Code != "E080" {
		t.Fatalf("want one E080, got %+v", ds)
	}
	if got := ds[0].Range.Start; got != at(4, 1) {
		t.Errorf("E080 at %+v, want %+v", got, at(4, 1))
	}
}

// -check's warnings are published as warnings.
func TestDiagnostics_TodoStubIsAWarning(t *testing.T) {
	ds := diagnosticsFor("function f(): i32 {\n  todo;\n}\nfunction main(): i32 {\n  return 0;\n}\n")
	if len(ds) != 1 {
		t.Fatalf("want one diagnostic, got %+v", ds)
	}
	d := ds[0]
	if d.Severity != severityWarning || d.Message != "`todo` stub remaining" || d.Range != (Range{Start: at(2, 3), End: at(2, 4)}) {
		t.Errorf("got %+v", d)
	}
}

// An error at line 0 has no position, whatever its type says, and is
// published with an empty range as one that is not Positioned is.
func TestDiagnostics_PositionlessErrorHasNoRange(t *testing.T) {
	d := toDiagnostic("function main(): i32 { return 0; }\n", &checker.Error{Msg: "m", ErrCode: "E070"})
	if d.Range != (Range{}) || d.Message != "m" || d.Code != "E070" {
		t.Errorf("got %+v", d)
	}
}

// A document with no file loads as stdin does, so its stdlib imports
// resolve.
func TestDiagnostics_SingleFileResolvesStdlibImports(t *testing.T) {
	if ds := diagnosticsFor("import \"std/string\";\nfunction main(): i32 {\n  let s: string = \"abc\";\n  return s.repeat(2).len();\n}\n"); len(ds) != 0 {
		t.Errorf("published %+v", ds)
	}
}

// A literate diagnostic's column is measured on the tangled line it was
// reported against, before it is moved onto the document. The document's
// first line is all two-byte characters, so measuring the tangled line 1
// against it halves the column.
func TestLiterateLSP_ColumnMeasuredOnTheTangledLine(t *testing.T) {
	src := "éééééééééééééééééééééééééééééé\n```fern\n<<*>>=\nfunction main(): i32 { return y; }\n```\n"
	got := literateDiagnosticsFor(src)
	if len(got) != 1 {
		t.Fatalf("want one diagnostic, got %+v", got)
	}
	if got[0].Range.Start != at(4, 31) {
		t.Errorf("at %+v, want %+v", got[0].Range.Start, at(4, 31))
	}
}

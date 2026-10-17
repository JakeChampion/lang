package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type relatedLabel struct {
	Location struct {
		URI   string   `json:"uri"`
		Range lspRange `json:"range"`
	} `json:"location"`
	Message string `json:"message"`
}

func TestSelfHostRelatedLabelSerialization(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := fullSelfHostProject(t)
	const src = `import "./lsp";
import "./util";
function main(): i32 {
  let r: util.Related = util.Related { line: 2, col: 7, length: 2, message: "context" };
  let d: util.Diag = util.Diag { code: "E013", message: "duplicate", line: 1, col: 4, fix: Some(util.Fix { line: 2, col: 5, old: "x", new_text: "y", title: "rename" }), related: [r, util.Related { ...r, line: 3 }] };
  let mapped: util.Diag = util.doc_diag([util.LineMap { doc: "doc.fern.md", lits: [7, 9], shifts: [2, 4] }], d);
  if (mapped.line != 7 || mapped.col != 2 || mapped.related.len() != 1 || mapped.related[0].line != 9 || mapped.related[0].col != 3 || mapped.related[0].length != 2) { return 1; }
  let Some(fix) = mapped.fix else { return 2; };
  if (fix.line != 9 || fix.col != 1) { return 3; }
  if (!util.contains(util.format_diags_in([mapped], []), "note: context (9:3)")) { return 4; }
  let text: lsp.Text = lsp.text_of("🙂ab");
  let a: string = lsp.related_information("file:///a.fern", text, util.Related { line: 1, col: 5, length: 1, message: "first \"label\"" });
  let b: string = lsp.related_information("file:///b.fern", text, util.Related { line: 1, col: 6, length: 0, message: "second" });
  print(lsp.diagnostic_with_related(text, 1, 1, "E013", "duplicate", false, "", [a, b]));
  print(lsp.diagnostic(text, 1, 7, "P001", "at EOF", false, ""));
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "related_run.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "related_run.fern", "related")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("related-label driver: %v\n%s", err, out)
	}
	var d struct {
		Range   lspRange       `json:"range"`
		Related []relatedLabel `json:"relatedInformation"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	if err := decoder.Decode(&d); err != nil {
		t.Fatalf("invalid diagnostic JSON: %v\n%s", err, out)
	}
	if d.Range.Start != (lspPos{Line: 0, Character: 0}) || d.Range.End != (lspPos{Line: 0, Character: 2}) {
		t.Fatalf("primary range splits the supplementary character: %#v", d.Range)
	}
	var eof struct {
		Range lspRange `json:"range"`
	}
	if err := decoder.Decode(&eof); err != nil || eof.Range.Start != (lspPos{Line: 0, Character: 4}) || eof.Range.End != eof.Range.Start {
		t.Fatalf("range extends beyond EOF: %#v, %v", eof.Range, err)
	}
	if len(d.Related) != 2 || d.Related[0].Location.URI != "file:///a.fern" || d.Related[0].Location.Range.Start != (lspPos{Line: 0, Character: 2}) || d.Related[0].Location.Range.End != (lspPos{Line: 0, Character: 3}) || d.Related[0].Message != `first "label"` || d.Related[1].Location.URI != "file:///b.fern" || d.Related[1].Location.Range.Start.Character != 3 || d.Related[1].Location.Range.End.Character != 4 {
		t.Fatalf("wrong related locations: %#v", d.Related)
	}
}

func TestSelfHostDuplicateVariableRelatedLabels(t *testing.T) {
	s := buildLSPServers(t)
	for _, c := range []struct {
		name, entry, file, source string
		files                     map[string]string
		line, character           int
	}{
		{"unicode", "main.fern", "main.fern", "function main(): i32 {\n  print(\"🙂\"); let n = 1;\n  let n = 2;\n  return n;\n}\n", nil, 1, 15},
		{"imported", "main.fern", "lib.fern", "pub function f(): i32 {\n  let n = 1;\n  let n = 2;\n  return n;\n}\n", map[string]string{"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.f(); }\n"}, 1, 2},
		{"literate", "doc.fern.md", "doc.fern.md", "# Example\n\n```fern\n<<*>>=\nfunction main(): i32 {\n  let n = 1;\n  let n = 2;\n  return n;\n}\n```\n", nil, 5, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := c.files
			if files == nil {
				files = map[string]string{}
			}
			files[c.file] = c.source
			writeTree(t, dir, files)
			entry, path := filepath.Join(dir, c.entry), filepath.Join(dir, c.file)
			text, accepted := checkVerdict(exec.Command(s.driver, "-check", entry, s.stdlib))
			if accepted || !strings.Contains(text, "previous declaration is here") {
				t.Fatalf("missing CLI declaration context:\n%s", text)
			}
			msgs := []lspMsg{lspOpen(fileURI(path), c.source)}
			if c.file != c.entry {
				msgs = append(msgs, lspOpen(fileURI(entry), files[c.entry]))
			}
			frames, code := runLSP(t, s.selfHost(), session(msgs...))
			if code != 0 {
				t.Fatalf("LSP exited %d", code)
			}
			var found []relatedLabel
			for _, f := range frames {
				if f.Method != "textDocument/publishDiagnostics" {
					continue
				}
				var p struct {
					URI         string `json:"uri"`
					Diagnostics []struct {
						Code    string         `json:"code"`
						Related []relatedLabel `json:"relatedInformation"`
					} `json:"diagnostics"`
				}
				if err := json.Unmarshal(f.Params, &p); err != nil {
					t.Fatal(err)
				}
				for _, d := range p.Diagnostics {
					if p.URI == fileURI(path) && d.Code == "E013" {
						found = d.Related
					}
				}
			}
			if len(found) != 1 || found[0].Location.URI != fileURI(path) || found[0].Location.Range.Start != (lspPos{Line: c.line, Character: c.character}) || found[0].Message != "previous declaration is here" {
				t.Fatalf("wrong related declaration: %#v", found)
			}
		})
	}
}

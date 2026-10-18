package e2ecompiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// `fern -lsp` is the language server (#6641): compiler/lsp.fern is the wire,
// fern.fern's check_source the diagnostics, and its format_source the
// formatting. These tests drive it as an editor does — framed JSON-RPC on
// stdin, everything read back from stdout once the input is closed — and hold
// it to `-check`, whose findings it must publish exactly, and to the wire
// answers written out below.

// lspMsg is one message a test sends.
type lspMsg = map[string]any

// lspFrame is one message a server wrote. Result is nil when the member was
// absent and `null` when it was sent as null.
type lspFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type lspPos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPos `json:"start"`
	End   lspPos `json:"end"`
}

type lspDiag struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Source   string   `json:"source"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
}

type lspPublish struct {
	URI         string    `json:"uri"`
	Diagnostics []lspDiag `json:"diagnostics"`
}

type lspEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// lspServers holds the server under test: the self-host CLI, which selfHost
// starts in server mode.
type lspServers struct {
	driver string
	runner []string
	stdlib string
}

func (s lspServers) selfHost() *exec.Cmd {
	return runX86_64Bin(s.runner, s.driver, "-lsp", s.stdlib)
}

func buildLSPServers(t *testing.T) lspServers {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	return lspServers{driver: driver, runner: runner, stdlib: stdlib}
}

func lspFrameBytes(t *testing.T, m lspMsg) []byte {
	t.Helper()
	m["jsonrpc"] = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(b))), b...)
}

// runLSPRaw writes input to a server, closes its stdin, and returns what it
// wrote, its exit code and its stderr.
func runLSPRaw(t *testing.T, cmd *exec.Cmd, input []byte) ([]lspFrame, int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v", cmd.Path, err)
	}
	return parseLSPFrames(t, stdout.Bytes()), code, stderr.String()
}

func runLSP(t *testing.T, cmd *exec.Cmd, msgs []lspMsg) ([]lspFrame, int) {
	t.Helper()
	var in []byte
	for _, m := range msgs {
		in = append(in, lspFrameBytes(t, m)...)
	}
	frames, code, stderr := runLSPRaw(t, cmd, in)
	if stderr != "" {
		t.Logf("%s stderr: %s", cmd.Path, stderr)
	}
	return frames, code
}

func parseLSPFrames(t *testing.T, out []byte) []lspFrame {
	t.Helper()
	var frames []lspFrame
	for len(out) > 0 {
		hdr, rest, ok := bytes.Cut(out, []byte("\r\n\r\n"))
		if !ok {
			t.Fatalf("unframed output: %q", out)
		}
		n := -1
		for _, line := range strings.Split(string(hdr), "\r\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if n < 0 || n > len(rest) {
			t.Fatalf("bad frame header %q before %d bytes", hdr, len(rest))
		}
		var f lspFrame
		if err := json.Unmarshal(rest[:n], &f); err != nil {
			t.Fatalf("frame body is not JSON: %v\n%s", err, rest[:n])
		}
		frames = append(frames, f)
		out = rest[n:]
	}
	return frames
}

func lspInitialize() lspMsg {
	return lspMsg{"id": 1, "method": "initialize", "params": lspMsg{}}
}

func lspShutdownExit() []lspMsg {
	return []lspMsg{{"id": 999, "method": "shutdown"}, {"method": "exit"}}
}

func lspOpen(uri, text string) lspMsg {
	return lspMsg{"method": "textDocument/didOpen", "params": lspMsg{"textDocument": lspMsg{"uri": uri, "languageId": "fern", "version": 1, "text": text}}}
}

func lspChange(uri, text string) lspMsg {
	return lspMsg{"method": "textDocument/didChange", "params": lspMsg{"textDocument": lspMsg{"uri": uri, "version": 2}, "contentChanges": []lspMsg{{"text": text}}}}
}

func lspClose(uri string) lspMsg {
	return lspMsg{"method": "textDocument/didClose", "params": lspMsg{"textDocument": lspMsg{"uri": uri}}}
}

func lspFormat(id int, uri string) lspMsg {
	return lspMsg{"id": id, "method": "textDocument/formatting", "params": lspMsg{"textDocument": lspMsg{"uri": uri}, "options": lspMsg{"tabSize": 2, "insertSpaces": true}}}
}

func fileURI(path string) string { return "file://" + filepath.ToSlash(path) }

// session wraps msgs in initialize … shutdown/exit.
func session(msgs ...lspMsg) []lspMsg {
	return append(append([]lspMsg{lspInitialize()}, msgs...), lspShutdownExit()...)
}

func publishes(t *testing.T, frames []lspFrame) []lspPublish {
	t.Helper()
	var out []lspPublish
	for _, f := range frames {
		if f.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p lspPublish
		if err := json.Unmarshal(f.Params, &p); err != nil {
			t.Fatalf("publishDiagnostics params: %v\n%s", err, f.Params)
		}
		if p.Diagnostics == nil {
			t.Fatalf("publishDiagnostics for %s sent no diagnostics array: %s", p.URI, f.Params)
		}
		out = append(out, p)
	}
	return out
}

func response(t *testing.T, frames []lspFrame, id string) lspFrame {
	t.Helper()
	for _, f := range frames {
		if string(f.ID) == id && f.Method == "" {
			return f
		}
	}
	t.Fatalf("no response to request %s among %d frames", id, len(frames))
	return lspFrame{}
}

// placed is a diagnostic as the wire places it: everything but the message,
// whose wording is the checker's and is held by the checker's own tests.
type placed struct {
	Range    lspRange
	Severity int
	Source   string
	Code     string
}

func placements(ps []lspPublish) [][]placed {
	out := [][]placed{}
	for _, p := range ps {
		row := []placed{}
		for _, d := range p.Diagnostics {
			row = append(row, placed{d.Range, d.Severity, d.Source, d.Code})
		}
		out = append(out, row)
	}
	return out
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSelfHostLSPLifecycleX86_64 pins the protocol around the documents: the
// capabilities advertised, which messages are answered and how, the exit
// codes the spec assigns, and what the framing tolerates.
func TestSelfHostLSPLifecycleX86_64(t *testing.T) {
	s := buildLSPServers(t)

	t.Run("initialize", func(t *testing.T) {
		frames, code := runLSP(t, s.selfHost(), session())
		var got struct {
			Capabilities struct {
				TextDocumentSync           int  `json:"textDocumentSync"`
				DocumentFormattingProvider bool `json:"documentFormattingProvider"`
				HoverProvider              bool `json:"hoverProvider"`
				DefinitionProvider         bool `json:"definitionProvider"`
				CompletionProvider         *struct {
					TriggerCharacters []string `json:"triggerCharacters"`
				} `json:"completionProvider"`
				SignatureHelpProvider *struct {
					TriggerCharacters   []string `json:"triggerCharacters"`
					RetriggerCharacters []string `json:"retriggerCharacters"`
				} `json:"signatureHelpProvider"`
				DocumentSymbolProvider bool `json:"documentSymbolProvider"`
				InlayHintProvider      bool `json:"inlayHintProvider"`
				CodeActionProvider     bool `json:"codeActionProvider"`
				ReferencesProvider     bool `json:"referencesProvider"`
				RenameProvider         bool `json:"renameProvider"`
				SemanticTokensProvider *struct {
					Full bool `json:"full"`
				} `json:"semanticTokensProvider"`
			} `json:"capabilities"`
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		}
		if err := json.Unmarshal(response(t, frames, "1").Result, &got); err != nil {
			t.Fatal(err)
		}
		c := got.Capabilities
		if c.TextDocumentSync != 1 || !c.DocumentFormattingProvider || !c.HoverProvider || !c.DefinitionProvider || !c.DocumentSymbolProvider || !c.InlayHintProvider || got.ServerInfo.Name != "fern" {
			t.Errorf("initialize result = %+v, want full sync, formatting, hover, definition, document symbols, inlay hints, server fern", got)
		}
		// Plain booleans, and full semantic tokens (the legend is held in
		// self_host_lsp_refs_test.go).
		if !c.CodeActionProvider || !c.ReferencesProvider || !c.RenameProvider || c.SemanticTokensProvider == nil || !c.SemanticTokensProvider.Full {
			t.Errorf("initialize result = %+v, want code actions, references, rename and full semantic tokens", got)
		}
		if c.CompletionProvider == nil || !reflect.DeepEqual(c.CompletionProvider.TriggerCharacters, []string{"."}) {
			t.Errorf("completionProvider = %+v, want triggered by `.`", c.CompletionProvider)
		}
		if sh := c.SignatureHelpProvider; sh == nil || !reflect.DeepEqual(sh.TriggerCharacters, []string{"(", ","}) || !reflect.DeepEqual(sh.RetriggerCharacters, []string{","}) {
			t.Errorf("signatureHelpProvider = %+v, want triggered by `(` and `,`, retriggered by `,`", sh)
		}
		if code != 0 {
			t.Errorf("exit after shutdown = %d, want 0", code)
		}
	})

	// Requests are answered, with the id echoed verbatim whatever its JSON
	// type; notifications never are, a known one or not; a request for a
	// method the server lacks is the spec's -32601; shutdown's result is an
	// explicit null.
	t.Run("answers", func(t *testing.T) {
		frames, code := runLSP(t, s.selfHost(), []lspMsg{
			lspInitialize(),
			{"method": "initialized", "params": lspMsg{}},
			{"method": "$/cancelRequest", "params": lspMsg{"id": 1}},
			{"id": "req-a", "method": "workspace/symbol", "params": lspMsg{"query": ""}},
			{"id": 7, "method": "shutdown"},
			{"method": "exit"},
		})
		if len(frames) != 3 {
			t.Fatalf("got %d frames, want 3 (initialize, workspace/symbol, shutdown): %+v", len(frames), frames)
		}
		ws := response(t, frames, `"req-a"`)
		if ws.Error == nil || ws.Error.Code != -32601 || ws.Result != nil {
			t.Errorf("workspace/symbol answered %+v, want error -32601 and no result", ws)
		}
		if sd := response(t, frames, "7"); string(sd.Result) != "null" || sd.Error != nil {
			t.Errorf("shutdown answered result=%q error=%+v, want result null", sd.Result, sd.Error)
		}
		if code != 0 {
			t.Errorf("exit after shutdown = %d, want 0", code)
		}
	})

	t.Run("exit-without-shutdown", func(t *testing.T) {
		if _, code := runLSP(t, s.selfHost(), []lspMsg{lspInitialize(), {"method": "exit"}}); code != 1 {
			t.Errorf("exit without shutdown = %d, want 1", code)
		}
	})

	t.Run("hang-up", func(t *testing.T) {
		frames, code := runLSP(t, s.selfHost(), []lspMsg{lspInitialize()})
		if len(frames) != 1 || code != 0 {
			t.Errorf("hang-up after initialize: %d frames, exit %d; want 1 frame, exit 0", len(frames), code)
		}
	})

	// Header names are case-insensitive and Content-Type is allowed; a body
	// that is not JSON is dropped without ending the session.
	t.Run("framing", func(t *testing.T) {
		init := lspFrameBytes(t, lspInitialize())
		body := init[bytes.Index(init, []byte("\r\n\r\n"))+4:]
		in := []byte("content-length: " + strconv.Itoa(len(body)) + "\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n")
		in = append(in, body...)
		in = append(in, []byte("Content-Length: 8\r\n\r\nnot json")...)
		for _, m := range lspShutdownExit() {
			in = append(in, lspFrameBytes(t, m)...)
		}
		frames, code, stderr := runLSPRaw(t, s.selfHost(), in)
		if len(frames) != 2 || code != 0 {
			t.Errorf("got %d frames, exit %d (stderr %q); want initialize + shutdown answered, exit 0", len(frames), code, stderr)
		}
	})

	for _, c := range []struct{ name, in string }{
		{"truncated-body", "Content-Length: 100\r\n\r\n{\"jsonrpc\":\"2.0\"}"},
		{"no-content-length", "Content-Type: application/json\r\n\r\n{}"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, code, stderr := runLSPRaw(t, s.selfHost(), []byte(c.in))
			if code != 2 || !strings.Contains(stderr, "fern -lsp:") {
				t.Errorf("exit %d, stderr %q; want exit 2 naming the framing failure", code, stderr)
			}
		})
	}
}

// lspCases are programs placed through every shape the wire conversion has:
// no diagnostic, one, several, a line with non-ASCII before the position (2-
// and 4-byte UTF-8: one and two UTF-16 units), CRLF line endings, a parse
// marker, a positionless refusal, a top-level const, imports from a sibling
// and from the stdlib, a sibling's private function, and a warning. `want` is
// what opening main.fern publishes.
var lspCases = []struct {
	name  string
	files map[string]string // main.fern is the document opened
	want  []placed
}{
	{"clean", map[string]string{"main.fern": "function main(): i32 {\n  return 0;\n}\n"}, []placed{}},
	{"undefined", map[string]string{"main.fern": "function main(): i32 {\n  return y;\n}\n"}, []placed{{span(1, 9, 1, 10), 1, "fern", "E001"}}},
	{"assign-mismatch", map[string]string{"main.fern": "function main(): i32 {\n  let x: i32 = \"s\";\n  return x;\n}\n"}, []placed{{span(1, 2, 1, 3), 1, "fern", "E003"}}},
	{"arg-count", map[string]string{"main.fern": "function f(a: i32): i32 {\n  return a;\n}\nfunction main(): i32 {\n  return f(1, 2);\n}\n"}, []placed{{span(4, 10, 4, 11), 1, "fern", "E004"}}},
	{"return-type", map[string]string{"main.fern": "function main(): i32 {\n  return \"s\";\n}\n"}, []placed{{span(1, 2, 1, 3), 1, "fern", "E002"}}},
	{"missing-field", map[string]string{"main.fern": "struct P { x: i32, y: i32 }\nfunction main(): i32 {\n  let p: P = P { x: 1 };\n  return p.x;\n}\n"}, []placed{{span(2, 13, 2, 14), 1, "fern", "E005"}}},
	{"nonbool-if", map[string]string{"main.fern": "function main(): i32 {\n  if (1) {\n    return 1;\n  }\n  return 0;\n}\n"}, []placed{{span(1, 6, 1, 7), 1, "fern", "E008"}}},
	{"break-outside-loop", map[string]string{"main.fern": "function main(): i32 {\n  break;\n  return 0;\n}\n"}, []placed{{span(1, 2, 1, 3), 1, "fern", "E011"}}},
	{"several", map[string]string{"main.fern": "function main(): i32 {\n  let a: i32 = p;\n  let b: i32 = q;\n  return a + b;\n}\n"}, []placed{{span(1, 15, 1, 16), 1, "fern", "E001"}, {span(2, 15, 2, 16), 1, "fern", "E001"}}},
	{"two-byte-before", map[string]string{"main.fern": "function main(): i32 {\n  let s: string = \"é\"; let x: i32 = y;\n  return 0;\n}\n"}, []placed{{span(1, 36, 1, 37), 1, "fern", "E001"}}},
	{"astral-before", map[string]string{"main.fern": "function main(): i32 {\n  let s: string = \"\U0001F600\"; let x: i32 = y;\n  return 0;\n}\n"}, []placed{{span(1, 37, 1, 38), 1, "fern", "E001"}}},
	{"crlf", map[string]string{"main.fern": "function main(): i32 {\r\n  return y;\r\n}\r\n"}, []placed{{span(1, 9, 1, 10), 1, "fern", "E001"}}},
	{"parse-marker", map[string]string{"main.fern": "function main(): i32 {\n  let x: i32 = ;\n  return 0;\n}\n"}, []placed{{span(1, 15, 1, 16), 1, "fern", "P001"}}},
	{"unresolved-import", map[string]string{"main.fern": "import \"./nope\";\nfunction main(): i32 {\n  return 0;\n}\n"}, []placed{{span(0, 0, 0, 0), 1, "fern", ""}}},
	{"sibling-import", map[string]string{
		"main.fern": "import \"./lib\";\nfunction main(): i32 {\n  return lib.shown() + z;\n}\n",
		"lib.fern":  "pub function shown(): i32 { return 2; }\n",
	}, []placed{{span(2, 23, 2, 24), 1, "fern", "E001"}}},
	{"stdlib-import", map[string]string{"main.fern": "import \"std/string\";\nfunction main(): i32 {\n  let s: string = \"abc\";\n  return s.len() + w;\n}\n"}, []placed{{span(3, 19, 3, 20), 1, "fern", "E001"}}},
	{"const", map[string]string{"main.fern": "const LIMIT: i32 = 10;\nfunction main(): i32 {\n  return LIMIT + y;\n}\n"}, []placed{{span(2, 17, 2, 18), 1, "fern", "E001"}}},
	{"private-in-sibling", map[string]string{
		"main.fern": "import \"./lib\";\nfunction main(): i32 {\n  return lib.hidden();\n}\n",
		"lib.fern":  "function hidden(): i32 { return 1; }\n",
	}, []placed{{span(2, 12, 2, 13), 1, "fern", ""}}},
	{"todo-stub", map[string]string{"main.fern": "function f(): i32 {\n  todo;\n}\nfunction main(): i32 {\n  return 0;\n}\n"}, []placed{{span(1, 2, 1, 3), 2, "fern", ""}}},
}

// TestSelfHostLSPDiagnosticPlacementX86_64 opens each case and wants its
// diagnostics published once, placed as written.
func TestSelfHostLSPDiagnosticPlacementX86_64(t *testing.T) {
	s := buildLSPServers(t)
	for _, c := range lspCases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, c.files)
			sf, _ := runLSP(t, s.selfHost(), session(lspOpen(fileURI(filepath.Join(dir, "main.fern")), c.files["main.fern"])))
			if got, want := placements(publishes(t, sf)), [][]placed{c.want}; !reflect.DeepEqual(got, want) {
				t.Errorf("published %+v\nwant      %+v", got, want)
			}
		})
	}
}

// TestSelfHostLSPDocumentSyncX86_64 drives one document through an editing
// session: a publish when the diagnostics change, none when the text or the
// diagnostics do not, an empty one on close, and a fresh one on reopening.
func TestSelfHostLSPDocumentSyncX86_64(t *testing.T) {
	s := buildLSPServers(t)
	// A space in the directory is a %20 in the URI the server must decode to
	// find the document's sibling import.
	dir := filepath.Join(t.TempDir(), "a project")
	writeTree(t, dir, map[string]string{
		"lib.fern":  "pub function one(): i32 { return 1; }\n",
		"main.fern": "function main(): i32 {\n  return nope;\n}\n",
	})
	uri := "file://" + strings.ReplaceAll(filepath.ToSlash(filepath.Join(dir, "main.fern")), " ", "%20")
	broken := "import \"./lib\";\nfunction main(): i32 {\n  return lib.one() + x;\n}\n"
	fixed := "import \"./lib\";\nfunction main(): i32 {\n  return lib.one();\n}\n"
	alsoFine := "import \"./lib\";\nfunction main(): i32 {\n  return lib.one() + 1;\n}\n"
	untitled := "untitled:Untitled-1"
	sf, code := runLSP(t, s.selfHost(), session(
		lspOpen(uri, broken),     // publish: one E001
		lspChange(uri, fixed),    // publish: none left — the buffer, not the disk
		lspChange(uri, fixed),    // same text: nothing
		lspChange(uri, alsoFine), // new text, same (empty) diagnostics: nothing
		lspChange(uri, broken),   // publish: E001 again
		lspClose(uri),            // publish: empty
		lspOpen(uri, broken),     // publish: E001, the first since closing
		lspOpen(untitled, "function main(): i32 {\n  return u;\n}\n"),
	))
	e001 := []placed{{span(2, 21, 2, 22), 1, "fern", "E001"}}
	wantURIs := []string{uri, uri, uri, uri, uri, untitled}
	want := [][]placed{e001, {}, e001, {}, e001, {{span(1, 9, 1, 10), 1, "fern", "E001"}}}
	got := publishes(t, sf)
	var gotURIs []string
	for _, p := range got {
		gotURIs = append(gotURIs, p.URI)
	}
	if !reflect.DeepEqual(gotURIs, wantURIs) {
		t.Errorf("published for %v\nwant          %v", gotURIs, wantURIs)
	}
	if g := placements(got); !reflect.DeepEqual(g, want) {
		t.Errorf("published %+v\nwant      %+v", g, want)
	}
	if code != 0 {
		t.Errorf("exit after shutdown = %d, want 0", code)
	}
}

// TestSelfHostLSPFormattingX86_64 wants each formatting request answered with
// one edit replacing the document with what `-fmt` writes, its end in UTF-16
// units, or none when the text is already formatted, does not parse, or is not
// open.
func TestSelfHostLSPFormattingX86_64(t *testing.T) {
	s := buildLSPServers(t)
	for _, c := range []struct {
		name, src string
		want      []lspEdit
	}{
		{"tabs", "function main(): i32 {\n\t\treturn 0;\n}\n", []lspEdit{{span(0, 0, 3, 0), "function main(): i32 {\n  return 0;\n}\n"}}},
		{"authored-diagnostic-marker", "// The parser prints /*unknown-stmt*/.\nfunction main(): i32 {return 0;}\n", []lspEdit{{span(0, 0, 2, 0), "// The parser prints /*unknown-stmt*/.\nfunction main(): i32 {\n  return 0;\n}\n"}}},
		{"comments", "// header\nfunction main(): i32 {\n  // inside\n  let x: i32 = 7;   // trailing\n  return x;\n}\n",
			[]lspEdit{{span(0, 0, 6, 0), "// header\nfunction main(): i32 {\n  // inside\n  let x: i32 = 7;  // trailing\n  return x;\n}\n"}}},
		{"blank-lines", "function a(): i32 { return 1; }\n\n\n\nfunction main(): i32 {   return a(); }\n",
			[]lspEdit{{span(0, 0, 5, 0), "function a(): i32 {\n  return 1;\n}\n\nfunction main(): i32 {\n  return a();\n}\n"}}},
		{"struct-and-match", "enum E { A, B(i32) }\nstruct P { x: i32 }\nfunction main(): i32 {\nlet p: P = P{x:1};\nlet e: E = B(2);\nmatch (e) { A => { return p.x; }, B(n) => { return n; } }\n}\n",
			[]lspEdit{{span(0, 0, 7, 0), "enum E { A, B(i32) }\n\nstruct P { x: i32 }\n\nfunction main(): i32 {\n  let p: P = P { x: 1 };\n  let e: E = B(2);\n  match (e) {\n    A => {\n      return p.x;\n    },\n    B(n) => {\n      return n;\n    }\n  }\n}\n"}}},
		{"non-ascii-last-line", "function main(): i32 {\n  return 0;\n}\n// café", []lspEdit{{span(0, 0, 3, 7), "function main(): i32 {\n  return 0;\n}\n// café\n"}}},
		{"formatted", "function main(): i32 {\n  return 0;\n}\n", []lspEdit{}},
		{"does-not-parse", "function main(): i32 { return", []lspEdit{}},
		{"missing-last-brace", "function main(): i32 {\n  return 0;\n", []lspEdit{}},
		{"missing-semicolon", "function main(): i32 {\n  let x: i32 = 1\n  return x;\n}\n", []lspEdit{}},
		{"stray-token", "function main(): i32 {\n  return @;\n}\n", []lspEdit{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			writeTree(t, filepath.Dir(path), map[string]string{"main.fern": c.src})
			sf, _ := runLSP(t, s.selfHost(), session(lspOpen(fileURI(path), c.src), lspFormat(2, fileURI(path)), lspFormat(3, "file:///nowhere/unopened.fern")))
			for _, r := range []struct {
				id   string
				want []lspEdit
			}{{"2", c.want}, {"3", []lspEdit{}}} {
				var got []lspEdit
				if err := json.Unmarshal(response(t, sf, r.id).Result, &got); err != nil {
					t.Fatalf("formatting %s: %v", r.id, err)
				}
				if !reflect.DeepEqual(got, r.want) {
					t.Errorf("request %s answered %+v\nwant %+v", r.id, got, r.want)
				}
			}
		})
	}
}

// TestSelfHostLSPFormattingLiterateX86_64 wants a `.fern.md` document
// formatted as `-fmt` formats it — its chunk bodies, not the whole Markdown
// file read as Fern, which answered with an edit emptying the document (#12645).
func TestSelfHostLSPFormattingLiterateX86_64(t *testing.T) {
	s := buildLSPServers(t)
	src := "# Doc\n\nfunction   prose( ){}\n\n```fern\n<<*>>=\nfunction   main( ):i32{return 0;}\n```\n"
	want := []lspEdit{{span(0, 0, 8, 0), "# Doc\n\nfunction   prose( ){}\n\n```fern\n<<*>>=\nfunction main(): i32 {\n  return 0;\n}\n```\n"}}
	path := filepath.Join(t.TempDir(), "doc.fern.md")
	writeTree(t, filepath.Dir(path), map[string]string{"doc.fern.md": src})
	sf, _ := runLSP(t, s.selfHost(), session(lspOpen(fileURI(path), src), lspFormat(2, fileURI(path))))
	var got []lspEdit
	if err := json.Unmarshal(response(t, sf, "2").Result, &got); err != nil {
		t.Fatalf("formatting: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("formatting answered %+v\nwant %+v", got, want)
	}
}

// checkLine matches one line of `-check` output that names a position in the
// entry module; a position in another module carries its path first, as a
// `todo` warning carries the entry's.
var (
	checkLine          = regexp.MustCompile(`^(\d+):(\d+): (.*)$`)
	checkLineElsewhere = regexp.MustCompile(`^\S+:\d+:\d+: `)
	diagnosticCode     = regexp.MustCompile(`^[EP]\d{3}$`)
)

// expectedFromCheck is what the server must publish for a document, derived
// from what `-check` prints for the same file: one diagnostic per line, at the
// line's position converted to LSP's, carrying its code, severity and
// message. A line in an imported module is not the document's.
func expectedFromCheck(t *testing.T, path, src, out string) []lspDiag {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	want := []lspDiag{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		// An indented line is the source line or caret under a diagnostic.
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		d := lspDiag{Severity: 1, Source: "fern"}
		rest := line
		if m := checkLine.FindStringSubmatch(strings.TrimPrefix(line, path+":")); m != nil {
			ln, _ := strconv.Atoi(m[1])
			col, _ := strconv.Atoi(m[2])
			ch := utf16Column(lines, ln, col)
			width := 0
			if ln > 0 && ln <= len(lines) && col > 0 && col <= len(lines[ln-1]) {
				r, _ := utf8.DecodeRuneInString(lines[ln-1][col-1:])
				width = 1
				if r > 0xffff {
					width = 2
				}
			}
			d.Range = lspRange{lspPos{ln - 1, ch}, lspPos{ln - 1, ch + width}}
			rest = m[3]
		} else if checkLineElsewhere.MatchString(line) {
			continue
		}
		switch {
		case strings.HasPrefix(rest, "warning: "):
			d.Severity = 2
			rest = strings.TrimPrefix(rest, "warning: ")
		case strings.HasPrefix(rest, "error["):
			code, msg, _ := strings.Cut(strings.TrimPrefix(rest, "error["), "]: ")
			if diagnosticCode.MatchString(code) {
				d.Code = code
			}
			rest = msg
		}
		d.Message = strings.TrimPrefix(rest, "fern: ")
		want = append(want, d)
	}
	return want
}

// utf16Column is LSP's 0-based UTF-16 offset for Fern's 1-based byte column
// on 1-based line ln.
func utf16Column(lines []string, ln, col int) int {
	if col < 1 {
		col = 1
	}
	if ln < 1 || ln > len(lines) {
		return col - 1
	}
	prefix := lines[ln-1]
	if col-1 < len(prefix) {
		prefix = prefix[:col-1]
	}
	n := 0
	for _, r := range prefix {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	if !utf8.ValidString(prefix) {
		return col - 1
	}
	return n
}

// TestSelfHostLSPPublishesCheckFindingsX86_64 holds the server to its
// definition: what it publishes for a document is what `-check` reports for
// that file — every finding in the document, at its position, with its code,
// severity and message — and nothing else. It runs over every rejection case
// in the conformance corpus, where `-check` has the most to say, plus a few
// shapes the corpus does not have.
//
// This is what makes the server's diagnostics the self-host checker's, so the
// checker differentials gate them too. Where those report a gap, the server
// reports the same gap: it is not this test's business to judge the checker.
func TestSelfHostLSPPublishesCheckFindingsX86_64(t *testing.T) {
	s := buildLSPServers(t)
	type doc struct{ name, path, src string }
	var docs []doc
	cases, err := filepath.Glob("../../../conformance/cases/*/expected.error")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 60 {
		t.Fatalf("found only %d expected.error conformance cases — the corpus moved and this proves nothing", len(cases))
	}
	for _, ec := range cases {
		p, err := filepath.Abs(filepath.Join(filepath.Dir(ec), "main.fern"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc{filepath.Base(filepath.Dir(ec)), p, string(b)})
	}
	extra := t.TempDir()
	writeTree(t, extra, map[string]string{
		"const/main.fern":        "const LIMIT: i32 = 10;\nfunction main(): i32 {\n  return LIMIT + y;\n}\n",
		"private/main.fern":      "import \"./lib\";\nfunction main(): i32 {\n  return lib.hidden();\n}\n",
		"private/lib.fern":       "function hidden(): i32 { return 1; }\n",
		"non-ascii/main.fern":    "function main(): i32 {\n  let s: string = \"日本\"; let x: i32 = y;\n  return 0;\n}\n",
		"in-an-import/main.fern": "import \"./lib\";\nfunction main(): i32 {\n  return lib.f();\n}\n",
		"in-an-import/lib.fern":  "pub function f(): i32 {\n  return nope;\n}\n",
		"todo/main.fern":         "function f(): i32 {\n  todo;\n}\nfunction main(): i32 {\n  return 0;\n}\n",
	})
	for _, name := range []string{"const", "private", "non-ascii", "in-an-import", "todo"} {
		p := filepath.Join(extra, name, "main.fern")
		b, _ := os.ReadFile(p)
		docs = append(docs, doc{name, p, string(b)})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].name < docs[j].name })

	// One server for the whole corpus: it is a long-running process, and each
	// document is checked on its own.
	var msgs []lspMsg
	for _, d := range docs {
		msgs = append(msgs, lspOpen(fileURI(d.path), d.src))
	}
	frames, code := runLSP(t, s.selfHost(), session(msgs...))
	if code != 0 {
		t.Fatalf("server exited %d", code)
	}
	got := map[string][]lspDiag{}
	for _, p := range publishes(t, frames) {
		got[p.URI] = p.Diagnostics
	}
	if len(got) != len(docs) {
		t.Fatalf("published for %d documents, want %d", len(got), len(docs))
	}
	findings := 0
	for _, d := range docs {
		t.Run(d.name, func(t *testing.T) {
			cmd := runX86_64Bin(s.runner, s.driver, "-check", d.path, s.stdlib)
			cmd.Dir = filepath.Dir(d.path)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			want := expectedFromCheck(t, d.path, d.src, stderr.String())
			findings += len(want)
			if have := got[fileURI(d.path)]; !reflect.DeepEqual(have, want) {
				t.Errorf("published %+v\n-check says %+v\n(-check output: %q)", have, want, stderr.String())
			}
		})
	}
	if findings < len(cases) {
		t.Errorf("%d findings across %d rejection cases — `-check` stopped reporting them", findings, len(cases))
	}
}

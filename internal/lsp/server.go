package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// Server holds the per-session state for an LSP session: the open
// documents and a sink for diagnostics-publishing notifications.
//
// The Server is single-threaded — the read loop dispatches messages
// one at a time. If we later want incremental re-checking on a debounce
// timer that runs off the read loop, that's the place to add a mutex.
type Server struct {
	// docs is the in-memory mirror of every document the editor has
	// opened. Keyed by URI; value carries the source text plus the
	// most recent parse + check products. Removed on didClose.
	//
	// We cache prog + info so hover / definition don't redo work
	// per cursor move — the editor sends those requests far more
	// often than didChange. The cache is rebuilt on every
	// didOpen / didChange.
	docs map[string]*docState

	// publish is called whenever the server decides to push a
	// notification (e.g. publishDiagnostics) back to the client.
	// In stdio mode this writes a framed JSON-RPC message to stdout;
	// the wasm wrapper plugs in a JS-callback-backed sink instead.
	publish func(method string, params any)

	// shutdownRequested flips on a `shutdown` request and gates
	// `exit` — per the spec, exit after shutdown is a clean 0,
	// exit without shutdown is non-zero. The transport (stdio main,
	// wasm wrapper) reads ExitCode after Serve returns to act on
	// this.
	shutdownRequested bool
	exited            bool
	exitCode          int

	// cache memoises parse + check by source content so undo / redo,
	// re-opens, and across-document duplicates skip the pipeline.
	// Shared across all open URIs because the compile output is a
	// pure function of the input — there's no per-URI context yet
	// (module loading happens once at Check time).
	cache *compileCache

	// lastDiags records the most recently published diagnostic list
	// per URI so publishDiagnostics can skip the wire send when
	// nothing has changed. Editors filter dedup themselves, but
	// dropping at the source saves the JSON marshal + the client's
	// debounce cycle.
	lastDiags map[string][]Diagnostic

	// workspace flips the URI-resolution strategy: when true, file://
	// URIs route through modload (with the in-memory document buffer
	// overriding disk for open files). cmd/fern-lsp sets it; the
	// wasm wrapper leaves it off because the browser has no
	// filesystem to read sibling modules from.
	workspace bool
}

// NewServer returns a fresh Server with no open documents. Use Serve
// (for stdio) or call HandleMessage directly (for in-process drivers
// like the wasm playground).
func NewServer() *Server {
	return &Server{
		docs:      map[string]*docState{},
		cache:     newCompileCache(16),
		lastDiags: map[string][]Diagnostic{},
	}
}

// docState is everything we cache per open document. Both prog and
// info may be nil when the source has problems severe enough that
// parsing or type-checking bailed; consumers (hover, definition)
// must nil-check before walking.
type docState struct {
	src   string
	prog  *ast.Program
	info  *checker.Info
	diags []Diagnostic

	// lit is set for literate (`.fern.md`) documents: the top-level
	// prog/info stay nil (so features that aren't literate-aware stay
	// inert), while lit carries the tangled program and the line maps
	// the cursor-driven handlers use to query + remap positions.
	lit *literateDoc
}

// ExitCode returns the process exit code the server thinks the
// transport should use after the read loop ends. 0 means clean
// shutdown (`shutdown` then `exit`); 1 means `exit` without prior
// `shutdown` (per the spec).
func (s *Server) ExitCode() int { return s.exitCode }

// Serve reads framed LSP messages from r, dispatches them, and writes
// any response / notification frames to w. Returns when the client
// hangs up the input stream OR sends `exit`.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	s.publish = func(method string, params any) {
		raw, err := json.Marshal(params)
		if err != nil {
			return // can't publish what we can't serialise; drop.
		}
		m := message{Jsonrpc: "2.0", Method: method, Params: raw}
		_ = writeFrame(w, m)
	}
	for {
		frame, err := readFrame(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		s.dispatch(frame, w)
		if s.exited {
			return nil
		}
	}
}

// HandleMessage parses, dispatches, and serialises a single LSP
// message exchange. Used by transports that don't own their own
// read loop (e.g. the wasm playground that's driven by JS callbacks).
//
// reqBytes is one JSON-RPC message (no Content-Length framing). The
// returned bytes are the JSON-RPC response — empty for notifications,
// since notifications have no response. Server-to-client notifications
// (publishDiagnostics) are delivered via the publish function the
// caller passes to SetPublisher.
func (s *Server) HandleMessage(reqBytes []byte) []byte {
	var msg message
	if err := json.Unmarshal(reqBytes, &msg); err != nil {
		return marshalResponse(nil, nil, &rpcError{
			Code:    errCodeParseError,
			Message: "invalid JSON: " + err.Error(),
		})
	}
	if msg.ID == nil {
		// Notification: no response, no error path back to the
		// client either. handleMethod still runs for side effects.
		_, _ = s.handleMethod(msg.Method, msg.Params)
		return nil
	}
	result, rerr := s.handleMethod(msg.Method, msg.Params)
	return marshalResponse(msg.ID, result, rerr)
}

// SetPublisher installs the callback the server uses to send
// notifications (publishDiagnostics, etc.) back to the client.
// Required when HandleMessage is the only entry point; Serve sets
// its own.
func (s *Server) SetPublisher(publish func(method string, params any)) {
	s.publish = publish
}

func (s *Server) dispatch(frame []byte, w io.Writer) {
	var msg message
	if err := json.Unmarshal(frame, &msg); err != nil {
		// Bad JSON with no ID: nothing to reply to.
		return
	}
	if msg.ID == nil {
		_, _ = s.handleMethod(msg.Method, msg.Params)
		return
	}
	result, rerr := s.handleMethod(msg.Method, msg.Params)
	_ = writeFrame(w, message{
		Jsonrpc: "2.0",
		ID:      msg.ID,
		Result:  rawOrNull(result),
		Error:   rerr,
	})
}

func (s *Server) handleMethod(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.handleInitialize(), nil
	case "initialized":
		return nil, nil
	case "shutdown":
		s.shutdownRequested = true
		return nil, nil
	case "exit":
		if s.shutdownRequested {
			s.exitCode = 0
		} else {
			s.exitCode = 1
		}
		s.exited = true
		return nil, nil
	case "textDocument/didOpen":
		return nil, s.handleDidOpen(params)
	case "textDocument/didChange":
		return nil, s.handleDidChange(params)
	case "textDocument/didClose":
		return nil, s.handleDidClose(params)
	case "textDocument/hover":
		return s.handleHover(params)
	case "textDocument/definition":
		return s.handleDefinition(params)
	case "textDocument/completion":
		return s.handleCompletion(params)
	case "textDocument/signatureHelp":
		return s.handleSignatureHelp(params)
	case "textDocument/inlayHint":
		return s.handleInlayHint(params)
	case "textDocument/documentSymbol":
		return s.handleDocumentSymbol(params)
	case "textDocument/semanticTokens/full":
		return s.handleSemanticTokens(params)
	case "textDocument/references":
		return s.handleReferences(params)
	case "textDocument/rename":
		return s.handleRename(params)
	case "textDocument/codeAction":
		return s.handleCodeAction(params)
	case "textDocument/formatting":
		return s.handleFormatting(params)
	}
	return nil, &rpcError{
		Code:    errCodeMethodNotFound,
		Message: "unknown method: " + method,
	}
}

func (s *Server) handleInitialize() initializeResult {
	return initializeResult{
		Capabilities: serverCapabilities{
			TextDocumentSync:   syncKindFull,
			HoverProvider:      true,
			DefinitionProvider: true,
			CompletionProvider: &completionOptions{
				TriggerCharacters: []string{"."},
			},
			SignatureHelpProvider: &signatureHelpOptions{
				TriggerCharacters:   []string{"(", ","},
				RetriggerCharacters: []string{","},
			},
			InlayHintProvider:      true,
			DocumentSymbolProvider: true,
			SemanticTokensProvider: &semanticTokensOptions{
				Legend: semanticTokensLegend{
					TokenTypes:     semanticTokenTypeNames(),
					TokenModifiers: []string{},
				},
				Full: true,
			},
			ReferencesProvider:         true,
			RenameProvider:             true,
			DocumentFormattingProvider: true,
			CodeActionProvider:         true,
		},
		ServerInfo: &serverInfo{Name: "lang-lsp"},
	}
}

func (s *Server) handleHover(raw json.RawMessage) (any, *rpcError) {
	var p hoverParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	if state.lit != nil {
		tpos, ok := state.lit.toTangled(p.Position)
		if !ok {
			return nil, nil
		}
		r := runHover(state.lit.tangled, tpos)
		if r == nil {
			return nil, nil
		}
		if r.Range != nil {
			dr := state.lit.toDocRange(*r.Range)
			r.Range = &dr
		}
		return r, nil
	}
	if r := runHover(state, p.Position); r != nil {
		return r, nil
	}
	return nil, nil // null hover = "nothing to show here"
}

func (s *Server) handleDefinition(raw json.RawMessage) (any, *rpcError) {
	var p definitionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	if state.lit != nil {
		tpos, ok := state.lit.toTangled(p.Position)
		if !ok {
			return nil, nil
		}
		loc := runDefinition(state.lit.tangled, p.TextDocument.URI, tpos)
		if loc == nil {
			return nil, nil
		}
		// In-document definitions carry the .fern.md URI; remap their
		// range back onto the document. (Single-file parse never yields
		// cross-module locations here.)
		if loc.URI == p.TextDocument.URI {
			loc.Range = state.lit.toDocRange(loc.Range)
		}
		return loc, nil
	}
	if loc := runDefinition(state, p.TextDocument.URI, p.Position); loc != nil {
		return loc, nil
	}
	return nil, nil
}

func (s *Server) handleCompletion(raw json.RawMessage) (any, *rpcError) {
	var p completionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return &completionList{Items: []completionItem{}}, nil
	}
	if state.lit != nil {
		tpos, ok := state.lit.toTangled(p.Position)
		if !ok {
			return &completionList{Items: []completionItem{}}, nil
		}
		// Completion items are plain insertions (no doc-positioned text
		// edits), so only the query position needs translating.
		return runCompletion(state.lit.tangled, tpos), nil
	}
	return runCompletion(state, p.Position), nil
}

func (s *Server) handleInlayHint(raw json.RawMessage) (any, *rpcError) {
	var p inlayHintParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return []inlayHint{}, nil
	}
	return runInlayHints(state, p.Range), nil
}

func (s *Server) handleDocumentSymbol(raw json.RawMessage) (any, *rpcError) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return []documentSymbol{}, nil
	}
	return runDocumentSymbols(state, p.TextDocument.URI), nil
}

func (s *Server) handleSemanticTokens(raw json.RawMessage) (any, *rpcError) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return semanticTokensResponse{Data: []int{}}, nil
	}
	return runSemanticTokens(state), nil
}

func (s *Server) handleReferences(raw json.RawMessage) (any, *rpcError) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position Position `json:"position"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return []Location{}, nil
	}
	if state.lit != nil {
		tpos, ok := state.lit.toTangled(p.Position)
		if !ok {
			return []Location{}, nil
		}
		locs := runReferences(state.lit.tangled, p.TextDocument.URI, tpos)
		for i := range locs {
			if locs[i].URI == p.TextDocument.URI {
				locs[i].Range = state.lit.toDocRange(locs[i].Range)
			}
		}
		return locs, nil
	}
	return runReferences(state, p.TextDocument.URI, p.Position), nil
}

func (s *Server) handleFormatting(raw json.RawMessage) (any, *rpcError) {
	var p formattingParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return []textEdit{}, nil
	}
	if edits := runFormatting(state); edits != nil {
		return edits, nil
	}
	return []textEdit{}, nil
}

func (s *Server) handleRename(raw json.RawMessage) (any, *rpcError) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position Position `json:"position"`
		NewName  string   `json:"newName"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	edit := runRename(state, p.TextDocument.URI, p.Position, p.NewName)
	if edit == nil {
		return nil, nil
	}
	return edit, nil
}

func (s *Server) handleCodeAction(raw json.RawMessage) (any, *rpcError) {
	var p codeActionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	return runCodeAction(p.TextDocument.URI, p), nil
}

func (s *Server) handleSignatureHelp(raw json.RawMessage) (any, *rpcError) {
	var p signatureHelpParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	state, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	if state.lit != nil {
		tpos, ok := state.lit.toTangled(p.Position)
		if !ok {
			return nil, nil
		}
		if sh := runSignatureHelp(state.lit.tangled, tpos); sh != nil {
			return sh, nil
		}
		return nil, nil
	}
	if sh := runSignatureHelp(state, p.Position); sh != nil {
		return sh, nil
	}
	return nil, nil
}

func (s *Server) handleDidOpen(raw json.RawMessage) *rpcError {
	var p didOpenParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	affected := s.updateDoc(p.TextDocument.URI, p.TextDocument.Text)
	s.publishDiagnostics(p.TextDocument.URI)
	for _, u := range affected {
		s.publishDiagnostics(u)
	}
	return nil
}

func (s *Server) handleDidChange(raw json.RawMessage) *rpcError {
	var p didChangeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	if len(p.ContentChanges) == 0 {
		return nil
	}
	// Full-sync mode: the last entry holds the entire new document.
	// We ignore Range; editors that respect our declared sync kind
	// never send one.
	affected := s.updateDoc(p.TextDocument.URI, p.ContentChanges[len(p.ContentChanges)-1].Text)
	s.publishDiagnostics(p.TextDocument.URI)
	for _, u := range affected {
		s.publishDiagnostics(u)
	}
	return nil
}

// updateDoc re-parses + re-checks src and caches the products. Called
// from didOpen and didChange. parseFor is a package-local indirection
// so test code can stub the pipeline.
//
// Three fast paths avoid the parse + check cost when possible:
//
//  1. The URI's existing docState already has this exact source —
//     happens when an editor sends a redundant didChange (some do on
//     focus events). No work, no publish-side churn.
//  2. (single-file mode) The shared compile cache has a prior entry
//     for this source — happens on undo / redo and on opening a
//     snippet you've seen before. Result is reused; URI's docState
//     is rebuilt against it. The cache is bypassed in workspace
//     mode because the result depends on the rest of the import
//     closure too, not just this file's text.
//  3. New source: full pipeline, then (single-file only) caches
//     updated.
//
// updateDoc returns the list of OTHER open URIs whose cached
// diagnostics changed as a side-effect of this update (workspace
// mode only — a load from main.fern can update util.fern's diags
// if util.fern is also open). The caller is responsible for
// publishing diagnostics to each URI in the returned list.
func (s *Server) updateDoc(uri, src string) []string {
	if prev, ok := s.docs[uri]; ok && prev.src == src {
		return nil
	}
	// Literate documents (`.fern.md`) are tangled before compilation and
	// their diagnostics remapped onto the document — handled ahead of the
	// workspace / single-file paths, which expect plain Fern source.
	if isLiterateURI(uri) {
		return s.updateLiterateDoc(uri, src)
	}
	// Workspace mode: thread through modload so cross-module imports
	// load + type-check together. file:// URIs without a real path
	// (the playground's opaque ones) fall back to single-file.
	if s.workspace {
		if entryPath, ok := uriToPath(uri); ok {
			// Stash the new src in s.docs so loadWorkspace's
			// override-snapshot sees the latest content for this
			// URI. We rebuild state into the same slot below.
			s.docs[uri] = &docState{src: src}
			prog, info, diagsByFile := s.loadWorkspace(entryPath)
			s.docs[uri] = &docState{
				src:   src,
				prog:  prog,
				info:  info,
				diags: diagsByFile[entryPath],
			}
			// Propagate diagnostics for any OTHER open URI that
			// the load touched (sibling modules in the import
			// closure that the editor also has open). Each one's
			// cached diags become the new per-file slice; the
			// caller publishes them.
			var affected []string
			for otherURI, doc := range s.docs {
				if otherURI == uri {
					continue
				}
				otherPath, ok := uriToPath(otherURI)
				if !ok {
					continue
				}
				newDiags := diagsByFile[otherPath]
				if newDiags == nil {
					newDiags = []Diagnostic{}
				}
				if !diagnosticsEqual(doc.diags, newDiags) {
					doc.diags = newDiags
					affected = append(affected, otherURI)
				}
			}
			return affected
		}
	}
	if hit := s.cache.get(src); hit != nil {
		s.docs[uri] = &docState{
			src:   hit.src,
			prog:  hit.prog,
			info:  hit.info,
			diags: hit.diags,
		}
		return nil
	}
	state := &docState{src: src}
	prog, perr := parseFor(src)
	state.prog = prog
	var checkErr error
	if prog != nil {
		state.info, checkErr = checker.Check(prog)
	}
	state.diags = collectDiagnostics(perr, checkErr)
	s.docs[uri] = state
	s.cache.put(src, state.prog, state.info, state.diags)
	return nil
}

func collectDiagnostics(parseErr, checkErr error) []Diagnostic {
	out := []Diagnostic{}
	if parseErr != nil {
		out = append(out, toDiagnostics(parseErr)...)
	}
	if checkErr != nil {
		out = append(out, toDiagnostics(checkErr)...)
	}
	return out
}

func (s *Server) handleDidClose(raw json.RawMessage) *rpcError {
	var p didCloseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	delete(s.docs, p.TextDocument.URI)
	// Clear diagnostics for the closed file so editors don't keep
	// stale squiggles in their Problems panel. Also reset the
	// last-published slot so a later didOpen with the same source
	// republishes from scratch.
	delete(s.lastDiags, p.TextDocument.URI)
	s.publish("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         p.TextDocument.URI,
		Diagnostics: []Diagnostic{},
	})
	return nil
}

func (s *Server) publishDiagnostics(uri string) {
	state, ok := s.docs[uri]
	if !ok {
		return
	}
	if s.publish == nil {
		return
	}
	// Skip the wire send when the diagnostic list hasn't changed
	// since the last publish for this URI. Editors filter dedup
	// themselves but cutting it at the source saves the JSON
	// marshal + the client's debounce cycle. Particularly relevant
	// when the cache fast-paths take effect — same source ⇒ same
	// diagnostics, no point re-announcing.
	//
	// Important: only dedup once we've actually published for this
	// URI before. The two-value map lookup distinguishes "never
	// published" (hasPrior == false) from "published an empty
	// list" (hasPrior == true, prior len 0). Without that guard,
	// the very first publish on a clean document gets swallowed
	// because the missing-key zero-value (nil slice) compares
	// equal to an empty diagnostic list — and the playground
	// never renders the "no problems found" state.
	if prior, hasPrior := s.lastDiags[uri]; hasPrior && diagnosticsEqual(prior, state.diags) {
		return
	}
	s.lastDiags[uri] = state.diags
	s.publish("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Diagnostics: state.diags,
	})
}

// diagnosticsEqual compares two diagnostic slices by value. Order-
// sensitive; that matches our publish path (we always emit in the
// same order the parser + checker produced).
func diagnosticsEqual(a, b []Diagnostic) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// parseFor is a package-local indirection (variable, not func, so
// tests can wrap it with call counters) over the parse stage. It's
// also where stdlib + module loading would hook in; for the MVP the
// playground runs single-file programs and `parser.Parse` is enough.
var parseFor = func(src string) (*ast.Program, error) {
	return parser.Parse(src)
}

// ---- framing ----

func readFrame(r *bufio.Reader) ([]byte, error) {
	contentLen := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if i := strings.IndexByte(line, ':'); i >= 0 {
			key := strings.TrimSpace(line[:i])
			val := strings.TrimSpace(line[i+1:])
			if strings.EqualFold(key, "Content-Length") {
				n, err := strconv.Atoi(val)
				if err != nil {
					return nil, fmt.Errorf("bad Content-Length: %w", err)
				}
				contentLen = n
			}
		}
	}
	if contentLen < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	buf := make([]byte, contentLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeFrame(w io.Writer, m message) error {
	if m.Jsonrpc == "" {
		m.Jsonrpc = "2.0"
	}
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := w.Write([]byte(header)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func marshalResponse(id json.RawMessage, result any, rerr *rpcError) []byte {
	m := message{
		Jsonrpc: "2.0",
		ID:      id,
		Result:  rawOrNull(result),
		Error:   rerr,
	}
	b, err := json.Marshal(m)
	if err != nil {
		// Last-resort error response with empty result.
		b, _ = json.Marshal(message{
			Jsonrpc: "2.0",
			ID:      id,
			Error: &rpcError{
				Code:    errCodeInternalError,
				Message: "failed to marshal response: " + err.Error(),
			},
		})
	}
	return b
}

// rawOrNull turns a Go value into a json.RawMessage, returning nil
// if the value is nil (so the JSON serialiser omits the field
// entirely thanks to omitempty). The two-step Marshal + cast is
// necessary because json.RawMessage's zero value isn't nil — it's
// a length-0 byte slice that omitempty still keeps out.
func rawOrNull(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

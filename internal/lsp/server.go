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
	// opened. Keyed by URI; value is the most recent full text we've
	// been told about. Removed on didClose.
	docs map[string]string

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
}

// NewServer returns a fresh Server with no open documents. Use Serve
// (for stdio) or call HandleMessage directly (for in-process drivers
// like the wasm playground).
func NewServer() *Server {
	return &Server{docs: map[string]string{}}
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
	}
	return nil, &rpcError{
		Code:    errCodeMethodNotFound,
		Message: "unknown method: " + method,
	}
}

func (s *Server) handleInitialize() initializeResult {
	return initializeResult{
		Capabilities: serverCapabilities{
			TextDocumentSync: syncKindFull,
		},
		ServerInfo: &serverInfo{Name: "lang-lsp"},
	}
}

func (s *Server) handleDidOpen(raw json.RawMessage) *rpcError {
	var p didOpenParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	s.docs[p.TextDocument.URI] = p.TextDocument.Text
	s.publishDiagnostics(p.TextDocument.URI)
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
	s.docs[p.TextDocument.URI] = p.ContentChanges[len(p.ContentChanges)-1].Text
	s.publishDiagnostics(p.TextDocument.URI)
	return nil
}

func (s *Server) handleDidClose(raw json.RawMessage) *rpcError {
	var p didCloseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return &rpcError{Code: errCodeInvalidRequest, Message: err.Error()}
	}
	delete(s.docs, p.TextDocument.URI)
	// Clear diagnostics for the closed file so editors don't keep
	// stale squiggles in their Problems panel.
	s.publish("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         p.TextDocument.URI,
		Diagnostics: []Diagnostic{},
	})
	return nil
}

func (s *Server) publishDiagnostics(uri string) {
	src, ok := s.docs[uri]
	if !ok {
		return
	}
	if s.publish == nil {
		return
	}
	s.publish("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Diagnostics: runDiagnostics(src),
	})
}

// parseFor + checkFor are package-local thunks so future test code can
// swap them with stubs. They're also where stdlib + module loading
// would hook in; for the MVP the playground runs single-file programs
// and `parser.Parse` / `checker.Check` are enough.
func parseFor(src string) (*ast.Program, error) {
	return parser.Parse(src)
}

func checkFor(prog *ast.Program) error {
	_, err := checker.Check(prog)
	return err
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

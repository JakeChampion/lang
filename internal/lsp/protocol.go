// Package lsp implements a minimal Language Server Protocol server
// for lang. It speaks LSP over an arbitrary byte stream so the same
// implementation drives both `cmd/fern-lsp` (stdio for editors) and
// the wasm playground (request/response over a JS-side adapter).
//
// The MVP covers initialize / shutdown / exit, full-sync didOpen /
// didChange / didClose, and publishes diagnostics on every change.
// Later phases add hover, definition, completion, signatureHelp.
package lsp

import "encoding/json"

// JSON-RPC 2.0 message shape. We hand-roll the small subset of LSP we
// need rather than pull in a protocol library — the wire format is
// well-defined and keeping it in-tree means the wasm build stays slim.
type message struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`     // request / response
	Method  string          `json:"method,omitempty"` // request / notification
	Params  json.RawMessage `json:"params,omitempty"` // request / notification
	Result  json.RawMessage `json:"result,omitempty"` // response
	Error   *rpcError       `json:"error,omitempty"`  // response
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC error codes (a subset).
const (
	errCodeParseError     = -32700
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInternalError  = -32603
)

// ---- LSP-specific types (only what the MVP exchanges) ----

// Position is 0-based line + 0-based UTF-16 character offset per the
// LSP spec. lang's internal positions are 1-based UTF-8 byte columns;
// the conversion lives in toLSPPosition (and is exact for ASCII —
// good enough for an MVP since lang source is mostly ASCII).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"` // 1 = Error, 2 = Warning, 3 = Info, 4 = Hint
	Source   string `json:"source,omitempty"`
	Code     string `json:"code,omitempty"` // stable error code (e.g. "E001") — see internal/diag/explanations/
	Message  string `json:"message"`
	// Data carries a machine-applicable fix (fixData) when the
	// underlying error had a diag.Suggestion. Per the LSP spec the
	// client round-trips it verbatim into codeAction requests'
	// context.diagnostics, so textDocument/codeAction serves the
	// quickfix from the request alone — no server-side fix cache.
	Data any `json:"data,omitempty"`
}

const (
	severityError   = 1
	severityWarning = 2
	severityInfo    = 3
	severityHint    = 4
)

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// textDocumentSyncKind values. We declare Full so each didChange
// carries the entire new content — simpler than reconstructing from
// per-character deltas, and acceptable for the file sizes lang
// programs reach.
const (
	syncKindNone = 0
	syncKindFull = 1
)

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   *serverInfo        `json:"serverInfo,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type serverCapabilities struct {
	TextDocumentSync           int                    `json:"textDocumentSync"`
	HoverProvider              bool                   `json:"hoverProvider,omitempty"`
	DefinitionProvider         bool                   `json:"definitionProvider,omitempty"`
	CompletionProvider         *completionOptions     `json:"completionProvider,omitempty"`
	SignatureHelpProvider      *signatureHelpOptions  `json:"signatureHelpProvider,omitempty"`
	InlayHintProvider          bool                   `json:"inlayHintProvider,omitempty"`
	DocumentSymbolProvider     bool                   `json:"documentSymbolProvider,omitempty"`
	SemanticTokensProvider     *semanticTokensOptions `json:"semanticTokensProvider,omitempty"`
	ReferencesProvider         bool                   `json:"referencesProvider,omitempty"`
	RenameProvider             bool                   `json:"renameProvider,omitempty"`
	DocumentFormattingProvider bool                   `json:"documentFormattingProvider,omitempty"`
	CodeActionProvider         bool                   `json:"codeActionProvider,omitempty"`
}

// semanticTokensOptions advertises the legend the client uses to
// decode the int stream returned by textDocument/semanticTokens/full.
// The legend's index ordering MUST match the indices the server
// encodes in each token tuple — see semantic_tokens.go.
type semanticTokensOptions struct {
	Legend semanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full"`
}

type semanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type completionOptions struct {
	// TriggerCharacters tells the client to re-query completion
	// when one of these characters is typed. We don't strictly
	// need any (completion can always be triggered explicitly)
	// but `.` is a natural choice for field-access completion
	// once we support it.
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type signatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
	RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
}

// ---- Param types we receive ----

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []contentChange `json:"contentChanges"`
}

// contentChange covers both shapes the spec allows: with Range (a
// delta — which we don't currently apply, we still take Text as the
// whole content) and without (the whole-document form sync=Full
// produces). The MVP simply takes the last entry's Text as the new
// document state. Editors that respect our advertised Full sync
// always send a single entry with no Range.
type contentChange struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// EnableWorkspace switches the server into workspace mode: file://
// URIs are resolved through modload so cross-module imports load,
// type-check, and contribute to symbol-table lookups. Non-file URIs
// (file:///playground.fern from the wasm wrapper, opaque test URIs)
// still use the single-file pipeline.
//
// Workspace mode requires filesystem access. cmd/fern-lsp enables
// it by default; cmd/fern-wasm leaves it off because the browser
// can't read sibling files.
func (s *Server) EnableWorkspace() { s.workspace = true }

// uriToPath converts a file:// URI to an absolute filesystem path,
// or returns ("", false) when uri isn't a file URI or can't be
// decoded. We accept only `file://` scheme and reject anything
// whose path lacks an absolute prefix.
func uriToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	// On POSIX, u.Path is "/abs/path". On Windows, it's
	// "/C:/abs/path" and needs the leading slash stripped.
	p := u.Path
	if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	if !filepath.IsAbs(p) {
		return "", false
	}
	return p, true
}

// requestModule returns the canonical module path naming the document
// a request was made against — the same string modload stamps on the
// decls it loads from that file. Empty when the URI has no filesystem
// path (the playground's opaque URIs, single-file mode), which leaves
// the module filter inert.
func requestModule(uri string) string {
	p, ok := uriToPath(uri)
	if !ok {
		return ""
	}
	return p
}

// inModule reports whether a node stamped with sourceModule belongs to
// the document the request named. Every module of a workspace program
// shares one (line, col) space and ast.Position carries no filename, so
// a position search that skips this check answers a request for one
// file out of another. An unstamped node ("" — single-file programs,
// literate tangles, checker-synthesised decls) is in scope everywhere.
func inModule(sourceModule, requested string) bool {
	return requested == "" || sourceModule == "" || sourceModule == requested
}

// pathToURI is the inverse — used to attribute cross-module
// definitions back to a file:// URI the editor can open.
func pathToURI(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.ToSlash(abs)
	// On Windows, abs starts with `C:/...`; on POSIX with `/...`.
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	return "file://" + abs
}

// loadWorkspace loads the program rooted at entryPath using modload
// + the open-document override map so unsaved buffers take
// precedence over disk. Diagnostics are split by their source-file
// path (via the diag.Filed interface) so callers can route each
// entry to the right URI; errors with no File() stamp fall back to
// the entry path.
func (s *Server) loadWorkspace(entryPath string) (*ast.Program, *checker.Info, map[string][]Diagnostic) {
	// Snapshot the current open-doc map into the path-keyed shape
	// modload expects. Documents whose URI doesn't resolve to a
	// file path (the playground's opaque URIs) get skipped.
	overrides := map[string]string{}
	for uri, doc := range s.docs {
		if p, ok := uriToPath(uri); ok {
			overrides[p] = doc.src
		}
	}
	prog, _, perr := modload.LoadWith(entryPath, overrides)
	var info *checker.Info
	var checkErr error
	if prog != nil {
		info, checkErr = checker.Check(prog)
	}
	byFile := splitDiagnosticsByFile(perr, entryPath)
	for f, ds := range splitDiagnosticsByFile(checkErr, entryPath) {
		byFile[f] = append(byFile[f], ds...)
	}
	return prog, info, byFile
}

// splitDiagnosticsByFile walks err (diag.Errors or a single error)
// and groups the converted Diagnostics by the source-file path
// stamped on each entry. Entries without a File() stamp land under
// entryFallback so the entry URI still sees them — that's the
// pre-decl checker errors, plus anything the lexer / parser
// surfaced before modload got around to stamping (shouldn't happen
// in workspace mode but we'd rather attribute than drop).
func splitDiagnosticsByFile(err error, entryFallback string) map[string][]Diagnostic {
	out := map[string][]Diagnostic{}
	if err == nil {
		return out
	}
	add := func(e error) {
		path := entryFallback
		if f, ok := e.(diag.Filed); ok {
			if v := f.File(); v != "" {
				path = v
			}
		}
		out[path] = append(out[path], toDiagnostic(e))
	}
	if es, ok := err.(diag.Errors); ok {
		for _, e := range es {
			add(e)
		}
		return out
	}
	add(err)
	return out
}

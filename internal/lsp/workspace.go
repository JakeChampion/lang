package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

// EnableWorkspace switches the server into workspace mode: file://
// URIs are resolved through modload so cross-module imports load,
// type-check, and contribute to symbol-table lookups. Non-file URIs
// (file:///playground.lang from the wasm wrapper, opaque test URIs)
// still use the single-file pipeline.
//
// Workspace mode requires filesystem access. cmd/lang-lsp enables
// it by default; cmd/lang-wasm leaves it off because the browser
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

// loadWorkspace loads the program rooted at entryPath using
// modload + the open-document override map so unsaved buffers
// take precedence over disk. Returns the same shape as the
// single-file pipeline so updateDoc can treat both paths
// uniformly.
func (s *Server) loadWorkspace(entryPath string) (*ast.Program, *checker.Info, []Diagnostic, error) {
	// Snapshot the current open-doc map into the path-keyed shape
	// modload expects. Documents whose URI doesn't resolve to a
	// file path (the playground's opaque URIs) get skipped.
	overrides := map[string]string{}
	for uri, doc := range s.docs {
		if p, ok := uriToPath(uri); ok {
			overrides[p] = doc.src
		}
	}
	// Make sure entryPath's content is in the override map too —
	// the freshly-arrived didChange may not have settled into
	// s.docs yet when updateDoc is mid-flight.
	prog, _, perr := modload.LoadWith(entryPath, overrides)
	var info *checker.Info
	var checkErr error
	if prog != nil {
		info, checkErr = checker.Check(prog)
	}
	return prog, info, collectDiagnostics(perr, checkErr), nil
}

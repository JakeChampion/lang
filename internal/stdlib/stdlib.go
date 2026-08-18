// Package stdlib serves the source for std/ and core/ module imports
// out of an embedded directory tree. The compiler binary ships with
// these baked in via go:embed; modload routes any `import "std/…"`
// or `import "core/…"` through Resolve below instead of the filesystem
// resolver.
//
// Layout under this package:
//
//	internal/stdlib/std/...   — high-level idiomatic helpers users
//	                            import explicitly (string, array,
//	                            i32, http, json, …).
//	internal/stdlib/core/...  — low-level primitives stdlib modules
//	                            are built on top of (mem, io, runtime
//	                            dispatch helpers).
//
// Resolve treats the import path as a filesystem-style path rooted
// at this package's `embed` (so `std/i32` maps to
// `internal/stdlib/std/i32.fern`). The `.fern` extension is appended
// automatically if missing, mirroring the disk resolver in modload.
//
// See docs/PRELUDE-TO-MODULES.md for the migration plan that drives
// what ends up in here.
package stdlib

import (
	"embed"
	"io/fs"
	"strings"
)

// `all:` is required so go:embed includes files / directories
// whose names start with `_` or `.` (default embed semantics skip
// them). The Phase 1 test fixtures live at `std/_test_empty.fern`
// and `core/_test_empty.fern`; dropping `all:` would silently
// exclude them.
//
//go:embed all:std all:core
var src embed.FS

// Resolve loads the source for a stdlib-namespaced import path. The
// returned string is the lang source text; the boolean reports
// whether the path matched a known stdlib module. Returns
// `("", false)` for any path that doesn't start with `std/` or
// `core/`, and also for paths under those prefixes that don't exist
// in the embedded tree (so callers can route the miss to a clear
// "unknown stdlib module" diagnostic rather than fall back to
// filesystem resolution).
func Resolve(importPath string) (string, bool) {
	if !IsStdlibPath(importPath) {
		return "", false
	}
	data, err := fs.ReadFile(src, withFernExt(importPath))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// IsStdlibPath reports whether importPath is namespaced under std/
// or core/. Lets the caller pre-classify without paying for an
// embedded-FS lookup — useful in modload's resolveImportPath where
// the classification gates whether to call filepath.Join.
func IsStdlibPath(importPath string) bool {
	return strings.HasPrefix(importPath, "std/") || strings.HasPrefix(importPath, "core/")
}

// ModuleKey is the canonical `stdlib://…` key a stdlib import resolves to —
// the identity modload records a module under, and therefore the shape of the
// keys in `ast.Program.DirectImports` / `ModuleImports`. Returns "" for a path
// that is not stdlib-namespaced.
//
// It exists so a caller holding an import's SOURCE spelling (`std/string`) can
// ask whether a module imported it without re-deriving the key. Re-deriving it
// is a silent failure: the lookup simply misses, every answer comes back
// "not imported", and a guard written that way looks correct while changing
// nothing.
func ModuleKey(importPath string) string {
	if !IsStdlibPath(importPath) {
		return ""
	}
	return "stdlib://" + withFernExt(importPath)
}

// withFernExt appends the source extension unless the path already carries it,
// so callers may write either form of a stdlib import.
func withFernExt(importPath string) string {
	if strings.HasSuffix(importPath, ".fern") {
		return importPath
	}
	return importPath + ".fern"
}

// FS returns the embedded filesystem so tooling can iterate the
// stdlib tree (cmd/ferndoc uses this to enumerate std/*.fern for
// reference-page generation). Read-only by virtue of embed.FS;
// callers that mutate the result would get a runtime panic.
func FS() fs.FS { return src }

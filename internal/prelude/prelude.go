// Package prelude embeds the lang-source standard library that's
// auto-injected into every program at checker time.
//
// See docs/LANGUAGE-DIRECTION.md "Stdlib implementation strategy"
// for the rationale: hand-written wat is faster on the runtime
// hot path (allocator, hash, str_eq) but is brittle and
// duplicates code per backend. Higher-level helpers expressed
// in lang itself benefit from IR-level optimisations, drop into
// any backend (wasm, arm64) the same way, and ~10x easier to
// maintain than the equivalent wat.
//
// The embedded `prelude.fern` source is parsed once during
// `checker.Check` and its top-level decls are prepended to the
// user's program before type-checking proceeds. The user can
// shadow / override any prelude function by declaring a
// same-named function locally — same scoping rules as the
// auto-injected enums (Option, Result, JsonValue, etc.).
package prelude

import _ "embed"

//go:embed prelude.fern
var Source string

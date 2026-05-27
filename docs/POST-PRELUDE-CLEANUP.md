# Post-prelude cleanup

Follow-up work surfaced while removing the auto-injected prelude
(`docs/PRELUDE-TO-MODULES.md`). Each item is independent; ordered by
value. Status is tracked in the checklist at the bottom.

## 1. Checker must reject method use whose impl module isn't imported

**Problem.** Built-in types whose methods are implemented in a `core/`
module type-check even when that module isn't imported, then fail at
codegen with `unknown callee map_new_impl` / `undefined reference to
__map_set_impl`. The checker is green but the backend can't link. This
bit the migration repeatedly (every Map-using program that hadn't yet
gained `import "core/map"`).

The offender today is `Map` (and `MapIter`): the type is built-in and
its methods (`set`/`get_or`/`has`/`delete`/`len`/`keys`/`values`/`iter`)
+ the `map_new` builtin lower to `core/map`'s runtime helpers, but the
checker treats the methods as universally visible and never checks that
`core/map` is in the import closure.

**Fix.** In the checker, when a program uses a Map operation — the
`map_new` builtin, a `Map { … }` literal, a `Map[K,V]` annotation that
gets constructed, or a Map receiver-method call — require `core/map` in
the loaded-import set (`prog.LoadedStdlibPaths`). Emit a clear error
(`Map operations require import "core/map"`) instead of letting it reach
codegen. Single-file checker callers (no modload, `LoadedStdlibPaths`
nil) are exempt — they have no import mechanism and are only used for
unit probes.

**Test.** Checker test: a `map_new`/`Map{}` program without
`import "core/map"` is a clean type error; with it, clean.

## 2. CI gate: every stdlib module type-checks standalone under no-prelude

**Problem.** `std/time`, `std/test`, and `std/fuzz` all had latent
missing imports (e.g. `std/time` used `.index_of` without importing
`std/string`) that only surfaced when a program imported them in
isolation post-flip — the prelude had always loaded the whole tree.

**Fix.** A Go test that, for each embedded `std/*` and `core/*` module,
builds a trivial program (`import "core/no_prelude"; import "<mod>";
function main(): i32 { return 0; }`) and runs it through modload +
checker. Fails if any module can't be imported standalone.

## 3. `std/test` ergonomics — ADDRESSED via import aliases

Post-flip the test DSL reads `test.assert_eq_i32(...)` /
`test.TestRunner` everywhere — verbose for a test framework. Rather
than a bespoke "bare-export module" mechanism, the fix is **import
aliases**: `import "std/test" as t;` lets callers write
`t.assert_eq_i32(...)` / `t.TestRunner`. A short prefix keeps
provenance explicit while cutting the verbosity, and it's a general
feature (any module, any qualifier collision — e.g.
`import "std/string" as s;`). Implemented: parser `as <ident>` syntax
+ `ast.Import.Alias`; modload already keys its import table off
`Import.LocalName`, so setting LocalName = alias is all the resolution
needs; the printer round-trips the `as` clause. Example files can adopt
`import "std/test" as t;` for the nicer shape (follow-up).

## 4. Parser accepts keyword-named module qualifiers

**Problem.** `std/string`'s free functions are unreachable under
no-prelude: the bare name is mangled away and `string.repeat_char`
won't parse because `string` is a type keyword. `repeat_char` had to be
dropped from a test. Any future `std/string` (or other keyword-named
module) free function is similarly stranded.

**Fix (chosen approach).** Teach the parser to accept a type-keyword
token as a module qualifier in *expression* position: when a primary
expression starts with a builtin type keyword (`string`, `i32`, `i64`,
`u32`, `u64`, `f32`, `f64`, `bool`) immediately followed by `.<ident>`,
parse it as a qualified reference (the same shape an ordinary
`ident.ident` qualifier produces), so modload's existing `mod.Foo`
rewrite handles it. Type positions are unaffected (a `.` never follows a
type there).

**Test.** Parser/e2e: `string.repeat_char(120, 4)` builds and runs;
restore the dropped `repeat_char` coverage in the arm64 bundle.

## 5. `PrintMainResult` self-contained (no stdlib `int_to_string`)

**Problem.** The wasm test harness's `PrintMainResult` wrapper formats
`main()`'s i32 return via `int_to_string`, forcing *every* program that
goes through it to import `core/int` (worked around by injecting the
import in the harness). A debug/observation feature shouldn't impose a
stdlib dependency.

**Fix.** Emit a self-contained i32→decimal-string routine inline in the
synthesised `_start`/`_lang_run` wrapper (loop dividing by 10 into a
small stack buffer, then `__fern_print`), independent of any Fern
function. Drop the harness-side `core/int` injection
(`withResultPrinter`, the fixture/multi-file injections) once it's
self-contained.

## 6. Hygiene

- **REPL through modload.** `internal/interp/repl.go` still uses bare
  `parser.Parse`, so REPL input can't `import` stdlib. Route it through
  `modload.LoadSource` like the other in-memory entry points.
- **Remove the dead `IsPrelude` field.** Nothing sets
  `ast.FuncDecl.IsPrelude` post-flip; `treeshake.go` still filters on
  it (a no-op). Delete the field + the filter.
- **modload type-rewriter test.** Pin the tuple / slice / generic-arg
  struct-name mangling fix (`(string, TestRunner)` →
  `(string, mod__TestRunner)`) with a direct modload test.

## Checklist

- [x] 1 — checker requires `core/map` import for Map construction
      (also fixed std/headers/http/url/test/json missing the import)
- [x] 2 — standalone-import gate for stdlib modules
- [x] 3 — std/test ergonomics — solved with import aliases (`as`)
- [x] 4 — parser keyword-qualified module references
- [~] 5 — self-contained `PrintMainResult` (deferred — see below)
- [x] 6 — hygiene: drop `IsPrelude`; modload compound-type test
      (REPL → LoadSource sub-item deferred — needs real
      import-accumulation design, not a one-line reroute)

### Why 5 is deferred

The harness already injects `core/int` transparently
(`withResultPrinter` + the fixture/multi-file injection), so no program
*author* ever sees the coupling — it's a contained test-harness detail,
not a user-facing wart. Making `_start` self-contained means
hand-writing the i32→decimal-string *and the string-value
construction ABI* (alloc + header layout that `__fern_print` expects)
inline in wasm bytecode; getting it subtly wrong breaks the entire wasm
e2e suite. The risk/value trade doesn't favour doing it in a cleanup
pass. Revisit as a focused change if the injection ever becomes a real
maintenance pain.

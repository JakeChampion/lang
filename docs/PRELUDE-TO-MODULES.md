# Prelude → modules migration

> **Status: complete.** All six phases have landed — the auto-injected
> prelude and the `internal/prelude` package were removed in #1561.
> Programs now declare their `std/` + `core/` imports explicitly. The
> rest of this document is kept as the historical migration record.

## Problem

`internal/prelude/prelude.fern` was auto-injected into every program at
checker time (`injectPrelude` in `internal/checker/checker.go`). It was
a ~6000-line grab-bag covering string / array / i32 methods, HTTP
parsers, JSON, sort, format, URL, log, TCP, allocators, and more.

The "magic prelude" approach has bitten us:

- **Discoverability is bad.** New users have no way to know what's
  available; the prelude is invisible from source. The only way to
  find a helper is grep the prelude file.
- **Coupling is opaque.** Any helper can collide with user names.
  A program that only needs `string.split` still pays the parse +
  type-check cost of HTTP, JSON, TCP.
- **Targets that don't support a helper still see it.** WASI doesn't
  have `tcp_serve` on every host, but the function is in scope
  anyway — only blowing up at codegen.
- **No namespacing.** Everything lives at the top level. `is_blank`,
  `is_empty`, `is_numeric` could come from any domain.

## Goal

Replace the magic prelude with two explicit module trees:

- `std/…` — high-level idiomatic helpers users write against
  (`std/string`, `std/array`, `std/i32`, `std/http`, `std/json`, …)
- `core/…` — low-level primitives stdlib modules are built on top
  of (`core/mem` for allocators, `core/io` for `__write_stdout`,
  `core/runtime` for the `__method_…` dispatch helpers we don't
  want users calling directly).

Programs declare what they need:

```Fern
import "std/string";
import "std/array";

function main(): i32 {
    var xs: string[] = "a,b,c".split(",");
    return len(xs);
}
```

Built-in **types** (`Option`, `Result`, `IoError`, `JsonValue`,
`Reader`, `Writer`, `Map`, `MapIter`, `HttpRequest`, `HttpResponse`,
`Url`) stay auto-available — they're language primitives, not
stdlib helpers. The user shouldn't need `import "core/option";`
to write `Option[i32]`. The synthetic-decl block in
`checker.go:Check()` keeps that responsibility.

## Constraints / non-goals

- **Don't break existing programs in this PR series.** The migration
  lands in phases. Phase 1–4 keep auto-injection on for backwards
  compat; phase 5 flips the switch.
- **Don't redesign the import syntax.** `import "path";` stays as
  is. `std/…` and `core/…` are resolved via a new prefix-aware
  resolver in modload, not a new language feature.
- **No package manager.** `std` / `core` are baked into the
  compiler binary via `//go:embed`. Third-party modules stay
  relative-path-only for now.

## Phased plan

### Phase 1 — modload `std/` + `core/` prefix resolution

Goal: `import "std/empty";` resolves to an embedded test module.
No behaviour change for existing programs.

Tasks:

1. New `internal/stdlib/` package:
   - `internal/stdlib/stdlib.go` exposes an embedded FS containing
     `std/*.fern` and `core/*.fern` (`//go:embed std core`).
   - Exported `Resolve(importPath string) ([]byte, ok bool)` —
     returns the source for `std/foo` / `core/bar` paths, or
     `(nil, false)` for paths that don't match the embedded set.
2. Wire into `internal/modload/modload.go`:
   - In `resolveImportPath`, detect a `std/` or `core/` prefix
     and route through `stdlib.Resolve` instead of `filepath.Join`.
   - Loader needs to accept embedded source bytes (not just file
     paths). Refactor `Load` so the per-module read step can come
     from either disk or the embedded FS.
3. Test fixture: `internal/stdlib/std/_test_empty.fern` with one
   trivial function; `modload_test.go` adds a case proving the
   `std/_test_empty` resolve path works end-to-end.

### Phase 2 — Auto-discover `Array.X` registrations

Goal: stop hardcoding `c.info.Methods["Array.X"] = "__method_Array_X"`
in `checker.go`. Discover the mapping from the function-name
convention so modules can ship Array methods without checker
edits.

Tasks:

1. After parser/modload but before method-dispatch resolution,
   scan `prog.Funcs` for names matching `__method_Array_<name>`.
   For each, register `Array.<name>` → that function.
2. Delete the explicit `c.info.Methods["Array.X"] = …` lines in
   `checker.go`. Verify the test suite still passes (the
   registrations were idempotent; auto-discovery should produce
   identical results).

### Phase 3 — Carve out `std/i32`

Goal: prove the migration shape works on the smallest receiver
group. Existing programs keep working because the prelude
re-exports.

Tasks:

1. Create `internal/stdlib/std/i32.fern`. Move every
   `function (n: i32) X(): Y { ... }` from `prelude.fern` into
   this file. Roughly 40-50 helpers (abs, clamp, gcd, lcm,
   is_prime, factorial, etc.).
2. In `prelude.fern`, add `import "std/i32";` at the top.
   Prelude re-export keeps existing code (`var k: i32 = (5).abs();`)
   working unchanged.
3. Run the full e2e suite. Any failures mean cross-module
   method resolution is broken — fix in this PR before
   moving on.
4. Add `internal/e2e/std_i32_test.go` covering a program that
   `import "std/i32";` directly and calls `.abs()` etc. (Skips
   the auto-prelude path; proves the module works standalone.)

### Phase 4 — Carve out remaining modules

Repeat the Phase 3 pattern, one PR per module:

- `std/string` — receiver methods on string (largest group, ~120
  funcs). Split into sub-modules if it gets unwieldy:
  `std/string/case` (capitalize / title_case / to_upper /
  to_lower), `std/string/parse` (parse_int / parse_bool /
  parse_int_radix), `std/string/format` (pad_start / pad_end /
  truncate / ellipsis). Decide on first split-or-not at PR time.
- `std/array` — Array.X methods + free `sort_*` functions.
- `std/option` — Option helper functions (unwrap_or, map, etc.).
  Type stays in checker.
- `std/result` — same for Result.
- `std/http` — request / response builders, header parsing,
  serialization.
- `std/json` — encode / parse.
- `std/url` — url_parse, query_parse, url_encode / decode.
- `std/format` — `format(template, args)`, `format_bytes`.
- `std/math` — pow, sqrt_floor, etc. (overlap with std/i32;
  decide on boundary: math = generic, i32 = receiver methods).
- `std/log` — log_info / warn / error.
- `std/sort` — free sort_* functions if std/array doesn't
  swallow them.
- `core/mem` — `__alloc_u8`, `string_from_bytes`, `__memcpy`.
- `core/io` — `__write_stdout`, `read_chunk`, `read_all_stdin`.
- `core/net` — `tcp_serve` and the WASI socket plumbing.

After every PR: the prelude still works (it just re-exports
more modules), e2e suite still passes.

### Phase 5 — Remove auto-injection

Goal: programs declare imports explicitly. The prelude file
becomes empty (or is deleted entirely).

Tasks:

1. Convert every e2e test program from "implicit prelude" to
   "explicit imports". One PR per logical group of tests so
   review stays sane. Each test program prepends the imports
   it needs:
   ```Fern
   import "std/i32";
   import "std/array";
   ```
2. Update every example program in `examples/` similarly.
3. Delete `injectPrelude` from `checker.go`. Delete
   `internal/prelude/`. Keep the synthetic-type injection
   block (Option / Result / etc.) — those stay built-in.
4. Add a checker test confirming a program that doesn't
   import `std/string` gets a clean type error on
   `"x".split(",")` rather than silently working.

### Phase 6 — Documentation + onboarding

- `docs/STDLIB.md` lists every module + its public surface.
- `docs/LANGUAGE-DIRECTION.md`: replace the "Phase B prelude"
  section with the new module strategy.
- `CLAUDE.md`: drop the prelude.fern reference; point at the
  module tree.

## Open questions (decide as we land each phase)

1. **Where do `Map[K,V]` methods live?** Map is a built-in type
   but its `set/get_or/has/delete/iter/len/keys/values` methods
   are large. Probably `std/map` (built-in type, stdlib methods).
2. **`std/string` cardinality.** ~120 helpers in one module
   makes for slow parse and an overwhelming "what does this
   module export" surface. Split or not?
3. **`std::name` vs `std/name` for stdlib references.** Current
   import syntax is `import "path";` with `/`-separated paths.
   `::` would mean a syntax change. Defer to a later PR if
   we want it.
4. **What goes in `core/`?** Working rule: anything a normal
   user shouldn't reach for (allocators, raw memcpy, dispatch
   helpers). If `__alloc_u8` shows up in user code it's
   probably wrong.

## Tracking

- [x] Phase 1 — modload `std/` resolver
- [x] Phase 2 — auto-discover Array.X
- [x] Phase 3 — `std/i32` proof-of-shape (single method, then bulk).
      Receiver-method dispatch became module-scoped; the checker
      now consults each call site's import closure (see
      `MethodSources` in `internal/checker/checker.go`).
- [x] Phase 4 — remaining modules carved out. Final layout, per
      `docs/STDLIB.md`:
      - `std/` (20): `i32`, `i64`, `u32`, `u64`, `string`, `array`,
        `log`, `sort`, `csv`, `format`, `http`, `io`, `path`,
        `base64`, `hex`, `url`, `json`, `math`, `float`, `tcp`.
      - `core/` (3): `int`, `map`, `no_prelude`.
      - `internal/prelude/prelude.fern` is now a bare import
        block — every helper / receiver method / runtime
        function lives in a module.
- [x] Phase 5 — drop auto-injection. **Done** — `injectPrelude`
      and the `internal/prelude` package are gone (#1561);
      programs declare their `std/` + `core/` imports
      explicitly. Foundation that landed first:
      `import "core/no_prelude";` was the opt-out sentinel (#498, later retired). `modload.LoadStdlibFlat` /
      `LoadStdlibFlatSkipping` route the auto-prelude through
      modload's rewriter in flat-namespace mode — qualified
      imports inside stdlib bodies (`int.foo(...)`) rewrite
      to bare-named decls on the auto-prelude path AND to
      mangled `int__foo` decls on the no-prelude path
      depending on which copy of the target module survives.
      Stdlib-to-stdlib import cycles are now allowed
      (`std/i32` ↔ `std/string`); the back-edge's `imports`
      pointer is patched up in a second pass once both
      modules are in `loaded` (#510). The checker's
      `methodVisibleHere` treats any cross-stdlib method
      call as universally visible (#509), so stdlib bodies
      don't need to enumerate every method-source import
      under no-prelude — only the explicit `import`
      declarations matter for which modules `modload`
      loads. Every stdlib module's cross-module
      free-function calls are qualified (#505 → #508) and
      every stdlib module that dispatches methods from
      another stdlib module now declares the corresponding
      `import` (#511 → #513). modload exempts the Map
      runtime helpers (`map_new_impl`, `__map_*_impl`,
      `__mapiter_*_impl`) from prefix mangling so codegen's
      hardcoded rewrites resolve cleanly under both load
      paths (#520). End-to-end coverage on arm64 / x86-64 /
      wasm32 lands as the `Test*NoPreludeStdlibImports`
      suites (#514 / #515). Every `examples/*.fern` and
      `examples/wasm/*.fern` program migrated to declare
      explicit imports (#517 / #518 / #519 / #520 / #521 /
      #522). The internal/e2e test programs were then migrated
      the same way (add `import "core/no_prelude";` + one
      `import "std/X";` per module touched; free-function calls
      qualified to `module.fn`, bare receiver methods unchanged)
      across batches #1547 / #1549 / #1550 / #1551 / #1554 /
      #1555, and the switch was flipped in #1561. The flip also
      surfaced and fixed: a modload type-mangling gap (tuple /
      slice / generic-arg positions didn't get the `<mod>__`
      prefix); three stdlib modules missing dependency imports
      the prelude had masked (`std/time` → `std/string`,
      `std/test` → `std/float`, `std/fuzz` → `std/test` +
      qualified `math.random_int`); and the in-memory compile
      paths (stdin / REPL / playground / the wasm bundle) which
      now load through the new `modload.LoadSource`. A checker
      test pins that an unimported `.split` is a clean type
      error rather than silently resolving.
- [x] Phase 6 — docs (`docs/STDLIB.md`).

## `pub use` re-exports

With explicit imports, a library that wants to present a curated public
surface — or a project that wants a small facade/prelude module — can
**re-export** symbols from other modules with `pub use`:

```fern
// facade.fern — a curated public API assembled from focused modules
pub use "./helpers".{add5, clamp};
pub use "core/int".{int_to_string};
```

A consumer then imports the facade and uses the re-exported names through
the facade's qualifier; they resolve to the *original* module's
definition (no copy is made):

```fern
import "./facade";
function main(): i32 { return facade.add5(10); }   // → helpers__add5(10)
```

Semantics:

- Only **public** symbols can be re-exported (re-exporting a private name
  errors: `module "helpers" does not export "secret"`).
- A re-exported name becomes part of the re-exporting module's public
  surface, so it can be re-exported again (transitive chains resolve to
  the ultimate original).
- This is the intended fix for the stdlib's `__`-helper leak: a module can
  keep its internals out of its public qualifier and re-export only the
  curated names (pairs with a future `pub(package)` visibility level).

**Types and traits** re-export the same way — a re-exported `struct` /
`enum` / `trait` resolves through the facade in every type position
(annotation, struct literal, `match` on a re-exported enum, `impl`, and
`dyn facade.Trait`):

```fern
// facade.fern
pub use "./shapes".{Point, Shape, Area};
// consumer
import "./facade";
function dynArea(a: dyn facade.Area): i32 { return a.area(); }   // → shapes__Area
var p: facade.Point = facade.Point { x: 6, y: 7 };               // → shapes__Point
```

Implementation (`internal/modload/modload.go`): `pub use` targets are
loaded like imports; after mangle prefixes are assigned,
`resolveReexports` builds two per-module tables — `reexports` (values:
function / const) and `reexportTypes` (types: struct / enum / trait), each
name → original mangled name — and adds the names to the module's public
set. The rewriter resolves a consumer's `facade.name` to the original flat
name: the value path through `rewriteExpr`, the type path through
`rewriteStructNameAt` / `rewriteTraitNameAt` (the latter also covers
`dyn facade.Trait`). Parsed by `parsePubUse` into `ast.PubUse`.

**Self-host parity** (#3136 part 2): the self-hosted compiler resolves
`pub use` too. `examples/self_host/parser.fern` parses `pub use
"path".{names…};` into a re-export `Import` (`is_reexport` + the
`reexport_names`), so the on-disk module loaders (`modloader.fern`,
`fern.fern`) pull the target module into the graph like any import.
`examples/self_host/flatten.fern`'s `build_reexports` then walks every
imported module's `pub use` directives into a parallel-array re-export
table (`facade__name` → `origin__name`), threaded through `RewriteCtx`;
`lookup_reexport` redirects a consumer's `facade.name` at the same two
points the native rewriter does — qualified value refs in `rewrite_expr`
and qualified type refs in `rewrite_type_name` (chained re-exports are
followed to the end). Covered end-to-end by the self-host IR e2e gates
`TestSelfHostPubUseModloadX86_64` (x86-64) and
`TestWasmSelfHostPubUseReexport` (wasm), mirroring the native
`internal/e2e/pub_use_test.go`.

## How loading works now (post-Phase 5 summary)

There is no prelude injector anymore — a program sees only what it `import`s.

`modload` loads each imported `std/` / `core/` module (and its transitive
imports), mangles non-entry decls to `<mod>__name`, and rewrites qualified call
sites. `ast.Program.LoadedStdlibPaths` records what was loaded, so a module
pulled in twice (directly + transitively) dedupes rather than redeclaring its
methods.

That dedupe also closed an older bug: an explicit `import "std/foo";` of a
module that transitively imports another (e.g. `std/json` → `core/int`) sent
bare-name method dispatch (`(n).to_string()`) through the mangled
`int__int_to_string` name and crashed the interpreter with "cast from
interp.Array to i32 not supported". It is fixed and guarded by
`TestInterpScriptInteropIntToStringViaMangling`
(`internal/e2e/interp_script_test.go`), which exercises the explicit-import,
transitive-import, and qualified-call shapes. Extend it if you touch the
mangling / alias path.

In-memory source (stdin, REPL, the wasm playground bundle) loads through
`modload.LoadSource`, not bare `parser.Parse`, so those paths resolve stdlib
imports the same way the file-based driver does.

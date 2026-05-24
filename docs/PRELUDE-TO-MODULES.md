# Prelude → modules migration

## Problem

`internal/prelude/prelude.fern` is currently auto-injected into every
program at checker time (`injectPrelude` in `internal/checker/checker.go`).
It's a ~6000-line grab-bag covering string / array / i32 methods, HTTP
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

```lang
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
   ```lang
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
- [ ] Phase 5 — drop auto-injection. Foundation fully landed:
      `import "core/no_prelude";` is the opt-out sentinel
      (#498). `modload.LoadStdlibFlat` /
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
      #522). Remaining work:
      - Convert every internal/e2e test program to declare
        its imports explicitly. The shape mirrors the
        examples migration: add `import "core/no_prelude";`
        plus one `import "std/X";` line per stdlib module
        the test touches. Free-function calls become
        qualified (`int.int_to_string_radix(s, 16)`); bare
        receiver methods (`.abs()`, `.to_string()`) stay
        unchanged. ~389 tests in arm64_test.go /
        x86_64_test.go / wasm_e2e_test.go, each with its
        own source string — the migration is mechanical
        but bulky.
      - Once the suite passes with no-prelude as the default,
        flip the switch and remove `injectPrelude` + the
        `internal/prelude` package.
- [x] Phase 6 — docs (`docs/STDLIB.md`).

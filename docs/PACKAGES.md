# Packages: the `fern.toml` manifest (slice 1 — path dependencies)

The first implemented slice of the package-management design
(`PACKAGE-MANAGEMENT-SOTA.md` trade-off table; `MODULE-PACKAGES-
RESEARCH.md` Rec §1). Local `path` dependencies only: no network, no
registry, no lockfile yet — those layer on later per the research
docs. Everything is opt-in: a program with no `fern.toml` anywhere up
its directory tree loads exactly as before.

## Using it

```
app/
  fern.toml        [package] name = "app"
                   [dependencies] helper = { path = "../helper" }
  main.fern        import "helper";        → ../helper/lib.fern
                   import "helper/extra";  → ../helper/extra.fern
helper/
  fern.toml        [package] name = "helper"   (+ optional lib = "api.fern")
  lib.fern         pub function three(): i32 { … }
```

Qualified use follows the import name: `helper.three()`. A bare
`import "<dep>"` resolves to the dependency's lib module — `lib.fern`
unless its own manifest sets `[package] lib`. `import "<dep>/sub"`
resolves `sub.fern` inside the dependency directory. Dependencies are
packages: their own imports resolve against **their** manifest
(transitive path deps work), and `pub` visibility applies as usual.

## Manifest schema (this slice)

```toml
[package]
name = "app"          # required
version = "0.1.0"     # informational for now
lib = "lib.fern"      # entry module for `import "<name>"` (default)

[dependencies]
helper = { path = "../helper" }   # path is the only supported source
```

The parser (`internal/manifest`) is a strict TOML subset — sections,
quoted strings, inline `{ path = "…" }` tables — and rejects anything
else with a pointed error. `helper = "1.2"` (a registry/version dep)
errors today; that form is reserved for the MVS + lockfile slice.

## Resolution + isolation rules

Inside a manifest-governed package, an import resolves in this order:

1. `std/` / `core/` → embedded stdlib (unchanged).
2. `./x` / `../x` → importing-file-relative (unchanged).
3. First path segment matches a **declared dependency** → into that
   dependency's directory. The manifest is the authority: a declared
   dep wins over a same-named sibling file.
4. Otherwise, an existing file at the directory-relative path loads as
   before (so adding a `fern.toml` never breaks a loading program).
5. Nothing matches → error naming the governing `fern.toml` and the
   `[dependencies]` line to add.

Rule 5 is the resolver-side isolation invariant from the research: a
package can only reach dependencies it declares — enforced in
`resolveImport` (`internal/modload`), not by directory layout.

## Not yet (deliberately)

Version constraints + MVS resolution, `fern.lock` content hashes,
remote (hash-addressed) fetching, vendoring, workspaces, `fern add`.
See `PACKAGE-MANAGEMENT-SOTA.md` for the design each of these follows.
The self-hosted compiler's modloader (`examples/self_host/
modloader.fern`) does not read manifests yet — a port slice, tracked
with the rest in issue #4907.

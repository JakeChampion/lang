# Packages: the `fern.toml` manifest (slices 1–3 — path, hash-addressed, vendored)

The implemented slices of the package-management design
(`PACKAGE-MANAGEMENT-SOTA.md` trade-off table; `MODULE-PACKAGES-
RESEARCH.md` Rec §1): local `path` dependencies, hash-addressed `url`
dependencies fetched by an explicit `fern -fetch` into a
content-addressed store, and `fern -vendor` for fully-offline vendored
builds. No registry, no version resolution, no lockfile yet — those
layer on later per the research docs. Everything is opt-in: a program
with no `fern.toml` anywhere up its directory tree loads exactly as
before.

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
helper = { path = "../helper" }                    # local directory
webkit = { url = "https://example.com/webkit.tar.gz",
           hash = "sha256:<64 hex of the archive bytes>" }
```

The parser (`internal/manifest`) is a strict TOML subset — sections,
quoted strings, inline tables — and rejects anything else with a
pointed error. `helper = "1.2"` (a registry/version dep) errors today;
that form is reserved for the MVS + lockfile slice.

## Hash-addressed dependencies + `fern -fetch` (slice 2)

A `url` dependency is identified by its **hash**, not its URL — the
`sha256:` of the archive bytes; the URL is just a mirror hint (the
Zig/Roc model: trust-on-first-use is closed with zero infrastructure,
an expired domain can't substitute code, and nothing ever needs
re-checking). `fern -fetch [DIR]` is the ONLY command that touches the
network: it walks the governing manifest and its dependencies'
manifests transitively, downloads missing archives, verifies each
against its declared hash (a mismatch fails the run and nothing is
unpacked), and unpacks into the per-machine content-addressed store —
`$FERN_CACHE_DIR|<user-cache>/fern/pkgs/<hex>/`, shared by every
project that references the hash. Archives are `.tar.gz` of the
package directory (a single top-level directory is stripped); entries
that escape the root or aren't plain files/dirs are rejected.
`build`/`check`/`interp` read the store and error with a pointer at
`fern -fetch` when a url dependency hasn't been fetched — the compiler
never fetches (the no-build-time-network constraint).

## Vendoring — `fern -vendor` + offline builds (slice 3)

`fern -vendor [DIR]` flattens the whole transitive dependency graph
into `<root>/vendor/<name>/` — one directory per package, keyed by its
manifest `name`, following path dependencies to their directories and
url dependencies to the (already-fetched) content-addressed store.
After vendoring, builds are **fully offline**: the loader resolves
every declared dependency out of `vendor/`, ignoring the deps' original
path/url locations entirely, so a checked-in `vendor/` tree builds with
no network and no external directories. This is the shape
`BOOTSTRAP-RESEARCH.md`'s no-build-time-network constraint wants — the
package manager fetches; the compiler only ever reads `vendor/`.

Names must be unique across the graph (the flat namespace); a collision
between two distinct packages is a hard error. `vendor/` holds source
only (`fern.toml` + `.fern`/`.fern.md`); a nested `vendor/` or dot-dir
in a dependency is skipped. url deps must be `fern -fetch`ed before
vendoring (vendoring copies from the store, it doesn't download).

## Resolution + isolation rules

Inside a manifest-governed package, an import resolves in this order:

1. `std/` / `core/` → embedded stdlib (unchanged).
2. `./x` / `../x` → importing-file-relative (unchanged).
3. First segment matches a **declared dependency**, and a `vendor/`
   tree governs the package → `<vendor-root>/vendor/<name>/`. Vendored
   mode is offline and total: a declared dep missing from `vendor/`
   errors (re-run `fern -vendor`), never a fallback to path/url.
4. First segment matches a **declared dependency** (no vendor tree) →
   into the dependency's directory (path dep) or content-addressed
   store (url dep). The manifest is the authority: a declared dep wins
   over a same-named sibling file.
5. Otherwise, an existing file at the directory-relative path loads as
   before (so adding a `fern.toml` never breaks a loading program).
6. Nothing matches → error naming the governing `fern.toml` and the
   `[dependencies]` line to add.

Rules 4/6 are the resolver-side isolation invariant from the research:
a package can only reach dependencies it declares — enforced in
`resolveImport` (`internal/modload`), not by directory layout. Vendored
mode (rule 3) preserves it: only declared deps resolve from `vendor/`.

## Not yet (deliberately)

Version constraints + MVS resolution, `fern.lock` (for url deps the
manifest hash already pins content; the lockfile matters once version
deps resolve transitively), workspaces, `fern add`.
See `PACKAGE-MANAGEMENT-SOTA.md` for the design each of these follows.
The self-hosted compiler's modloader (`examples/self_host/
modloader.fern`) does not read manifests yet — a port slice, tracked
with the rest in issue #4907.

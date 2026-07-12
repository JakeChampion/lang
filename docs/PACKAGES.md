# Packages: the `fern.toml` manifest (slices 1–2 — path + hash-addressed deps)

The implemented slices of the package-management design
(`PACKAGE-MANAGEMENT-SOTA.md` trade-off table; `MODULE-PACKAGES-
RESEARCH.md` Rec §1): local `path` dependencies, and hash-addressed
`url` dependencies fetched by an explicit `fern -fetch` into a
content-addressed store. No registry, no version resolution, no
lockfile yet — those layer on later per the research docs. Everything
is opt-in: a program with no `fern.toml` anywhere up its directory
tree loads exactly as before.

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

Version constraints + MVS resolution, `fern.lock` (for url deps the
manifest hash already pins content; the lockfile matters once version
deps resolve transitively), vendoring, workspaces, `fern add`.
See `PACKAGE-MANAGEMENT-SOTA.md` for the design each of these follows.
The self-hosted compiler's modloader (`examples/self_host/
modloader.fern`) does not read manifests yet — a port slice, tracked
with the rest in issue #4907.

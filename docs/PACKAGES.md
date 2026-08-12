# Packages: the `fern.toml` manifest (slices 1–9)

The implemented slices of the package-management design
(`PACKAGE-MANAGEMENT-SOTA.md` trade-off table; `MODULE-PACKAGES-
RESEARCH.md` Rec §1): local `path` dependencies, hash-addressed `url`
dependencies fetched by an explicit `fern -fetch` into a
content-addressed store, `fern -vendor` for fully-offline vendored
builds, workspaces, `fern -add` to declare a dependency (auto-hashing
url sources), and Minimum Version Selection over a version index
(`fern -resolve` → `fern.lock`). No mandatory registry. Everything is
opt-in: a program with no `fern.toml` anywhere up its directory tree
loads exactly as before.

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
kv = { path = "../kv", capabilities = ["net"] }    # capability grant
```

The parser (`internal/manifest`) is a strict TOML subset — sections,
quoted strings, inline tables — and rejects anything else with a
pointed error. A bare `helper = "1.2.0"` is a versioned (MVS) dependency (see below);
`helper = "1.2"` errors (versions are MAJOR.MINOR.PATCH).

An optional `capabilities = ["net", …]` on any dependency form grants
the dependency the listed v1 capabilities (`net`, `fs`, `env`,
`subprocess`, `time`, `random` — an unknown name is a manifest error).
When the key is present, the checker enforces it by call-graph
reachability (E070); without the key, reaches print as warnings for
now. Grants attenuate: a governed dependency may grant its own
dependencies at most the capabilities it holds itself — an amplifying
grant is a load-time manifest error. See
docs/PACKAGE-CAPABILITIES-BRIEF.md.

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

`fern -vendor` on a **workspace root** vendors the *union* of every
member's external (path/url) dependencies into the root's `vendor/` —
in-tree `workspace = true` deps are skipped (members resolve those by
name). Members then resolve their external deps out of the shared root
`vendor/`, so the whole workspace builds offline from one vendored set.

## Workspaces (slice 4)

A **workspace** is a directory tree of related packages sharing one
root. A `fern.toml` with a `[workspace]` table lists member directories:

```toml
[workspace]
members = ["compiler/lexer", "compiler/parser", "handlers/app"]
```

The root may be workspace-only (no `[package]`) or both a package and a
workspace root. A member depends on another member by name with the
`workspace = true` form — no brittle `../../lexer` path:

```toml
# handlers/app/fern.toml
[package]
name = "app"
[dependencies]
lexer = { workspace = true }        # resolves to the member named "lexer"
```

`import "lexer"` in `app` resolves to the workspace member whose
**package name** is `lexer` (its directory name may differ). Isolation
is unchanged: a member reaches a sibling only if it *declares* the
`workspace = true` dependency — an undeclared `import "lexer"` is still
the usual undeclared-dependency error. This is the shape the eventual
self-hosted compiler wants (lexer / parser / checker / codegen as
sibling members).

`fern -check DIR` understands packages and workspaces: given a
workspace root it type-checks **every member** (each member's `lib`
module, or `main.fern` for an application member), printing an
`ok`/`FAIL` line per member and exiting non-zero if any fail — one
broken package doesn't hide the rest. Given a plain package directory
it checks that package's entry module; given a `.fern` file it is the
original single-entry check. This is the workspace-wide validation the
multi-package compiler wants: one command checks the whole tree.

## Adding a dependency — `fern -add` (slice 5)

`fern -add NAME SPEC [DIR]` appends a declared dependency to the
`fern.toml` governing `DIR` (default `.`), editing the file textually
so comments and formatting survive:

```
fern -add helper path:../helper                          # { path = "../helper" }
fern -add webkit url:https://example.com/webkit.tar.gz   # fetches, records the hash
fern -add lexer  workspace                               # { workspace = true }
```

The `url:` form is the ergonomic payoff: you never hand-compute a
`sha256`. `add` downloads the archive, records the hash it observed
(the Zig "write the url, the tool tells you the hash" flow), and leaves
it verified in the content-addressed store — so the immediately
following build works offline and every later `fern -fetch` verifies
against that recorded hash. Adding an already-declared dependency is an
error (edit it by hand to change a source), and the edited manifest is
re-parsed before it's written so a bad edit can never leave a broken
`fern.toml` on disk.

## Resolution + isolation rules

Inside a manifest-governed package, an import resolves in this order:

1. `std/` / `core/` → embedded stdlib (unchanged).
2. `./x` / `../x` → importing-file-relative (unchanged).
3. First segment matches a **`workspace = true` dependency** → the
   workspace member with that package name.
4. First segment matches a **declared dependency**, and a `vendor/`
   tree governs the package → `<vendor-root>/vendor/<name>/`. Vendored
   mode is offline and total: a declared dep missing from `vendor/`
   errors (re-run `fern -vendor`), never a fallback to path/url.
5. First segment matches a **versioned (MVS) dependency** → the version
   pinned in `fern.lock` (a local dir, or a url version in the store).
6. First segment matches a **declared dependency** (no vendor tree) →
   into the dependency's directory (path dep) or content-addressed
   store (url dep). The manifest is the authority: a declared dep wins
   over a same-named sibling file.
7. Otherwise, an existing file at the directory-relative path loads as
   before (so adding a `fern.toml` never breaks a loading program).
8. Nothing matches → error naming the governing `fern.toml` and the
   `[dependencies]` line to add.

Rules 3/5/6/8 are the resolver-side isolation invariant from the
research: a package can only reach dependencies it declares — enforced
in `resolveImport` (`internal/modload`), not by directory layout.
Vendored mode (rule 4) and workspace mode (rule 3) both preserve it:
only declared deps resolve.

## Versioned dependencies — MVS + `fern.lock` (slice 8)

The research's headline resolution recommendation is **Minimum Version
Selection** (Cox/Go): a dependency declares its *lowest* acceptable
version, and resolution keeps, per package, the *maximum of the
minimums* across the whole transitive graph. The constraint language is
minimum-only (no ranges), so a solution always exists, is the unique
lattice minimum, and is computed by deterministic graph reachability —
no SAT solver, no conflict-explanation UX debt.

```toml
[package]
name = "app"
index = "index.toml"    # the version source

[dependencies]
foo = "1.1.0"           # min version; MVS picks the exact one
bar = { version = "1.0.0" }
```

The **version source** is a plain **index file** (no registry service —
consistent with the shipped hash-addressing), mapping each package's
available versions to a source:

```toml
[foo]
"1.0.0" = { path = "foo-1.0.0" }                       # local monorepo
"1.1.0" = { url = "https://…/foo-1.1.0.tar.gz", hash = "sha256:…" }
[bar]
"1.0.0" = { path = "bar-1.0.0" }
"2.0.0" = { path = "bar-2.0.0" }
```

`fern -resolve [DIR]` runs MVS over the index (expanding each selected
version's own versioned deps to a fixpoint), fetches + verifies any
url-sourced versions into the content-addressed store, and writes the
chosen `(name, version, source)` set to **`fern.lock`**. A demanded
version absent from the index is a precise error, never a silent
round-up. Example: root wants `foo >= 1.1.0` and `bar >= 1.0.0`, but
`foo@1.1.0` requires `bar >= 2.0.0`, so the lock pins `bar = 2.0.0`.

MVS's minimum-only constraint language cannot express "not 1.9, it's
broken" — deliberately (an upper bound documents a bug instead of fixing
it). The escape hatch is Go's: a top-level **`[exclude]`** table banning
specific versions, applied **only from the manifest `fern -resolve` runs
on** (a dependency's `[exclude]` is ignored, preserving determinism):

```toml
[exclude]
bar = ["1.9.0", "1.9.1"]   # a demand for 1.9.0/1.9.1 rounds up to the
                           # next non-excluded indexed version
```

Excluding every version at or above a demand is a precise error;
excluding a version nothing demands is a no-op.

The build reads only `fern.lock` — never the index or the network (the
no-build-time-network constraint). A versioned dep whose lock is missing
errors pointing at `fern -resolve`; a locked url version absent from the
store errors pointing at `fern -fetch` (which also populates url
versions from a committed lock, for fresh-machine offline builds). MVS
implementation: `internal/mvs` (semver, index parse, the fixpoint,
lockfile read/write).

## Native vs self-hosted coverage

The two compilers are **not** at parity. The **native** compiler
(`internal/*`, the `fern` CLI) implements the whole surface below; the
**self-hosted** compiler's module loader
(`examples/self_host/modloader.fern` + `fern_toml.fern`) implements a
subset.

| Capability | Native | Self-hosted |
| --- | --- | --- |
| `path` deps + resolver-side isolation | ✅ | ✅ |
| `[workspace]` + `workspace = true` deps | ✅ | ✅ |
| vendored mode (`vendor/`) | ✅ | ✅ |
| versioned deps via `fern.lock` (`path` source) | ✅ | ✅ |
| `url`+hash deps + content-addressed store | ✅ | ✅ read-only (see below) |
| versioned deps whose lock source is a `url` | ✅ | ✅ read-only (see below) |
| `-resolve` (Minimum Version Selection → `fern.lock`) | ✅ | ✅ `path`-sourced index entries; absolute DIR only (see below) |
| CLI commands `-fetch` / `-vendor` / `-add` / `-checkws` | ✅ | ❌ (#6640) |

The self-hosted loader (`examples/self_host/modloader.fern` +
`fern_toml.fern`) resolves every **disk-resolvable** form: `path` deps,
`workspace = true` member deps (walking up to the `[workspace]` root and
matching by package name), vendored mode (`<vendor-root>/vendor/<name>/`
takes precedence), and versioned deps pinned in `fern.lock` to a `path`
source. All need no network or cache lookup.

The **store-backed** forms — `url` deps, and versioned deps whose lock
source is a `url` — resolve since #4949: the loader computes the store
path (`$FERN_CACHE_DIR/pkgs/<hex>/`, else `<user-cache>/fern/pkgs/<hex>/`
via `$XDG_CACHE_HOME` / `$HOME/.cache` / `$HOME/Library/Caches`) with the
`env` runtime builtin and reads the unpacked package from it —
`modloader.load_from_store`, mirroring native `pkgcache.Root()`/`Dir()`.
**Read-only**: populating and sha256-verifying the store stays a native
`fern -fetch` responsibility (the no-build-time-network constraint), so
the self-host loader trusts an already-populated store exactly as the
native loader does at build time.

Version RESOLUTION is the self-host's since #6640: `examples/self_host/mvs.fern`
ports native's `internal/mvs` — version precedence, the index format, the
max-of-the-minimums fixpoint, top-level `[exclude]` round-up, and the
`fern.lock` text — and `fern -resolve <ABS-DIR>` drives it. The two
compilers are pinned against each other by
`TestSelfHostResolveDifferentialX86_64`, which compares the emitted lock
byte for byte, refusals and their wording included. Two departures, both
of them the absence of a builtin rather than a resolution difference: the
directory must be absolute (there is no working-directory builtin), and an
index entry MVS selects by `url` is refused rather than fetched, since
populating the store stays native's `-fetch`.

The self-host wiring is **additive** throughout — consulted only when a
`fern.toml` is actually present — so the compiler's own manifest-less
bootstrap is byte-for-byte unchanged.

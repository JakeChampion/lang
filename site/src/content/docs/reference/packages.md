---
title: Packages
description: fern.toml, dependencies, workspaces, vendoring, and version resolution.
sidebar:
  order: 7
---

Packaging is opt-in. A program with no `fern.toml` anywhere above it
loads exactly as it always did — `import "./helper"` finds the file next
door. Add a manifest when you want a name, dependencies, or a boundary
the compiler will enforce.

## A package

```
app/
  fern.toml
  main.fern
```

```toml
[package]
name = "app"
version = "0.1.0"     # informational for now
lib = "lib.fern"      # entry module for `import "app"` (this is the default)
```

`name` is the only required key. A bare `import "<dep>"` from another
package resolves to that package's `lib` module; `import "<dep>/sub"`
resolves `sub.fern` inside its directory.

## Dependencies

Every dependency is declared, and **only declared dependencies
resolve** — a package can't reach a sibling it didn't ask for, whatever
the directory layout says.

```toml
[dependencies]
helper = { path = "../helper" }
webkit = { url = "https://example.com/webkit.tar.gz",
           hash = "sha256:<64 hex of the archive bytes>" }
lexer  = { workspace = true }
foo    = "1.1.0"
```

| Form | Resolves to |
| ---- | ----------- |
| `{ path = "…" }` | A local directory |
| `{ url = "…", hash = "sha256:…" }` | An archive in the content-addressed store |
| `{ workspace = true }` | The workspace member with that package name |
| `"1.1.0"` | The version [MVS](#versioned-dependencies) picked, from `fern.lock` |

Use `fern -add` rather than editing by hand — it manages the hash for
you and re-parses the manifest before writing, so a bad edit can't leave
a broken `fern.toml` on disk:

```bash
fern -add helper path:../helper
fern -add webkit url:https://example.com/webkit.tar.gz   # fetches, records the hash
fern -add lexer  workspace
```

## The hash is the identity

A `url` dependency is identified by its **hash**, not its URL. The URL is
a mirror hint; the `sha256:` is what's checked. An expired domain can't
substitute code, and nothing needs re-verifying later.

`fern -fetch` is **the only command that touches the network**. It walks
the manifest and its dependencies' manifests, downloads what's missing,
verifies each archive against its declared hash — a mismatch fails the
run and nothing is unpacked — and unpacks into a per-machine store at
`$FERN_CACHE_DIR` (or your user cache directory), shared across
projects.

Build, check and interp read that store and never fetch. If a `url`
dependency hasn't been fetched, they say so and point at `fern -fetch`.

## Capability grants

A dependency can be granted only the capabilities it needs:

```toml
[dependencies]
kv = { path = "../kv", capabilities = ["net"] }
```

The six v1 capabilities are `net`, `fs`, `env`, `subprocess`, `time` and
`random`. With the key present the compiler enforces the grant by
call-graph reachability — a logging library that reaches the network
fails the build (E070) rather than the audit. Grants **attenuate**: a
dependency may grant its own dependencies at most what it holds itself,
and an amplifying grant is a load-time error.

`fern -capabilities FILE.fern` prints what each package in a program can
reach, with an example call chain down to the runtime builtin.

## Vendoring

```bash
fern -vendor
```

Flattens the whole transitive graph into `vendor/<name>/`, one directory
per package. After that, builds are **fully offline and total**: the
loader resolves every declared dependency out of `vendor/`, ignoring the
originals entirely, and a declared dependency missing from `vendor/` is
an error rather than a quiet fall back to the network.

Names must be unique across the graph. `url` dependencies must be
fetched before vendoring — vendoring copies from the store, it doesn't
download.

## Workspaces

A workspace is a tree of related packages sharing one root:

```toml
[workspace]
members = ["compiler/lexer", "compiler/parser", "handlers/app"]
```

A member depends on another by name, with no brittle relative path:

```toml
# handlers/app/fern.toml
[package]
name = "app"
[dependencies]
lexer = { workspace = true }
```

`import "lexer"` resolves to the member whose **package name** is
`lexer`, whatever its directory is called. Isolation is unchanged: a
member reaches a sibling only if it declares the dependency.

`fern -check DIR` on a workspace root type-checks **every member**,
printing an `ok` / `FAIL` line each and exiting non-zero if any fail, so
one broken package doesn't hide the rest. `fern -vendor` on a root
vendors the union of the members' external dependencies into one shared
`vendor/`.

## Versioned dependencies

Versions resolve by **Minimum Version Selection**: a dependency declares
its *lowest* acceptable version, and resolution keeps the maximum of
those minimums across the graph. The constraint language is
minimum-only — no ranges — so a solution always exists, it's unique, and
it's computed by graph reachability rather than a solver.

```toml
[package]
name = "app"
index = "index.toml"

[dependencies]
foo = "1.1.0"
bar = { version = "1.0.0" }
```

The version source is a plain index file — there is no registry service:

```toml
[foo]
"1.0.0" = { path = "foo-1.0.0" }
"1.1.0" = { url = "https://…/foo-1.1.0.tar.gz", hash = "sha256:…" }
[bar]
"1.0.0" = { path = "bar-1.0.0" }
"2.0.0" = { path = "bar-2.0.0" }
```

```bash
fern -resolve
```

Runs MVS over the index, fetches and verifies any url-sourced versions,
and writes the chosen `(name, version, source)` set to **`fern.lock`**.
A demanded version missing from the index is an error, never a silent
round-up. If the root wants `foo >= 1.1.0` and `bar >= 1.0.0` but
`foo@1.1.0` needs `bar >= 2.0.0`, the lock pins `bar = 2.0.0`.

Builds read only `fern.lock` — never the index, never the network.

### Excluding a bad version

Minimum-only constraints can't say "not 1.9, it's broken", deliberately:
an upper bound documents a bug instead of fixing it. The escape hatch is
a top-level `[exclude]` table, applied only from the manifest you run
`fern -resolve` on, so a dependency can't change your resolution:

```toml
[exclude]
bar = ["1.9.0", "1.9.1"]   # a demand for either rounds up to the next
                           # non-excluded version in the index
```

Excluding every version at or above a demand is an error; excluding a
version nothing demands does nothing.

## How an import resolves

Inside a manifest-governed package, in order:

1. `std/` or `core/` — the embedded standard library.
2. `./x` or `../x` — relative to the importing file.
3. A `workspace = true` dependency — that workspace member.
4. A declared dependency, when a `vendor/` tree governs — `vendor/<name>/`.
5. A versioned dependency — the version pinned in `fern.lock`.
6. A declared dependency — its directory, or the store for a `url` dep.
7. An existing file at that path — so adding a `fern.toml` never breaks
   a program that already loaded.
8. Nothing — an error naming the `fern.toml` and the `[dependencies]`
   line to add.

The manifest is the authority: a declared dependency wins over a
same-named file next door.

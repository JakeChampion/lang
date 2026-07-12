# Module system + package management research

> **2026-07 update:** see `PACKAGE-MANAGEMENT-SOTA.md` for a
> state-of-the-art refresh (resolution-algorithm complexity, the
> newest-language peers, supply-chain security post-2025, and
> deltas to this doc's recommendations). Note Rec §10 (import
> aliasing) has since landed, as have `pub(package)` (#3095) and
> `std/semver` (#4886).

`internal/modload/modload.go` ships a working in-tree module
system: path-derived imports, private-by-default with `pub`
exports, recursive multi-file loading, cycle detection.
`docs/PRELUDE-TO-MODULES.md` covers the on-going `std/…` +
`core/…` migration. That's the *intra-project* dependency
story.

This doc is about the **inter-project** story: what happens
when the language picks up its first third-party package.
That question is unavoidable once the codebase has more than
one user / repo (which is technically not yet, but will be
once self-hosting completes and the Fern ecosystem grows
organically from the same repository structure).

The constraints carried over from `BOOTSTRAP-RESEARCH.md`:

- **No build-time network access.** Snapshots in-repo,
  vendored deps, content-addressed identifiers.
- **No build-time code execution beyond constfold.** Rules
  out Cargo's `build.rs` shape.
- **Reproducible builds.** Lockfiles, content hashes, no
  "latest-compatible-at-resolve-time" ambiguity.

Within those, the design space is still wide. This doc
surveys Cargo, Deno/jsr, Go modules, npm, Hex, Nimble,
Maven, Nix flakes, Swift PM, and Bazel — picks the
transferable bits, names the anti-patterns to skip.

## Framing — what we actually need from packages

Reasoning from the codebase's targets:

- **CLI tools and edge handlers** import a *small* number
  of third-party packages. Roughly: a typed HTTP client, a
  cookie parser, an OAuth helper, a JWT verifier, maybe a
  regex library. Not the JS-ecosystem 1000-deps tree; not
  even the Rust 50-deps tree. Single-digit dep counts are
  realistic.
- **Stdlib covers most of the surface area.** HTTP, JSON,
  date/time, I/O, format, log, base64, hex — all in-stdlib
  (per `STDLIB-DESIGN-RESEARCH.md`). Third-party space is
  *application-layer* libraries.
- **Static linking is the default.** No runtime dep loading.
  The package manager produces a flat source tree the
  compiler walks; output is a single binary.
- **Cross-target builds.** Same source compiles for arm64-
  linux + arm64-darwin + wasi-http + x86_64. Packages
  must compose across targets cleanly.

The interesting design space, given those constraints:

1. **Identification.** What names a package? Path, URL,
   hash, registry coordinate?
2. **Versioning.** Semver, calendar, content-hash, or no
   versioning?
3. **Resolution.** When multiple packages disagree on a
   shared dependency's version, how is the conflict
   resolved?
4. **Manifest + lockfile.** Format and granularity.
5. **Distribution.** Central registry, decentralised
   URLs, git, or vendored?
6. **Build extensibility.** Compile-time hooks, codegen,
   linker flags — what's allowed?

Most of the rest of this doc is the specific answers to
those six questions from each surveyed source.

## What we already do well — call out so we don't drift

- **Private-by-default with `pub`.** OCaml signatures /
  Rust `pub` / Roc exports converge. Private-by-default
  is the right surface; exports are an opt-in.
- **Path-derived module names.** `import "std/http"` →
  module `http`. No coordinate-to-name mapping. Less
  ceremony than Maven / Cargo. Aliasing (`import … as`)
  isn't yet supported — that's a small gap (Rec §10).
- **Mangling for name collisions handled by the loader,
  not the user.** Two modules can both define `parse`;
  the loader gives them disjoint mangled names; the
  user never sees the mangling.
- **Cycle detection at load time.** Cycles fail fast with
  a clean diagnostic, not at codegen.
- **`pub` is the *only* visibility modifier.** Not `pub`,
  `pub(crate)`, `pub(super)`, `pub(in path)`. Rust's
  five-level visibility ladder is a notorious source of
  ceremony; the codebase's binary public-or-private is
  the right floor.

## Single-source deep dives

### Cargo — crates.io, semver, the canonical full-featured PM

Sources:
- https://doc.rust-lang.org/cargo/
- https://crates.io/
- "The Rust Edition Guide" (for the `edition` field).

**The Cargo design is the reference point most modern
package managers gravitate toward.** Worth understanding
in full, because the parts that *work* are widely copied
and the parts that *don't* are widely cited as warts.

#### What Cargo gets right

- **`Cargo.toml` is human-readable and writable.** No
  generated XML, no JSON-only-comments-disallowed format.
  TOML hits the right balance of strict-and-skimmable.

- **Semver-range dependency specifications.** `serde =
  "1.0"` means "any 1.x.y compatible with 1.0." Cargo
  resolves to the highest version in the range. Rejects
  incompatible breaking changes by ecosystem convention
  (semver is a *promise*, not a guarantee).

- **`Cargo.lock` records the exact resolved versions.**
  Reproducible builds, by construction. Bin crates check
  it in; library crates conventionally don't (so dep-of-
  dep users get fresh resolution).

- **Workspaces.** A top-level `Cargo.toml` declares
  `members = [...]`, each member is a sub-crate sharing
  the same lockfile + target directory. Build all at
  once, test all at once. Right shape for monorepos.

- **`[features]` table.** Compile-time-selectable features
  with optional dep gating. `serde = { version = "1.0",
  features = ["derive"] }`. Lets crates ship multiple
  configurations without separate sub-crates.

- **`cargo doc`, `cargo test`, `cargo bench`, `cargo fmt`,
  `cargo clippy` all live in the same tool.** One UI for
  everything.

- **Crates.io has a "sum DB"-equivalent**: each published
  version has a content hash recorded immutably. Yanking
  is a flag (removes from default resolution; existing
  lockfiles still work).

#### What Cargo gets wrong (well-known)

- **`build.rs` is a free-form Rust script that runs at
  build time** with the full host privilege. Used for:
  bindgen, codegen, environment probing. Necessary for
  Rust's C-FFI breadth; reproducibility-hostile and
  security-relevant. Bazel / Buck / Nix users routinely
  treat Rust crates with `build.rs` as suspect.

- **No central tag for *what kind of dep* is needed.**
  Build-time tool (proc-macro), runtime library, test
  helper all use the same `dependencies` shape with
  `dev-dependencies` as a partial workaround. Could be
  more explicit.

- **Workspace member discovery is awkward** for nested
  workspaces (one workspace inside another's directory).

- **No cross-language story.** Rust crates that need
  C-side code build it via `build.rs` calling `cc`. Each
  crate reinvents this; no standard.

- **Yanking is per-version, not per-version-range.**
  Yanking a vulnerable version doesn't refuse to resolve
  *for all of `^1.0`* even if every later 1.x exists.

#### What translates

- **TOML manifest** — `fern.toml` (or `package.toml`) per
  package, human-readable. Yes.

- **Lockfile** — `fern.lock`. Records the exact resolved
  version + content hash per dep. Bin crates (apps) check
  in; lib crates don't.

- **Workspace support** — multi-package monorepo from day
  one. Top-level `fern.toml` lists members; one lockfile;
  one target directory.

- **Single `fern` CLI for build/test/fmt/doc/run.** Already
  the case; preserve.

- **Content-hash record per published version** — same as
  Cargo / Go sum DB.

#### Considered, left

- **`build.rs`-shape arbitrary build scripts.** Reproducibility-
  hostile. Per `BOOTSTRAP-RESEARCH.md ▸ Anti-patterns`, no
  build-time code execution beyond constfold. If a crate
  needs codegen, it ships the generated code (and the
  generator as a separate tool the user runs explicitly
  before building). Vendor the generated output.

- **Features as a first-class concept.** Cargo features
  are notorious for combinatorial explosion (a crate
  with N features has 2^N possible build configurations
  to test). Replace with: per-target stdlib subset (e.g.
  `wasi-http` target excludes `std/tcp`), no user-defined
  features.

- **Five-level visibility ladder.** Cargo plus Rust have
  `pub`, `pub(crate)`, `pub(super)`, `pub(in path)`,
  private. We have `pub` and private. Don't expand.

### Deno / jsr.io — URL imports and the registry-as-CDN

Sources:
- https://jsr.io/
- Deno's "Imports work like the web" design talks
  (Ryan Dahl, 2018-).
- https://docs.deno.com/runtime/manual/basics/import_maps/

**Deno's headline design: import URLs directly.**

```typescript
import { serve } from "https://deno.land/std@0.220.0/http/server.ts";
```

The package is fetched on first run, cached locally,
content-hashed. No central registry needed; any HTTPS
host can serve packages. The version is *part of the URL*.

**jsr.io** is Deno's *opt-in* registry — a CDN with
JS-specific niceties (typed metadata, type-coverage
reporting, semver ranges). Coexists with the URL-only
mode.

#### What Deno/jsr gets right

- **Versioning is part of the identifier.** No "latest"
  resolution at runtime; the URL pins a specific version.
  Lockfile records the content hash for byte-level
  reproducibility.

- **No central registry needed for ecosystem to exist.**
  Anyone with HTTPS can host. jsr.io is one (high-
  quality) option; deno.land/x is another; private hosts
  are first-class.

- **Content addressing.** Each fetched dep is content-
  hashed; mismatched content fails the build. Equivalent
  to Go's sum DB.

- **No `node_modules`-shape recursive directory.** One
  flat cache per machine; dep resolution is a tree but
  storage is flat.

#### What Deno/jsr gets wrong (or controversially right)

- **URLs as identifiers age badly.** Domain disappears →
  uninstallable dep. Mitigated by ranger-style mirroring
  but the URL-is-the-identifier story is fragile.

- **Type stripping at runtime.** Deno-specific; doesn't
  apply to Fern (we're AOT-compiled).

#### What translates

- **Content-addressed identification.** The package's
  identifier *includes its content hash*, not just its
  name + version. Lockfile entries are 1:1 with hashes.

- **No central registry required for the ecosystem.**
  Packages can live in a git repo, on a CDN, or in a
  registry. The package manager doesn't *require* a
  single trusted server.

- **Versioning as part of the import.** Optional sugar:
  `import "github:foo/bar@1.2.3"` is the long form;
  a `fern.toml`-listed dep gets a short form
  `import "bar"`.

#### Considered, left

- **Full URL imports as the *primary* surface.** The
  ergonomics drift toward unmaintainable. The package
  manager handles URLs; the user mostly types short
  names that resolve through the manifest.

### Go modules — Minimum Version Selection

Sources:
- https://go.dev/ref/mod
- Russ Cox, "Versions in Go" (2018 blog series).
- https://sum.golang.org/ (the sum DB).

**The most novel design of any modern package manager.**
Go modules introduce **Minimum Version Selection (MVS)**:

> When resolving a dependency graph, pick the *minimum*
> version each `go.mod` declares satisfies. Never the
> latest "compatible" version; the *exact* version the
> user wrote, plus any minimum-version constraints from
> transitive deps.

This is the inverse of every other package manager. npm,
Cargo, pip all resolve to the *maximum* compatible
version; Go to the *minimum*.

#### Why MVS wins

- **No "latest compatible" surprise upgrades.** Cargo's
  `serde = "1.0"` silently upgrades to 1.0.197 when 1.0.0
  is what you tested. With MVS, 1.0.0 stays 1.0.0 until
  you explicitly bump it.

- **The lockfile is *redundant* in MVS.** The version is
  fully determined by the manifest + transitive
  manifests. (Go still has `go.sum` for content hashes,
  but the version itself is unambiguous from `go.mod`s.)

- **Reproducibility falls out for free.** Two developers
  resolving the same `go.mod` get the same versions, no
  matter when they resolve.

- **Updating is explicit.** `go get pkg@version` bumps;
  nothing else does.

#### What MVS gets wrong (or is criticised for)

- **Bug fixes don't propagate automatically.** If 1.0.0
  has a security bug fixed in 1.0.1, and your `go.mod`
  pins 1.0.0, you keep the bug until you bump explicitly.
  Cargo's `^1.0` auto-pulls 1.0.1; Go does not.

- **Major version bumps are awkward.** Go requires the
  package import path to *change* (`pkg/v2`) for a major
  bump. Different style from semver's "major version is
  in the version number."

- **Diamond dependencies** still possible; MVS chooses
  the *highest* of the minimums. Usually fine; not
  always.

#### Go's sum DB

`sum.golang.org` is a transparent log of every
`(module, version) -> content-hash` ever published.
Builders check against it; mismatch = bail. Prevents
"someone re-published a different version with the same
tag" attacks. Modeled on Certificate Transparency.

#### What translates

- **Minimum Version Selection.** Worth adopting. The
  reproducibility win is real; the "auto-upgrade" loss
  is mostly a feature (the user *explicitly* updates,
  doesn't get surprised).

- **Content-hash sum DB.** Already implied by the
  content-addressing scheme (Deno) — make it explicit:
  the manifest records the expected hash for each dep,
  the package manager verifies on fetch.

- **Major version in the import path.** Forces upgrade
  consideration; cleanest semantic. `import "github:
  foo/bar/v2"` is fine.

#### Considered, left

- **The transparency log infrastructure.** Go's
  sum.golang.org is a real service. For a single-user
  language, in-repo `fern.lock` with content hashes is
  enough.

### npm — what to learn from the failure modes

Sources:
- https://docs.npmjs.com/
- "npm Has a Quasi-Standard Lock File" — multiple posts
  on lockfile woes.
- The 2016 `left-pad` incident.

**npm is the inverse reference: study what *not* to do.**

- **Default-to-latest-compatible resolution** + a *modifiable*
  lockfile that npm sometimes ignores. The famous "I ran
  `npm install` and got different versions" failure mode.
  Solved by `npm ci` (strict lockfile), but the default
  is wrong.

- **Default mutation of `package.json` on install**. `npm
  install foo` modifies `package.json`. Reproducibility
  loss disguised as ergonomics.

- **node_modules**. Recursive directory tree, files
  duplicated across many crates, no flat cache. Pnpm
  fixed this with content-addressed symlinks; npm did not.

- **`postinstall` scripts** — arbitrary code execution at
  install time. Reproducibility-hostile *and* security-
  hostile. The 2018 `event-stream` attack injected
  malicious code via a `postinstall`-running dep.

- **No semantic difference between "I depend on X" and
  "I depend on X as a build tool"**. `devDependencies`
  is a partial workaround.

- **Registry-by-name only.** No git imports, no URL
  imports without ceremony. Lock-in.

#### What translates

Anti-patterns to avoid:

- **Auto-modify-manifest-on-install.** No. `fern add foo`
  is explicit; modifying `fern.toml` is what the command
  does, not a side effect of build.
- **`postinstall`-shape scripts.** No.
- **Default to latest-compatible.** Use MVS (Go's
  approach).
- **Recursive `node_modules`-shape directory tree.** Flat
  content-addressed cache per machine; build artifacts
  reference the cache.

### Hex (Erlang / Elixir)

Sources:
- https://hex.pm/
- https://hexdocs.pm/mix/Mix.Tasks.Deps.html

**Hex is a central registry with **signed** packages,
content-hashed, with a "preferred CDN" for fetches.**

#### What Hex gets right

- **Every published package is signed by the publisher.**
  The user's machine verifies the signature at fetch
  time. Compromised registry doesn't substitute malicious
  packages.

- **mix.lock is canonical** and required for builds.

- **Documentation generation is centralised** —
  hexdocs.pm builds and hosts every published package's
  documentation automatically. Discoverability is a
  first-class feature, not an afterthought.

- **The protocol is *http*; the registry isn't required.**
  Hex.pm is the default but private Hex servers are
  first-class for company internal packages.

#### What translates

- **Signing.** When/if a registry exists, sign every
  release. Verification at fetch.

- **Auto-generated central docs.** A `fern doc` workflow
  that produces structured documentation per published
  package; hostable on a single shared site. Even if
  there's no central registry, having `fern doc` *work*
  for any package is the foundation.

### Nix flakes — pure-functional, content-addressed

Sources:
- https://nixos.wiki/wiki/Flakes
- "Nix Pills" Volumes 1-3.

**Nix's flakes are the most rigorous of any package
manager.** Every input is content-addressed; every build
is pure and reproducible; flake.lock records every
dependency's hash + the registry resolution.

#### Key ideas

- **Inputs are sources** (a tarball, a git ref, another
  flake's output). Every input is hashed; the build is
  pure-functional of the inputs.

- **Outputs are derivations** — recipes the build system
  executes deterministically.

- **`flake.lock`** records every input's *hash* (not
  just its semver). Resolution is fully reproducible
  across machines and times.

- **No package manager configuration files in user-home.**
  Flakes are self-contained.

#### What translates

- **Pure-functional build.** Already aligned: the Fern
  compiler is a deterministic function of source files +
  config + dep set. Stay there. Per
  `BOOTSTRAP-RESEARCH.md`, "don't add build-time code
  execution beyond constfold" is the same principle.

- **Lockfile records content hashes, not just versions.**
  `fern.lock` lists `(name, version, content-hash)` per
  dep. Verification on fetch.

- **Inputs explicit, no implicit environment.** A
  `fern.toml`'s dep list is the *whole* dependency
  specification — no env-var-driven path resolution,
  no parent-shell influence.

#### Considered, left

- *Going full Nix-style derivation graph.* Multi-year
  build-out; not necessary for single-language reproducibility.

### Swift PM — git-based with no central registry

Source:
- https://swift.org/package-manager/

**Swift packages are git repositories with a `Package.swift`
manifest.** Versions are git tags (`1.2.3`). No central
registry; dependencies are URLs to git hosts (typically
GitHub).

#### What translates

- **Git-tag-based versioning** is the simplest
  decentralised story. No registry required.

- **`Package.swift` as a *swift* file**, not TOML/YAML.
  Trade-off: easier to express conditional config; harder
  to parse without a swift compiler. *Not* what we want
  (manifests must be parseable cheaply during dependency
  resolution, often before the Fern compiler is built).

### Bazel / Buck — monorepo build systems

Sources:
- https://bazel.build/
- https://buck.build/

**Not package managers in the same sense, but the
reference for monorepo / multi-language builds.** Bazel
treats every directory as a "package" with a BUILD file
listing targets; one global build graph; remote-execution +
caching.

#### What translates

- **Workspace** = a directory tree of related packages,
  one shared build state. Modeled on Cargo workspaces +
  Bazel workspaces; right shape.

- **Remote build cache** — a stretch goal. Once the
  language picks up a multi-developer workflow, a
  content-addressed shared cache cuts CI time
  dramatically.

#### Considered, left

- *Per-directory BUILD-file shape.* Cargo's manifest-per-
  package model is more lightweight. Save Bazel-shape
  for if/when the workspace grows past hundreds of
  packages.

### OCaml's ML modules + dune

Sources:
- https://dune.build/
- OCaml's `.cmi` / `.cmx` story (per
  `BOOTSTRAP-RESEARCH.md ▸ OCaml`).

**OCaml has two layers:**

1. **Language-level modules** — `module Foo : sig … end =
   struct … end`. First-class within the language. Modules
   can be passed around, nested, parameterised (functors).

2. **Package-level** — dune (the modern build tool) +
   opam (the package manager). dune manages workspace +
   build; opam manages remote deps.

The interesting bit is the *separation of concerns*: the
language's module system is module shapes; the package
manager handles *files on disk + remote retrieval*. Our
`internal/modload` is the file-on-disk part; a future
`fern.toml` + remote-fetch is the package-manager part.

#### What translates

- **Strict separation.** Module-as-language-construct is
  what `pub` is for. Package-as-distribution-unit is what
  `fern.toml` + lockfile + registry are for. Don't
  conflate; OCaml's separation is what lets ML modules
  scale across decades.

## Cross-cutting themes

1. **Lockfile is universal.** Cargo.lock, mix.lock,
   go.sum + go.mod, flake.lock. Reproducible builds
   need pinned content hashes; no exceptions.

2. **Content-addressing is universal in modern
   designs.** Cargo's crates.io hash, Go's sum DB,
   Nix's everything, Deno's URL+hash, Hex's signature.
   Trust the bytes you fetched.

3. **Build-time code execution is contested.** Cargo
   has it (build.rs); Nix forbids it; Go forbids it; npm
   has it (postinstall) and regrets. Our line: forbid.

4. **Minimum Version Selection > Maximum Version
   Selection.** Less surprising, more reproducible. The
   "auto-fix" loss is a feature (forces explicit
   bumps).

5. **No central registry is *necessary* for ecosystems.**
   Deno, Swift PM, Nimble all run without one. Hex /
   Cargo work better *with* one but don't require one.

6. **Workspaces are universal in modern systems.**
   Cargo, Go, Bazel, dune. Monorepo-as-first-class.

7. **Features explode.** Cargo features cause
   combinatorial-build-config problems. Replace with
   per-target subsetting + no user-defined features.

8. **Major-version-in-the-path** (Go style) or
   **major-version-in-the-coordinate** (Cargo style).
   Pick one. Path-embedding is less ambiguous; coordinate-
   embedding is more familiar.

## Concrete recommendations

Ordered by leverage × cost. Several recommendations are
ordered after the bootstrap-and-self-host work — the
package manager is a *post-self-host* concern; nothing
here blocks the current trajectory.

### 1. Define `fern.toml` as the manifest format

**Cost: 1 week.** **Impact: foundation; gates §2-9.**

Per-package manifest. TOML. Schema:

```toml
[package]
name = "my-handler"
version = "0.1.0"
edition = "2026"               # opt-in to language flavour year

[targets.wasi-http]
entry = "src/main.fern"

[targets.arm64-linux]
entry = "src/main.fern"

[dependencies]
auth = "1.2"                                # registry-resolved (later)
crypto = { git = "https://github.com/...", tag = "v0.3" }
local-helper = { path = "../helpers" }
config = { path = "../config", optional = false }
```

A `fern.toml`-less single-file program stays valid (today's
behaviour). The manifest is opt-in; once present, becomes
the build-system root.

### 2. Lockfile design: `fern.lock`

**Cost: 1 week (after §1).** **Impact: gates §3.**

TOML or JSON; records exact resolved versions + content
hashes per direct + transitive dep:

```toml
[[dep]]
name = "auth"
version = "1.2.0"
source = "https://lang.dev/pkg/auth/1.2.0.tar.zst"
hash = "blake3:1234..."

[[dep]]
name = "crypto"
source = "git+https://github.com/.../crypto.git#v0.3"
git-rev = "abc123..."
hash = "blake3:5678..."
```

Apps check it in; libraries don't (lockfile is for the
*final binary*, transitive resolution happens fresh in
dependents).

### 3. Adopt Minimum Version Selection

**Cost: 2 weeks.** **Impact: high; correctness win.**

Resolution algorithm:

- Walk transitive `fern.toml`s.
- For each unique package name, pick the **maximum of the
  minimum** versions across all manifests that declare it.
- Pin that exact version.

Mirrors Go's MVS. Reproducible by construction; no
"latest compatible" surprises.

Upgrade is explicit: `fern upgrade <pkg> [version]`
mutates `fern.toml` + `fern.lock`.

### 4. Content-addressed cache

**Cost: 2 weeks.** **Impact: medium-high; reproducibility +
disk-space.**

Single per-machine cache at `~/.cache/lang/pkgs/<blake3>/`.
Each package version's tarball is content-hashed; the
hash is the directory name; the contents are unpacked
once and reused.

`fern.lock` references the hash; mismatched hash on
fetch → fail. No `node_modules`-style recursive trees;
build references the cache.

### 5. Workspace support

**Cost: 1 week.** **Impact: medium-high; needed once the
codebase has multiple fern-implemented packages.**

Top-level `fern.toml` declares:

```toml
[workspace]
members = [
    "compiler/lexer",
    "compiler/parser",
    "compiler/checker",
    "handlers/auth",
    "handlers/main",
]
```

Each member is a sub-package with its own `fern.toml`.
One lockfile at the workspace root; one `target/`
directory.

For the eventual self-hosted Fern compiler (per
`BOOTSTRAP-RESEARCH.md`), the workspace shape lets the
lexer / parser / checker / codegen modules live as
separate packages without leaving the repo.

### 6. Vendoring as the default offline workflow

**Cost: 1 week.** **Impact: medium; per
`BOOTSTRAP-RESEARCH.md` constraint of no build-time
network.**

`Fern vendor` copies all transitive deps into a `vendor/`
directory at the workspace root. Subsequent builds with
`--offline` use `vendor/` exclusively; no network access.

CI / release builds run `--offline` mode; `Fern vendor`
runs as a separate explicit step.

This is the right shape for `BOOTSTRAP-RESEARCH.md`'s
"no build-time network access" constraint — the package
manager *can* fetch from the network, but the *compiler*
never does.

### 7. No `build.rs` analogue; codegen as a separate phase

**Cost: 0 (a deferral).** **Impact: avoids a Cargo wart.**

Packages that need code generation ship the generator as
a separate `fern` binary (e.g. via `bin/` in the package
or as a separate dev-tool dep). Users run the generator
*before* `fern build`. The generated code is checked in
to the consuming package.

No `[build-script]` config. No "this package runs Python
during build." Build is `fern.toml` + source files in,
binary out, deterministic.

### 8. Cross-target via `[target.X]` sections

**Cost: 1 week.** **Impact: medium; aligns with multi-
target codebase.**

```toml
[targets.wasi-http]
entry = "src/main.fern"
deps = ["std/wasi-http"]

[targets.arm64-linux]
entry = "src/main.fern"
deps = ["std/tcp"]
```

Per-target dep lists; the compiler resolves once per
target. Same source, different platform-deps.

This is the natural place for the per-target capability
descriptor sketched in `PLATFORM-RESEARCH.md ▸ Rec §2`.

### 9. No central registry, but a curated index

**Cost: ongoing, ~1 day per published package.** **Impact:
low for one user; high for the eventual ecosystem.**

Skip the run-our-own-registry investment. Packages live
in git repos; `fern.toml` declares them by URL + tag /
git-ref. Content hash verification at fetch.

A *curated index* (a single markdown / JSON file at
`https://lang.dev/packages.json` or similar) lists
known-good packages with metadata. Discovery; not
binding. Like Awesome-X lists. Cheap to start, easy to
abandon.

### 10. `import "long/path/foo" as f`

**Cost: 3 days.** **Impact: small but resolves a known
gap in `internal/modload/modload.go`.**

Already documented as a limitation in `modload.go`:

> Aliasing (`import "./long/path" as p`) isn't supported;
> the local name always comes from the path basename.

Add the syntax + the bindings. Useful when two imports
have the same basename:

```
import "std/http" as http;
import "outbound/http" as outbound_http;
```

### 11. Documentation tooling: `fern doc`

**Cost: 2 weeks.** **Impact: medium-high; discoverability
is the difference between an ecosystem and a graveyard.**

For each `pub` declaration:

- Extract the leading comment as the doc.
- Render as Markdown + HTML.
- Generate a per-package documentation site.

Mirror godoc / rustdoc / hexdocs in shape. Run as part
of `fern doc`; outputs `target/doc/`. If a hosted index
exists (Rec §9), packages submit to it.

### 12. Signing and verification

**Cost: 2 weeks; deferred.** **Impact: low for one user;
high for an ecosystem.**

Each published package version is signed with the
publisher's key (sigstore / cosign / minisign).
Lockfile records the expected signature. Verification
on fetch.

Defer until the ecosystem has more than one publisher.
Hex.pm's signed-by-default posture is the model.

## Anti-patterns — explicit "do not adopt"

- **`build.rs`-shape arbitrary build-time code execution.**
  Per `BOOTSTRAP-RESEARCH.md`. Reproducibility-hostile,
  security-relevant. Codegen runs as an explicit pre-
  step.

- **`postinstall`-shape scripts on install.** Per npm's
  cautionary tale.

- **Auto-modifying-manifest on `install`/`add`.**
  Mutating `fern.toml` is what `fern add` does; nothing
  else.

- **Default to latest-compatible resolution.** Use MVS;
  bumps are explicit.

- **Single-source-of-truth central registry that the
  ecosystem cannot exist without.** Deno's "anyone with
  HTTPS can host" pattern is more robust.

- **`node_modules`-shape recursive duplicated directory
  tree.** Content-addressed flat cache.

- **Cargo features as a user-facing primitive.** Per-
  target subsetting handles the "feature flag" use case
  without combinatorial explosion. No user-defined
  feature flags.

- **Maven-style XML manifests.** TOML is the right
  format.

- **Manifests as code (Swift's `Package.swift`).**
  Static, declarative, cheap-to-parse.

- **Five-level visibility ladder** (Rust's `pub`,
  `pub(crate)`, `pub(super)`, `pub(in path)`). `pub` +
  default-private is enough.

- **Different identifiers for the same package across
  different consumers** (Maven's `groupId:artifactId`
  + `version` + `classifier` combinations). One name
  per package.

- **Pre-1.0 versioning conventions that break semver.**
  Honour semver from 0.x; treat 0.x.y → 0.(x+1).0 as a
  breaking change; cleaner than the "0.x is always
  unstable" convention.

## When to revisit

- **When the fern-port reaches feature parity** (per
  `BOOTSTRAP-RESEARCH.md`'s "Full compiler self-host
  on wasm" milestone). At that point the compiler
  itself becomes a multi-package workspace and §5
  (workspaces) is overdue.

- **When the second non-stdlib package is needed.**
  Today: zero third-party packages. The instant a real
  application wants a typed JWT verifier or a cookie
  parser, §1 (manifest) becomes the next step.

- **When two packages need the same dep at different
  versions.** §3 (MVS) gets exercised; bugs in resolution
  surface here.

- **When CI start-up time exceeds 30 seconds.** §6
  (vendoring) + §4 (content-addressed cache) become
  high-leverage.

The single highest-leverage recommendation, when the time
comes, is **§1 (manifest format) + §2 (lockfile)** as a
pair. Everything else hangs off them. Rec §3 (MVS) is
the most opinionated piece; the rest are mechanical.
The whole package-manager build-out is roughly 2-3
months of work — do it after self-hosting, not before.

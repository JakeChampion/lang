# Package management: state of the art (2026 refresh)

Companion to `MODULE-PACKAGES-RESEARCH.md` (the 2026-06 survey of
Cargo, Deno/jsr, Go modules, npm, Hex, Nix flakes, Swift PM, Bazel,
dune). That doc's framing, constraints, and recommendations §1–§12
still stand. This doc is the **state-of-the-art refresh**: the areas
the first survey skipped or compressed — dependency-resolution
algorithms *as algorithms*, enforced-semver / API-evolution tooling,
what the newest generation of small languages (Zig, Roc, Gleam,
MoonBit) actually shipped, content-addressed stores in practice, and
the 2024–2026 supply-chain-attack wave with the defenses that emerged
from it — plus what has changed **in this repo** since the survey was
written.

Provenance note: the core findings below (resolution complexity, MVS,
PubGrub, Go proxy/sumdb, pnpm storage, JSR, cargo-semver-checks) were
adversarially fact-checked against primary sources (3-vote
verification per claim; one claim was refuted and is called out
below). The newer-language sections (Zig/Roc/Gleam/MoonBit, Elm
mechanics, the npm attack timeline) are corroborated against primary
docs but did not go through the same multi-vote pass — treat their
fine details as strong-but-unverified.

Constraints carried over unchanged (from `BOOTSTRAP-RESEARCH.md`,
restated in `MODULE-PACKAGES-RESEARCH.md`):

- **No build-time network access.** The package manager may fetch;
  the compiler never does.
- **No build-time code execution beyond constfold.** No `build.rs`,
  no `postinstall`.
- **Reproducible builds.** Lockfiles, content hashes, no
  latest-at-resolve-time ambiguity.

Every finding in this refresh strengthens those three constraints;
none argues against them.

## What changed in this repo since the first survey

The first survey's "where we are" section is stale in four ways —
all in the direction of *more* of the substrate existing:

1. **Import aliasing landed.** `import "std/test" as t;` is
   implemented (`internal/parser/parser.go:605-612`,
   `internal/modload/modload.go:29-33`) — the survey's Rec §10 is
   done. Duplicate-alias rejection is tested
   (`modload_test.go:145,167`).
2. **`pub(package)` visibility landed** (#3095,
   `docs/PUB-PACKAGE.md`): same-directory package scoping
   (`modload.go:319-332`). The visibility surface a package
   boundary needs (private / `pub(package)` / `pub`) now exists.
3. **`std/semver` landed** (#4886): SemVer 2.0.0 parse + full
   precedence compare as a stdlib module
   (`internal/stdlib/std/semver.fern`). A future resolver's
   version-ordering kernel is already written in Fern — what's
   missing is a *range/constraint* type (`^1.2`, `>=1.2 <2`) and
   the resolution algorithm on top. Notably, if the resolution
   algorithm is MVS (below), the constraint type stays trivial:
   a bare minimum version per dep, no range grammar at all.
4. **Per-module compilation is an active epic** (#3451; steps 1–4
   merged, #3457/#3458 open). Incremental codegen keyed on
   per-module compilation inputs + cross-module signature hashes
   (#3458) is exactly the shape a package cache wants: a package
   is "a module subtree with a frozen signature set." The package
   manager and the incremental-build cache should share the
   content-hashing substrate rather than inventing two.

Also unchanged and load-bearing: module loading is intra-project
only. `resolveImportPath` (`modload.go:556-575`) knows exactly two
namespaces — `stdlib://` (embedded via `go:embed`) and
importing-file-relative disk paths. There is no manifest, no
lockfile, no remote fetch, no vendoring, no registry anywhere in
shipping code. A package manager integrates with a greenfield
surface, which is the best time to pick the design.

## Resolution algorithms: the actual math

The first survey recommended MVS on reproducibility grounds. The
algorithmic literature makes the case sharper — the choice of
constraint language, not the solver, is the real decision.

### Version selection with arbitrary constraints is NP-complete

Cox's reduction (research.swtch.com/version-sat) encodes 3-SAT into
dependency resolution using only four assumptions nearly every
package manager satisfies: versioned deps, mandatory transitive
installation, per-version dep lists, and "two versions of the same
package cannot both be installed." PubGrub's own design doc states
the same NP-hardness as its foundational constraint, and the result
is independently corroborated by the peer-reviewed EDOS/OPIUM work
on Linux-distro dependency solving. **Verified 3-0.**

There are exactly three exits, and every shipping design is one of
them (or a hybrid):

1. **Solve it anyway with a smart solver** (PubGrub — below).
2. **Restrict the constraint language until the problem is easy**
   (Go's MVS — below).
3. **Let two versions coexist** so *a* solution always exists
   (npm's nested tree; Cargo's duplicate-across-major-versions).
   Finding the *smallest* solution stays NP-complete, but nobody
   needs the smallest. Combined with semver this becomes "different
   major versions are different packages" — Cargo unifies within a
   semver-compatible range and duplicates across incompatible ones
   (0.x minors count as incompatible); Go puts the major version in
   the import path (`pkg/v2`). **Verified 3-0.**

### PubGrub: the state of the art for expressive constraints

If a design wants full version *ranges* (`>=1.2 <2`, exclusions),
the state of the art is PubGrub (Dart's pub; adopted by uv,
pubgrub-rs, and others). It is Conflict-Driven Clause Learning from
SAT solving (specifically the clasp answer-set-solver variant)
adapted to versions: each conflict's root cause is recorded as a
learned "incompatibility," so backtracking never re-explores known
dead ends. Its distinguishing feature over an opaque SAT solver is
**human-readable failure proofs** — a derivation DAG of
incompatibilities that reconstructs *why* no solution exists,
rendered as an English explanation. **Verified 3-0.**

The lesson is not "use PubGrub"; it's that expressive constraints
are only acceptable *with* PubGrub-class conflict explanation, which
is a multi-week solver project. A small language that doesn't want
to fund that project should restrict the constraint language
instead.

### MVS: restrict the language, get determinism for free

Go's Minimum Version Selection permits exactly one constraint form:
"at least version X." Resolution is then: gather the transitive
closure of requirements, keep the per-module maximum of the stated
minimums. Under this restriction a solution always exists, the
solution set forms a lattice with a unique minimum, and resolution
is linear-time graph reachability — no solver, deterministic by
construction. (matklad's 2024 re-derivation: MVS's constraint
language sits in the intersection of 2-SAT, Horn-SAT, and
Dual-Horn-SAT; Horn formulas have unique minimal models.)
**Verified 3-0.**

Two consequences the first survey under-stated:

- **The lockfile becomes redundant *for version selection*.** MVS
  never chooses a version not written in some manifest in the
  build, so publishing a new version changes nothing until someone
  explicitly bumps. `go.sum` exists only for *content integrity*,
  not version pinning. A Fern `fern.lock` therefore carries hashes,
  not resolution decisions — it can be regenerated from manifests
  at any time and only the hashes are load-bearing. **Verified
  3-0.**
- **Upper bounds are inexpressible by design.** Cox's position:
  a version conflict/exclusion constraint "documents a bug instead
  of fixing it." The known critiques of MVS (Boyer's survey)
  attack this practical stance — sometimes you really do need
  "not 1.9, it's broken" — not the math. Go's answer is
  `exclude` directives in the *top-level* module only, which
  preserves determinism. Worth copying if MVS is adopted.

## Distribution: the two poles, and what small languages picked

The verified evidence brackets the design space with two poles that
both *work*, plus the actual choices of Fern's nearest peers.

### Pole 1 — Go: decentralized fetch + centralized transparency

Go has no registry: modules are URLs into version control. What
makes that safe and fast is two pieces of default-on, Google-run
infrastructure (since Go 1.13):

- **A caching mirror** (proxy.golang.org) that stores module
  metadata + source in its own storage, so code survives upstream
  deletion and `go get` never pulls full VCS history for a
  transitive dep. Caveat verified: persistence cuts both ways — a
  Feb 2025 incident had a malicious package persisting in the
  mirror after upstream cleaned it. **Verified 3-0.**
- **A checksum transparency log** (sum.golang.org), a
  Certificate-Transparency-style append-only Merkle tree (Trillian)
  of `(module, version) → hash`. This exists because a per-project
  lockfile of hashes **alone works by trust-on-first-use**: the
  hash is recorded on first download and never compared with what
  anyone else saw, so a targeted-at-you malicious first download is
  undetected. The log closes exactly that hole — first-time
  verification comes from the log with inclusion + consistency
  proofs, and even a module's own author can't silently retag a
  version. **Verified 3-0.**

The TOFU point is the sharpest single correction to the first
survey, which treated "lockfile with hashes" as the reproducibility
endgame. It is the endgame for *repeat* builds; the *first*
resolution of any dep is unauthenticated without either a
transparency log or an out-of-band hash (see Roc/Zig below for the
zero-infrastructure alternative).

### Pole 2 — JSR: the registry that understands the language

JSR (March 2024, now independently governed) is the reference for a
*purpose-built* registry for a typed language. Three moves matter
for Fern:

- **Constrain the format**: TypeScript-first, ES-modules-only, yet
  runtime-agnostic; registry software is MIT-licensed open source.
- **Move build steps author→registry**: authors publish plain TS
  source; the registry does transpilation, `.d.ts` generation, and
  **API-doc generation** server-side. (The registry-hosted-docs
  idea the first survey credited to Hex, taken further.)
- **Security default-on, not opt-in**: tokenless OIDC publishing
  from CI (no long-lived publish token to steal) + SLSA provenance
  attestations recorded in Sigstore's Rekor transparency log, with
  zero configuration (`--no-provenance` is the opt-*out*).
  **Verified 3-0.**

A Fern registry, if one ever exists, could do the same trick
better: the registry runs `fern -check`, `ferndoc`, and (below)
API-diff semver verification on publish, because it owns the
compiler.

### What the nearest peers shipped (corroborated, not 3-vote-verified)

- **Zig**: no registry. `build.zig.zon` declares deps as
  `url + hash`, where the docs are explicit that **the hash is the
  source of truth**: "packages do not come from a url; packages
  come from a hash. url is just one of many possible mirrors."
  Multihash format, sha256-based, computed over the *unpacked file
  tree* (not the tarball bytes), so re-compressed mirrors still
  match.
- **Roc**: no package manager at all (deliberately, for now).
  Packages are imported by URL with a base64-encoded BLAKE3 hash
  embedded *in the URL itself*; the CLI verifies content against
  the hash and caches in `~/.cache/roc`. Their stated rationale is
  the same TOFU-closing property: the URL's content can't change
  behind you, an expired domain can't serve you something else, and
  no re-check is ever needed because the identifier *is* the
  content address.
- **Gleam**: the opposite bet — **piggyback on an existing
  ecosystem's infrastructure**. Gleam is a BEAM language and uses
  Hex.pm wholesale (`gleam.toml` deps, `gleam add foo@4`, rebar3/
  mix packages importable directly), plus its own thin discovery
  index (packages.gleam.run). Zero registry-operation cost; the
  price is inheriting Hex's policies and Erlang-shaped packaging.
- **MoonBit**: a centralized first-party registry (mooncakes.io),
  `moon.mod.json` manifest, per-user namespacing
  (`user/package`), SemVer 2.0.0, and registry-generated docs —
  the JSR shape at smaller scale, viable because MoonBit has a
  funded core team.

The pattern across all four: **nobody small runs Go/npm-style
infrastructure.** The choice is (a) hash-addressed URLs with no
server at all (Zig, Roc), (b) rent someone else's registry
(Gleam), or (c) run a small registry only if there's an
organization behind the language (MoonBit, JSR).

## Storage: content-addressed stores in practice

pnpm proves the mechanism at npm scale: every file of every
installed package is a hard link (or copy-on-write reflink on
APFS/Btrfs/XFS) into one per-machine content-addressable store —
stored once, shared across all projects. **Verified 3-0.** This
confirms the first survey's Rec §4 (flat `~/.cache/…/<hash>/`
store).

**Refuted claim, worth recording**: "pnpm's symlinked layout
enforces dependency strictness / prevents phantom-dependency
access" was killed 0-3 in verification. Layout does not give
isolation — undeclared-dependency access must be blocked by the
*loader/resolver*, not the directory shape. For Fern this is
actually good news: `modload` already resolves imports from an
explicit graph, so phantom dependencies are structurally impossible
as long as a package's import resolution is restricted to its own
declared dep set (the natural design). Don't rely on cache layout
for it; enforce it in `resolveImportPath`'s package-aware
successor.

Fern-specific synergy (new since the first survey): #3458's
incremental-codegen cache is keyed by content hashes of per-module
compilation inputs + imported-signature hashes. A package cache and
the object cache should be two levels of the same content-addressed
store: immutable package sources hash once at fetch time, and that
hash feeds directly into the object-cache key. One hashing
substrate, two consumers.

## API evolution: enforced semver vs opt-in linting

Two shipping models:

- **Elm — compiler-enforced at publish.** Because the language is
  statically typed and the registry runs the compiler, `elm bump`
  *computes* the next version from the API diff and `elm publish`
  refuses a version number inconsistent with the diff (`elm diff`
  shows the change classification). Caveat (elm-lang.org#868): the
  enforcement is over *type signatures*; behavioral breaking
  changes under an unchanged signature slip through, so "enforced
  semver" overpromises slightly and Elm's own docs were corrected
  on this.
- **Rust — opt-in pre-release linting.** crates.io does *not*
  enforce semver at publish (the request, crates.io#435, has been
  open for years); the ecosystem converged on cargo-semver-checks,
  which lints API changes by querying **rustdoc's JSON output**
  via Trustfall — i.e. it layers on compiler-emitted API metadata,
  entirely outside the package manager and registry
  (cargo-publish integration is a stated goal, not merged as of
  2026). Its documented maintenance cost is exactly the unstable
  rustdoc-JSON format. **Verified 3-0.**

Design lesson for Fern: semver checking is cheap **iff the compiler
emits a stable API-summary artifact** (exported signatures per
module — which the per-module epic's signature side-tables, #3454,
already compute for codegen purposes!). The Elm experience says
compiler-computed version bumps are viable and loved when the
language controls the whole pipeline; the Rust experience says the
tooling can come later and still work, but bolting it onto an
unstable metadata format is the tax. Recommendation delta below.

## Supply-chain security: what 2024–2026 taught

The npm attack wave (corroborated timeline; not 3-vote-verified):
the Sept 2025 **Shai-Hulud worm** was the first self-propagating
npm attack — a `postinstall` script harvested credentials and used
any npm tokens it found to republish malicious versions of every
package the victim could publish, spreading exponentially;
"Shai-Hulud 2.0" (Nov 2025) moved to `preinstall` and destroyed
home directories on credential-theft failure; tens of thousands of
repos were affected. The ecosystem response: npm revoked all
classic long-lived tokens (Dec 2025–Feb 2026), capped new
write-token lifetimes at 7–90 days, shipped **trusted publishing**
(OIDC, the OpenSSF standard already live on PyPI/RubyGems/JSR), and
is adding staged publishing with MFA-verified approval windows.

What generalizes, ranked by relevance to Fern:

1. **The attack ran at install time via lifecycle scripts.** Fern's
   standing constraint (no install/build-time code execution) is
   not a purity preference; it removes the propagation mechanism of
   the worst attack class outright. Keep it absolute.
2. **Long-lived publish credentials are the theft target.** Any
   future Fern publish flow should be OIDC/trusted-publishing-first
   from day one — the standard exists now (OpenSSF), which it
   didn't when the first survey was written.
3. **Provenance is cheap now.** Sigstore/Rekor + SLSA attestations
   are default-on at JSR and free for GitHub-hosted projects. A
   registry-less Fern ecosystem gets most of the benefit from
   hash-in-import (Roc/Zig style); a registry-ful one should copy
   JSR's default-on posture.
4. **Capability-based sandboxing of dependencies remains
   unshipped** in every mainstream package manager (verification
   found no shipping implementation). For Fern this is a
   *language*-level opportunity, not a package-manager feature:
   the platform capability bag (#4414) already scopes what a
   *program* can reach; per-dependency capability attenuation
   ("this JWT library gets no filesystem, no subprocess") would be
   a genuine differentiator on the WASM/WASI target, where the
   component model enforces it structurally. Track as research,
   not as a v1 requirement.

## Trade-off space for Fern, concretely

Collapsing the above into the decisions that actually interact:

| Decision | Options | Evidence-backed pick |
|---|---|---|
| Constraint language | ranges + PubGrub / minimums-only + MVS | **MVS**: no solver, no lockfile-for-versions, deterministic; `std/semver` compare is already sufficient machinery. Add top-level-only `exclude` for the "1.9 is broken" case. |
| Major versions | coordinate (Cargo) / import path (Go) | **Import path** (`foo/v2`) — composes with MVS, makes coexistence explicit. (Unchanged from first survey.) |
| Identity | name@version / url / **hash** | **Hash is the source of truth; URL is a mirror hint** (Zig's exact wording, Roc's URL-embedded BLAKE3). Manifest maps a short name → (url, hash); lockfile records hashes for transitives. |
| First-fetch trust | TOFU lockfile / transparency log / hash-in-manifest | **Hash-in-manifest** closes TOFU with zero infrastructure. A transparency log is the *upgrade path* if an ecosystem forms (piggybacking on Sigstore's public Rekor instance is the cheap federated option — open question). |
| Registry | none / rent (Gleam-on-Hex) / own (MoonBit/JSR) | **None** for now (curated index per first-survey Rec §9). If one ever exists: JSR shape — registry runs the compiler (check, docs, API-diff) and is OIDC/provenance-default-on. |
| Storage | per-project trees / global CAS | **Global content-addressed store** (pnpm-proven), *shared with the #3458 object cache*. Isolation enforced in the resolver, never by layout (refuted claim). |
| Semver posture | none / opt-in lint / enforced at publish | **Compiler-emitted API summary + opt-in `fern semver-check` first** (Rust path, but on a *stable* format — avoid the rustdoc-JSON tax); graduate to Elm-style computed bumps if/when a registry runs the compiler. Signature side-tables from #3454 are the seed artifact. |
| Install-time execution | scripts / none | **None**, unchanged — now with Shai-Hulud as the case study rather than just `event-stream`. |
| Dep capabilities | ambient / per-dep attenuation | Research track riding #4414 + the WASI component model; no shipping precedent anywhere — potential differentiator, not a v1 blocker. |

## Delta to MODULE-PACKAGES-RESEARCH.md recommendations

The first survey's Recs §1–§12 survive contact with the verified
evidence. Adjustments:

- **Rec §2 (lockfile) — reframe.** Under MVS the lockfile is a
  *hash store*, not a version store; versions are fully determined
  by manifests. Simplifies the format (no resolution snapshot to
  keep coherent) and makes "libraries don't check it in" moot for
  version skew.
- **Rec §3 (MVS) — upgraded from "opinionated pick" to
  "evidence-backed."** The NP-completeness result plus the
  PubGrub-cost observation make MVS the only choice that avoids
  both a solver project and conflict-explanation UX debt. Add:
  top-level `exclude`.
- **Rec §4 (content-addressed cache) — merge with #3458.** One
  content-addressed substrate for package sources and per-module
  objects. New, and the single most actionable item because the
  object-cache side is already scheduled work.
- **Rec §9 (no registry, curated index) — confirmed by peer
  evidence** (Zig/Roc ship registry-less; Gleam rents; only
  funded teams run registries). Add the Roc/Zig refinement:
  hash-in-manifest as the trust root, URL as mirror hint.
- **Rec §12 (signing) — modernize.** "Sign with publisher keys"
  (Hex model) is no longer the state of the art; OIDC trusted
  publishing + Sigstore provenance is, and it's what every major
  registry converged on post-2025. When publishing exists, start
  there.
- **New rec: API-summary artifact.** Emit a stable, versioned
  exported-API summary per module from the compiler (the #3454
  signature analysis, serialized). It's the foundation for
  `fern semver-check` (Rust lesson), eventual Elm-style computed
  bumps, and #3458's cross-module invalidation — three consumers,
  one artifact.
- **New rec (research track): per-dependency capability
  attenuation** on the WASI component target, building on #4414.
  No shipping precedent in any package manager; genuine
  differentiation potential.

## Open questions (carried forward)

1. Operational/governance cost of a transparency log for a
   one-maintainer ecosystem — is publishing checksums to the
   public Sigstore Rekor instance a viable federated substitute
   for running one?
2. Elm's enforced-semver failure modes in practice (behavioral
   changes under stable signatures; ecosystem chafing) — how much
   of the enforcement belongs in the compiler vs the registry?
3. Can per-dependency capability attenuation be made ergonomic
   enough to be a default rather than an expert feature, and does
   the native (non-WASI) target get a meaningful approximation?
4. Whether the ~512-function merged-bundle budget (#3425) or the
   per-module epic (#3451) lands first materially changes when a
   multi-package workspace (first-survey Rec §5) becomes exercisable
   for the self-hosted compiler itself.

## Related issues

- #3451 [Epic] Per-module compilation + linking — the build-system
  substrate a package cache plugs into.
- #3458 Per-module step 6: incremental codegen — content-hash keyed
  per-module object cache + signature invalidation; share the
  hashing substrate with the package cache.
- #3454 Per-module step 2 (closed) — the whole-program signature
  side-tables that seed the API-summary artifact.
- #3457 Per-module step 5: retire the legacy AST emitters.
- #3095 `pub(package)` visibility (closed) — the package-boundary
  visibility level.
- #3091 Trait, type & module ergonomics: lessons from Rust (closed).
- #4886 `std/semver` (closed) — the version-ordering kernel; MVS
  needs nothing more from it.
- #4385 stdlib gaps for the CLI + edge use cases — the boundary
  between "stdlib covers it" and "first third-party package".
- #4414 platform/edge capability bag — the language-level
  capability surface that per-dependency attenuation would build
  on.
- #4451 native-convergence freeze tracker — a package manager is
  new surface; the implementation should be weighed
  self-host-first per the convergence policy.
- #3425 self-host IR-emit live set — gates when the self-hosted
  compiler itself can become a multi-package workspace.

## Sources (primary, for the verified core)

- research.swtch.com/version-sat, research.swtch.com/vgo-mvs, and
  the Go proposal docs 24301 (versioned Go) + 25530 (sumdb) — Cox.
- github.com/dart-lang/pub `doc/solver.md` — Weizenbaum's PubGrub
  design doc; pubgrub-rs internals guide.
- go.dev/blog/module-mirror-launch; proxy.golang.org;
  words.filippo.io/gosum.
- pnpm.io docs (symlinked-node-modules-structure, motivation, faq).
- deno.com/blog/jsr_open_beta; jsr.io/docs/trust; github.com/jsr-io/jsr.
- crates.io/crates/cargo-semver-checks;
  github.com/obi1kenobi/cargo-semver-checks;
  rust-lang/crates.io#435.
- Corroborating (unverified tier): ziglang build.zig.zon docs,
  roc-lang.org/faq, gleam.run + hex.pm/docs/gleam-usage,
  moonbitlang.com/blog/intro-to-mooncakes, docs.npmjs.com
  trusted-publishers, Wiz/Unit42 Shai-Hulud analyses,
  elm-lang.org#868.

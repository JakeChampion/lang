# Reading notes, 2026-10-21

Status: [research] seven outside sources read on 2026-10-21, with the decision each led to.

This study names historical or proposed source paths. The reference documentation identifies current files.

Seven outside sources, read against the code as it stands, and what each one
changes for Fern. One of them became a change in the same PR (the change-risk
lint rule, `docs/LINT.md`); the rest are recorded here as a decision, with the
facts the decision rests on, so nobody has to re-derive them.

| Source | Verdict |
|---|---|
| Savoia, "This code is CRAP" (Google Testing Blog, 2011) | **Done:** `change-risk` lint rule from a `-cover` run |
| Pierre, "On highlighting code" (Highlights, a WAT lexer-highlighter) | **Cannot adopt:** Highlights has no Fern lexer and no way to add one from outside the package |
| packslip.dev (signed release metadata, by the authors of mise and aqua) | **Done:** the nightly release signs a packslip for the `fern` archives |
| ASD-STE100 Simplified Technical English (0xpili's skill) | **Done:** the skill's MIT text is vendored and `CLAUDE.md` makes STE the rule for prose |
| MultiTable (Petkus, 2026) | **Measured** (`bench/maps/`): not the lever; Fern's `Map` loses ten times to hashbrown on the same layout class, so the cost is the runtime path, not the table |
| rsconstmap (Lemire; binary fuse static maps) | **Done:** `std/constmap`, measured against `Map` and the Rust crate in `bench/maps/` |
| usage.jdx.dev (a portable CLI spec in KDL) | **Done:** `std/cli` emits a usage spec from the same `CliSpec` as its man page and completions |
| "Principles for fast Tokio applications" (dial9) | No change without a measurement; two things worth measuring |

## CRAP: complexity against coverage

The post's content is one formula and one threshold. CRAP1(m) =
comp(m)² × (1 − cov(m))³ + comp(m), with comp the cyclomatic complexity and cov
the coverage from automated tests; over 30 is the anti-pattern. Savoia says
plainly that it is a curve fit to colleagues' judgements of real Java code,
not a derivation, and that it does nothing for cohesion or coupling.

Fern already measured both halves and reported them in different places:
`fern -lint` scores cyclomatic complexity per function (`compiler/lint.fern`),
and `fern -cover` writes a row per instrumented line (`compiler/cover.fern`,
folded by `compiler/coverreport.fern`). The repository's own complexity
ratchet (`docs/COMPLEXITY-CAMPAIGN.md`) has been working the first half alone,
and a complexity number cannot say whether a 400-fork function is dangerous or
merely large. Joining the two is a second lint rule, which `docs/LINT.md`
documents and `-lint -lint-cover FILE` runs. Line coverage stands in for the
post's "basis path" coverage, as every port of the metric does; Fern's branch
rows exist only for functions with conditionals, so lines are the denominator
that is dense for every function.

## Highlights: a lexer, not a grammar, drives the colours

The article's result is a hand-written-WAT lexer that emits HTML or token
records directly, scanning 16 bytes at a time for strings and comments, with
a streaming mode that keeps lexer state across chunks and re-tokenizes only
the lines an edit touched. The numbers (up to 298× Shiki's throughput, 50 MiB
against 155 MiB) are the vendor's, on one machine.

Where Fern stands: the documentation site highlights ```fern fences through
Starlight, which runs Shiki over the VS Code extension's TextMate grammar
(`editors/vscode/syntaxes/fern.tmLanguage.json`, loaded in
`site/astro.config.mjs` so the two stay one file). That is a build-time cost
on a static site, so the article's speed argument does not apply. The
playground (`web/index.html`) mounts CodeMirror 6. The language server emits
semantic tokens from the real lexer and parser (`compiler/lsptokens.fern`).

Asked to replace Shiki with Highlights, the answer is no, for a reason in the
package rather than a preference: its lexers are hand-written WebAssembly text
in `src/langs/*.wat`, registered in a table inside the package and compiled by
its build, and a TextMate grammar cannot be loaded into it. Fern is not among
its languages, so the site's Fern fences would render as plain text. Adopting
it would mean contributing a Fern lexer upstream in WAT and maintaining it
there, a second lexer beside `compiler/lexer.fern` in a language nobody here
writes.

What does apply is the article's premise: a grammar maintained beside a lexer
drifts from it. Fern's keyword set is written four times: `is_keyword` and
`keywords()` in `compiler/lexer.fern`, the `keywords` map in
`internal/syntax/lexer/lexer.go`, and the grammar's four keyword scopes. The
grammar, the Go map and `keywords()` were already held to one set by
`TestGrammarHighlightsExactlyTheKeywords`, which reads the grammar and the
Fern source as data. The pair nothing pinned was inside the compiler,
`is_keyword` against the `keywords()` list it is documented to mirror, and
`TestFernLexerKeywordTableMatchesItsList` now pins it the same way. The
expensive change, a `fern -highlight` mode emitting spans from `lexer.fern`
for the site and the playground's WASI build, would make the grammar a derived
artefact; it is worth doing only when the playground wants live highlighting,
and is not scheduled.

## packslip: one signed file per release

packslip is a format and CLI, stable at version 1: a release publishes
`packslip.sigstore.json` beside its artifacts, recording each file's digest,
platform (`os`, `arch`, `libc`) and executable paths, signed keylessly from a
CI OIDC identity or with an Ed25519 key, as an in-toto statement in a
sigstore bundle. The point it makes against the usual `.sha256` sidecar is
that whoever can replace the binary can replace the checksum beside it;
verification against a trusted signer is what the sidecar lacks. It does not
by itself detect a withdrawn or rolled-back release; separately signed
release lists do that.

Where Fern stands: `release.yml` publishes a rolling `nightly` of `fern` and
the coreutils per host as `.tar.gz` plus a `.tar.gz.sha256` sidecar, exactly
the shape the format criticises. The bootstrap pin is the other way round and
sound: `bootstrap/stage0.lock` carries the sha256 of each stage0 binary in the
repository, so a `make bootstrap` verifies against a hash the vendor cannot
rewrite after the fact (the "hash-in-manifest closes TOFU" posture of
`docs/PACKAGE-MANAGEMENT-SOTA.md`); package fetches (`compiler/pkgfetch.fern`)
verify the same way. The toolchain here is already managed by mise
(`mise.toml`), whose author co-wrote packslip and whose installer consumes it.

Done: the nightly `release.yml` runs `jdx/packslip@v1` after the release is
published, over the three `fern` archives, signing keylessly with the
workflow's OIDC identity (`id-token: write`) and attesting each archive, and
keeps the `.sha256` files for checking by hand. The version is
`0.0.0-nightly.<run number>`, since the rolling tag names no semver. The
step's CLI (`packslip create`, version 1.10.1) was run here against archives
named as the release names them: it infers `linux/x86_64/gnu`,
`linux/aarch64/gnu` and `darwin/aarch64`, finds `fern` in each, and writes
`packslip.sigstore.json`. What a container cannot exercise is the keyless
signing itself; the first scheduled run is the test of that. The coreutils
archives are a second tool and want a second packslip
(`github.com/JakeChampion/lang/fern-coreutils`); a signed release list is the
follow-on once the nightly has more than one version worth distinguishing.

## Simplified Technical English

ASD-STE100 is a controlled language for maintenance documentation: 53 writing
rules (one instruction per sentence, about twenty words, active voice, no
contractions, a paragraph of at most six sentences) and an 869-word dictionary
in which each word has one approved meaning. The repository packages it as an
agent skill with a checker script.

Where Fern stands: `CLAUDE.md` ("Write plainly") and `docs/AGENT-PROMPTING.md`
("mannered prose") asked for the same thing less mechanically. STE is now the
rule: the skill's MIT-licensed text (the 53 rules, the substitutions, the
examples and the checker) is vendored at
`.claude/skills/simplified-technical-english/`, and `CLAUDE.md` names it. The
dictionary is not vendored, because it is ASD's property; STE permits
technical names beside its approved words, and this project's identifiers are
technical names, so the rules apply without it. The checker runs with
`--no-vocab`.

## MultiTable

A cascading multi-level hash table: a key hashes to one bucket per level and
settles in the first with room, trading extra probes for a physical load
factor up to 1. Fixed-size byte keys and values, no resize, no iteration, and
the README calls it not production-grade; the paper's claim is less memory
than hashbrown at any load factor.

Where Fern stands: `internal/stdlib/core/map.fern` is a SwissTable-style open
table: power-of-two bucket count, a 7-bit control byte per slot scanned eight
at a time with the SWAR zero-byte finder, tombstones, a 75% load-factor grow,
runtime key-kind tags.

Measured (`bench/maps/README.md`): on the `bench/map_int` workload MultiTable
looks up 1.2x to 1.7x slower than hashbrown at 4 000 to 1 000 000 keys, the
trade its paper states, and Fern's `Map` is 7x to 15x slower than hashbrown
on the same layout class. The lever is the runtime path of a lookup
(`docs/MAP-SPECIALIZATION.md`), not the table; MultiTable is not adopted.
Finding the numbers also found #12927, a quadratic copy of a value built in a
loop and then captured by an escaping closure.

## rsconstmap: static string maps from binary fuse filters

An immutable string-to-u64 map built as a binary fuse filter: for every key,
`data[h0] ^ data[h1] ^ data[h2] == value`, so a lookup is one hash, three
loads and two XORs, at about 9 bytes per key (18 with a fingerprint so absent
keys are detected). The author's numbers put it at 7 ns against 55 ns for a
`HashMap` at a million keys on an M4 Max; at ten thousand keys everything is
in cache and the gap is small. Build is slower than a hash map's and the
structure is immutable, so it fits sets known at construction.

Where Fern has static string sets:

- `std/publicsuffix` holds 6,949 rules as one 78 KB string and answers
  `__rule(r)` with `contains("\n" + r + "\n")`: a substring scan of the whole
  list per label, up to a few labels per host. This is the clearest case for a
  built-once structure. A sorted array with binary search is the smallest
  change; a binary fuse set is the fast one and would be a `std` module in its
  own right. Measure against the string scan before choosing.
Done: `std/constmap` is the Fern version, a binary fuse structure with the
values in place of the fingerprints, in `ConstMap` and `VerifiedConstMap`
forms, tested in `tests/stdlib/constmap_test.fern`. Measured against `Map`
and the Rust crate in `bench/maps/README.md`: in Rust the structure ties
hashbrown with FxHash and halves the SipHash map's cost; in Fern it ties or
beats `Map[string, u64]` by up to a third, and the hash is most of a lookup,
which puts the next win in the `xxh64` kernel the compiler emits rather than
in the structure. The two call sites below stand:

- `match` on string literals lowers to an `str_eq` if-else-if chain
  (`docs/FEATURE-AUDIT.md`). For the arm counts programs write, a hash would
  lose to comparing lengths first; a length-then-bytes dispatch in the
  lowering is the win there, and belongs in the IR so every backend gets it.
- The lexer's `is_keyword` already dispatches on the first byte and compares
  a handful of candidates, which is the right shape for forty keys.

## Fast Tokio applications

The principles that transfer to a reactor-driven HTTP server: measure a real
latency metric before changing anything, and the metric that matters is the
time from a task becoming ready to its being polled; yield between pipelined
requests for latency and batch for throughput (yielding after four
consecutive ready reads took a mini-Redis p99 from 2.5 ms to 0.3 ms at the
same throughput); bound concurrency with a semaphore rather than an adaptive
scheme; keep critical sections tiny and never hold a lock across I/O; isolate
the event loop's threads from other work, because an OS scheduling delay of
10 to 20 ms under load is the whole p99 budget.

Where Fern stands (`internal/stdlib/std/serve.fern`): each worker is one
single-threaded reactor loop, so the mutex and global-queue advice has no
object; concurrency is bounded already (`max_connections`,
`max_connections_per_ip`, `max_in_flight`, per-request limits); workers are
preforked (`workers`), and nothing pins them to cores. The one place the
article's first principle bites is step 6 of the loop's contract: requests
pipelined behind an answered one are served from the same readable event and
their responses corked into one write. The loop's single 4 KiB read buffer
bounds how many that can be, so one connection cannot starve the others
beyond a few dozen tiny requests, but the article's point is that it still
costs the other connections' p99. Two things are worth measuring before
anything is changed, and `tools/fernload` (open-loop, corrected for
coordinated omission) is the instrument: a cap on requests answered per
readable event, and a "ready to served" latency histogram in
`std/metrics.ServerMetrics` beside the request-duration one, which is the
schedule-latency metric the article says to read first. No change here.

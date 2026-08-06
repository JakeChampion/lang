# The Fern conformance corpus

Status: normative. This directory defines observable Fern behaviour by
example. A change to a case's expected output is a change to the
language, and should be reviewed as one.

`cases/` holds 361 self-contained programs, each a directory with the
program plus a few declarative sidecar files describing what running it
must produce. Every Fern implementation is measured against it:
`internal/interp`, the three native backends (x86-64, arm64, wasm), and
the self-hosted compiler's own three emitters.

## Why this lives outside `internal/`

It used to be `internal/e2e/testdata/cases` — inside one implementation,
which made it look like that implementation's test suite rather than the
language's definition. That framing is about to stop being harmless.

Today the specification of Fern is "whatever `internal/` does", and that
works: when the self-host wasm leg disagrees with native, native is right
by definition. `docs/NATIVE-CONVERGENCE.md` plans to end it — after the
freeze preconditions (#4451) go green, new surface lands self-host-first
and native stops being the oracle. This corpus is what has to carry the
definition across that transition, so it is positioned as a peer of the
implementations rather than as a component of one.

See `docs/SPECIFICATION-RESEARCH.md` for the wider argument and for the
staged shape this is the first slice of.

## What is NOT here

**Per-implementation expectation files stay with their implementation.**
The `selfhost-*-known-divergences.txt` files remain in
`internal/e2e/testdata/` deliberately: a divergence list records where
one implementation currently falls short of the corpus, which is a fact
about that implementation, not about the language. This follows test262,
where the suite is shared and each engine keeps its own expectations.

## Case format

A case is a directory `cases/<name>/`. Contents:

| File | Required | Meaning |
| --- | --- | --- |
| `main.fern` | yes | Entry point, defining `main(): i32`. May import sibling `.fern` files (`import "./helper";`) and stdlib modules. |
| `*.fern` | no | Sibling modules imported by `main.fern`. |
| `expected.stdout` | no | Expected stdout. Byte-for-byte in the default `exact` mode; a list of required substrings, one per line, in `contains` mode. Defaults to empty. |
| `expected.exit` | no | Expected process exit code, `0`–`255`. Defaults to `0`. |
| `stdin` | no | Bytes fed to the program's stdin. |
| `match` | no | `exact` (default) or `contains`. |
| `backends` | no | Whitespace-separated subset of `interp x86_64 arm64 wasm`; `#` starts a comment. Defaults to all four. |
| `expected.error` | no | Marks a **compile-error case** — see below. |

No other filenames are permitted, and case directories have no
subdirectories. Both are enforced (`TestConformanceCorpusFormat`),
because an unrecognised sidecar is otherwise read as an absent one: a
`expected.exitcode` typo silently asserts exit `0` rather than failing.

### Compile-error cases

A case containing `expected.error` is **not run**. It must fail the
front-end — parse, module load, or type check — and the captured error
must contain the trimmed contents of `expected.error`. This gives
declarative coverage of the rejection paths behind the `E0NN` codes.

Such a case must carry none of `expected.stdout`, `expected.exit`,
`stdin`, `match` or `backends`; those are ignored on this path, and a
case that sets one is asserting something that is not being checked.

### The wasm exit-code convention

Native and interp backends propagate `main`'s return value straight to
the process exit code (full `0..255`). A preview-2 wasm host only
surfaces 0/1 through `wasi:cli/exit`, so the wasm leg builds with
`PrintMainResult` and recovers the value from a trailing result line
appended to stdout. Two consequences for a case that opts into wasm:
`main` must return `i32` (not void), and `int_to_string` must be
reachable so the line can be formatted — i.e. an exact-match case
targeting wasm must `import "core/int";`, or drop `wasm` from its
`backends` file.

## Adding a case

Drop in a directory. No Go code is required, and nothing needs
registering — the runners discover directories.

Prefer `match: exact`. A `contains` case asserts less, and the gap
between what it asserts and what the language guarantees is exactly the
kind of unspecified behaviour this corpus exists to pin down. If a
case's output genuinely cannot be exact — because the language
deliberately leaves something free, as `docs/FLOAT-SEMANTICS.md` does
for NaN bit-patterns — say so in a comment in `main.fern`, so that the
freedom is a decision on the record rather than a weaker assertion
nobody revisits.

Same for `backends`: a subset means "this behaviour is not portable",
which is a claim about the language. Use it when that is true, and file
an issue when it is merely not implemented yet.

## Runners

| Runner | What it does |
| --- | --- |
| `TestFernFixtures` (`internal/e2e`) | Every case across every backend it opts into. The primary gate. |
| `TestFernFixturesSelfHost{Wasm,X86_64,Arm64}` (`internal/e2e`) | The same cases through the self-host compiler, against per-target divergence lists. Env-gated by `FERN_SELFHOST_FIXTURES=1`. |
| `TestConformanceCorpusFormat` (`internal/e2e`) | Validates this document's format rules. Fast; no compilation. |
| `forEachRunnableFixture` (`internal/e2e/rc_freelist_test.go`) | Re-runs the corpus with the rc freelist flag off and on. |
| `scripts/selfhost-emit-hashes` | Hashes self-host emit output per case, for byte-identity comparison. |

The corpus root is a single constant, `e2eharness.ConformanceCases`.

# The Fern conformance corpus

Status: normative. This directory defines observable Fern behaviour by
example. A change to a case's expected output is a change to the
language, and should be reviewed as one.

`cases/` holds 485 self-contained programs, each a directory with the
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
| `expected.error` | no | Marks a **compile-error case** — the front end must reject it. See below. |
| `expected.lowering-error` | no | Marks a **lowering-error case** — the front end must ACCEPT it and lowering must reject it. See below. |
| `meta` | no | Justifies a case that asserts less than the maximum — see below. Required exactly when the case does. |
| `reclaim-observable` | no | Marks a case whose output deliberately DIFFERS with reclamation off, so the free-off gates invert for it rather than requiring a match. Contents are prose explaining why; only the file's presence is read. |

No other filenames are permitted, and case directories have no
subdirectories. Both are enforced (`TestConformanceCorpusFormat`),
because an unrecognised sidecar is otherwise read as an absent one: a
`expected.exitcode` typo silently asserts exit `0` rather than failing.

A `backends` file that names all four backends is rejected: that is
already what omitting the file means, so the list says nothing and only
its comment carries information — and a comment belongs in `main.fern`
with the rest of the case's rationale. The rule keeps "has a `backends`
file" readable as "is restricted".

### Compile-error cases

A case containing `expected.error` is **not run**. It must fail the
front-end — parse, module load, or type check — and the captured error
must contain the trimmed contents of `expected.error`. This gives
declarative coverage of the rejection paths behind the `E0NN` codes.

Such a case must carry none of `expected.stdout`, `expected.exit`,
`stdin`, `match` or `backends`; those are ignored on this path, and a
case that sets one is asserting something that is not being checked.
It must not carry a `meta` waiver either: a waiver justifies asserting
less than byte-exact output on all four backends, and a rejection case
asserts no output at all.

### Lowering-error cases

Not every rejection is a front-end rejection. `E068` — a `fbip`
function that allocates without a donor to reuse — is reported by
`internal/ir/fip_verify.go` during lowering, after the checker has
already accepted the program, so an `expected.error` case can never
reach it: that path stops at the type check.

A case carrying `expected.lowering-error` asserts **both** halves. The
front end must accept the program, and `ir.LowerWith` must then reject
it with an error containing the file's trimmed contents. Asserting only
the second half would let a program the checker already rejects
masquerade as a lowering rule, which is the confusion the separate
sidecar exists to prevent — the runner reports that case by name and
says to move it to `expected.error`.

It runs at **both pointer widths**. A lowering rejection that fires on
one target and not the other is a portability defect in its own right,
so a case that holds at only one width fails rather than passing
quietly.

The same exclusivity rules apply as for `expected.error`: no run
sidecars, and a case cannot carry both files — which stage rejects a
program is precisely what these cases pin, and it cannot be two.

### A program that aborts cannot opt into wasm

The same convention makes an *aborting* case unobservable on the wasm
leg: a trapping program never reaches the trailing result line, so the
runner sees empty stdout rather than an exit code, and the case fails
whatever `expected.exit` says. The abort family (`oob_index_read` and
its siblings) therefore restricts `backends` to the natives and carries
a `harness-limit` waiver. This is a property of how the exit code is
recovered, not of the wasm backend, which traps exactly as intended.

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

## Justifying a weaker assertion

The strongest thing a case can say is "this exact stdout, on all four
backends". There are two ways to say less — `match: contains`, and a
`backends` subset — and both are claims about the language, so a case
that uses either **must** carry a `meta` file saying why. A case that
uses neither must **not** carry one. Both directions are enforced.

`meta` is `key: value` lines; `#` starts a comment, and a line indented
relative to its key continues that key's value.

| Key | Meaning |
| --- | --- |
| `waiver` | Why the case asserts less. One of the four kinds below. Required. |
| `reason` | Prose. Required — a waiver with no stated reason is indistinguishable from an oversight. |
| `issue` | Bare issue number, no `#`. Required for `implementation-gap`, rejected for the others. |

The four kinds exist because they call for completely different
follow-up, and collapsing them is how a gap goes missing:

| `waiver` | Means | Follow-up |
| --- | --- | --- |
| `implementation-gap` | A backend has not implemented this yet. | Tracked by `issue`. The case should widen when it lands. |
| `harness-limit` | The runner cannot *observe* the behaviour on that backend. | None — not a language or backend defect. |
| `unspecified` | The language deliberately grants the freedom, as `docs/FLOAT-SEMANTICS.md` does for NaN bit-patterns. | None; cite the doc in `reason`. |
| `harness-self-test` | The case exercises the runner, not the language. | None. |

```
waiver: implementation-gap
issue: 2843
reason: the native wasm backend has no sleep_ms (it needs a WASI
  poll-based sleep). monotonic_ns already works there.
```

The rule matters in both directions. An unjustified weakening hides a
gap behind what looks like a passing case. A *stale* waiver is worse: it
is how an obsolete exclusion survives for years. `f64_sqrt` sat opted
out of arm64 and x86-64 with "sqrt needs libm; the `-nostdlib` native
backends can't link it" long after `sqrt` began lowering to a hardware
instruction with nothing to link — two backends of real coverage,
silently absent. It also matched on a substring "to stay robust to
trailing fractional digits" that the output does not have. Requiring the
waiver to be deleted the moment the case stops weakening is what turns
that from archaeology into a test failure.

## Adding a case

Drop in a directory. No Go code is required, and nothing needs
registering — the runners discover directories.

Prefer the strongest assertion the behaviour supports: byte-exact stdout
on all four backends, no `meta`. Reach for a waiver when that is
genuinely not available, not when it is inconvenient — the gap between
what a case asserts and what the language guarantees is exactly the kind
of unspecified behaviour this corpus exists to pin down.

## Runners

| Runner | What it does |
| --- | --- |
| `TestFernFixtures` (`internal/e2e`) | Every case across every backend it opts into. The primary gate. |
| `TestFernFixturesSelfHost{Wasm,X86_64,Arm64}` (`internal/e2e`) | The same cases through the self-host compiler, against per-target divergence lists. Env-gated by `FERN_SELFHOST_FIXTURES=1`. |
| `TestConformanceCorpusFormat` (`internal/e2e`) | Validates this document's format rules. Fast; no compilation. |
| `forEachRunnableFixture` (`internal/e2e/rc_freelist_test.go`) | Re-runs the corpus with the rc freelist flag off and on. |
| `scripts/selfhost-emit-hashes` | Hashes self-host emit output per case, for byte-identity comparison. |

The corpus root is a single constant, `e2eharness.ConformanceCases`.

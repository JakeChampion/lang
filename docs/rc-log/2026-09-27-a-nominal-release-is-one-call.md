# A nominal release is one call, and the inference's rows are indexed

2026-09-27 — `ssarc.drop_value`, `ssarc.drop_helpers`, `ownership.Index`.
Refs #8171, #9415, #10352, #4451.

## Where the typed path's compile time went

The typed lowering became the default between #8171's last profile and this
one, and it is most of what that issue's numbers moved by. On a 4-core x86-64
container, the native-built `bin/fern-selfhost` compiling `checker.fern` to a
binary:

| | wall | emitted asm lines |
| --- | ---: | ---: |
| `FERN_SEM_IR=` (AST lowering) | 6.85 s | 495,822 |
| default (typed lowering) | 15.6 s | 725,771 |

callgrind puts the whole compile at 110 G Ir, against the 26 G #8171 recorded
before the flip. `semlower.substitution` is 47% of it and the assembler 14%.

Two things were cheap to take.

## 1. A nominal release was twenty lines at every site

`drop_value` expanded every release inline: `__fern_rc_is_unique`, an `if`, the
`__sem_drop_<T>` call for the children, then the rc-guarded box dec. On x86-64
that is about twenty lines per site, and `checker.fern` alone had 6,177 of
them — 10,272 uniqueness tests in all. The AST lowering's `__fern_arr_dec` is
one call, but it also frees none of the box's children, so the typed path's
extra work is the reclaim goal 2 is after; the size was how it was spelled.

A record or union with children is now released by `__sem_release_<T>`, the
helper map columns already used, and a site is one call. The helper's own body
keeps the inline expansion (`drop_value_inline`), so it cannot call itself;
a type that reaches itself descends through `__sem_drop_<T>` ->
`__sem_release_<T>` -> `__sem_drop_<T>`, one frame deeper per level than the
inline form. `drop_helpers` emits both helpers for every record and enum with
children; `merge_helpers` keeps one copy of each.

| `checker.fern` | before | after | |
| --- | ---: | ---: | ---: |
| emitted asm lines | 725,771 | 591,111 | **-18.6%** |

A whole-compiler build through the typed path (`fern.fern` to a linked binary)
went 223.6 s to 180.5 s (**-19%**, one run each) and the binary 12,639,547 to
11,573,819 bytes (**-8.4%**). The compiled compiler is no slower for the call:
stage 2 built with and without the change compiles `checker.fern` in 9.87 /
9.86 s and 9.71 / 9.87 s, and both emit the same bytes.

The reuse sites are untouched: they call `drop_children` and `release`
themselves, so their decline arm is still the plain box dec
`irverifyrc` recognises.

## 2. The ownership inference scanned its rows by name

`ownership.find` was a linear scan with a string compare per row, reached from
`consumed` and `lent_values` once per call operand per function per round:
**6.41% of the compile** inclusive. A round rewrites bits and never names, so
`ownership.Index` chains the rows by `util.hash_bucket` once, and `infer_rows`
resolves each function's own row once rather than every round. Output is
byte-identical.

Together, `checker.fern` to a binary: **15.05 s to 14.03 s (-6.8%)**, three
interleaved pairs. The index is about a third of that, in line with #8541's
finding that a scan loop's Ir overstates its time.

## Measured and not taken

Turning the inference off entirely (`inferred_pass` returning its input)
removes 18,111 of the 230k extra lines and 1.8 s of the 15.1 s. Part of that
is the second lowering, and part is a shape worth its own decision: the
inference is a GREATEST fixpoint, so a read-only recursive traversal — `f(a)`
matching `a` and recursing on a field of it — keeps its parameter counted,
because the recursive call is the only use that carries it out.
`checker.type_eq` is the example: every call retains both arguments and every
exit runs the uniqueness test and the release. #10352 has the reduced case and
the decision it needs.

## Gates

Run locally, on the tree as committed:

- `internal/e2eselfhost`, 57 tests: the IR verifiers and their corpus
  sweeps, ownership inference, inferred reuse, keyed maps, recursive,
  enum-field and receiver deep drops on three targets, the leak matrices,
  `TestSelfHostSemIRStrict`, block-reassign scope, the feature census and
  fixture sources; and, in a second run once a local mutation check had
  finished touching the tree, the physical-RC legs (x86-64, arm64, rejects),
  `TestSelfHostSemanticWholeCompilerX86_64`, the semantic reuse
  differential and the three `TestSelfHostSemanticSource*` tests.
- The semantic differential on x86-64, arm64 and wasm.
- `make check-sources`' checks and the lint ratchet.

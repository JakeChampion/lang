# Which conformance fixtures leak (2026-08-30)

`docs/TEST-GATES.md` states the gap plainly:

> The rc detector counts over-*releases* only. A leak reads as a clean 0.
> `FERN_LEAKCHECK=1` sees that a leak happened and `FERN_RC_TRACE=1`
> names the alloc site it came from … but neither runs as part of any
> gate — you have to go looking.

This went looking, over the whole conformance corpus.

## The census

**453 runnable fixtures. 319 clean. 134 leak, 66,570 unpaired
allocations.**

Heavily skewed: the median leaking fixture loses 4 allocations, and
`regex_captures_assert` alone accounts for 54,713 of the total. The
regex family dominates the top of the list.

| fixture | unpaired |
| --- | --- |
| regex_captures_assert | 54,713 |
| generic_fnarg_typevar | 1,850 |
| prop_string_involution | 1,124 |
| regex_named_groups | 907 |
| prop_sort_i32 | 900 |

The 68 fixtures not measured are negative ones — they carry an
`expected.error` or `expected.lowering-error` file and are not supposed
to compile.

## Method, and why it is trustworthy

Each fixture is emitted with `ast.RcTrace` on, run, and its `rctrace`
records paired by pointer. An alloc with no matching free is memory the
program never gave back on the path it took.

Two independent checks that the pairing is right:

- **Against the sanitizer.** `FERN_SANITIZE=1` reports
  `fern-sanitizer: leak N bytes in M blocks`. On `audit_std_json`,
  `array_sort` and `args` it reports 25, 1 and 2 blocks; the pairing
  says 25, 1 and 2 unpaired. Exact, three for three — and the sanitizer
  calling them `leak` is what says these are real leaks rather than
  benign liveness at exit.
- **Against a second implementation.** A throwaway shell harness
  driving the compiled CLI reached the same 134 fixtures and the same
  66,570 total as the in-process test.

## Cost

**16 seconds** for all 453, because the test emits in process rather
than spawning the compiler per fixture — the shell harness that did
spawn took about ten minutes for the same work.

## The pin

`internal/e2e/testdata/conformance-leak-census.txt`, one row per
fixture, the same shape as the self-host leak matrix's pin. A fixture
that starts leaking, or leaks more, fails. So does one that leaks
**less**: that is progress, and it has to be recorded rather than
absorbed.

Counts are pinned; **sites are not**. A site is a runtime return
address, so it moves with any codegen change and would churn the file
for reasons unrelated to reference counting. Sites are printed on
failure instead, where `addr2line` on a `-g` build turns one into a
source line — which is the half that says where to look.

## What this is not

It is not a claim that 134 fixtures have 134 distinct bugs; the regex
family alone is most of the volume and may well be one cause. It is not
a check of paths a fixture does not take. And it fixes nothing — it
makes a gap that was known in principle into a list with names and
numbers, and stops it growing quietly.

# 2026-10-02 — the two name hashes take the elidable byte loop

`util.hash_bucket` and `checker.sig_name_bucket` / `sig_bucket_from`.
Refs #8171. Emitted bytes unchanged: the `selfhost-emit-hashes` sweep is
1,965 rows, 0 differing from a compiler built from main at bca4b4cb.

## What the profile named

`util.hash_bucket` was 795 M of self cost on the 31.04 G stage-2 compile
of `checker.fern`, 2.6%, and the signature hash 219 M more. Both hashed
with a loop over `i < n` for a `n` bound to `s.len()` beforehand, and
`hash_bucket` took four bytes per step so the bounds check at `s[i]`
was paid once per four bytes instead of once per byte. The parser's
bounds-check elision takes only `while (i < s.len())` over `s[i]` with
`i` starting at a non-negative literal in the statement before and
stepped by non-negative literals, so neither loop qualified: every read
carried its `cmp; jae __fern_oob_abort`, and the four-byte step carried
four of them plus the multiplies of the unrolled roll.

## What changed

Both loops are the elidable shape: `let i: i32 = 0; while (i < s.len())
{ … s[i] … i = i + 1; }`. The four-byte step is gone, so `hash_bucket`
is the byte-at-a-time roll `a = a * 31 + byte` it always computed, and
`util_hash_run.fern` with `TestSelfHostUtilHash`, which pinned the
unrolled step against that roll, go with it: there is no second body
left to drift. `sig_name_bucket` holds the loop; `sig_bucket_from`, run
once per signature when the table is built, hashes a copy of the suffix
through it, so the suffix key and the whole-string key still share one
body.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at bca4b4cb and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.04 G | 30.92 G (−0.38%) |
| stage 2, `util.hash_bucket` self Ir | 795 M | 712 M |
| stage 2, `checker.sig_name_bucket` + `sig_bucket_from` self Ir | 219 M | 193 M |

Four bytes per step bought less than the bounds checks it kept: the
rolled loop with no check is a load, a multiply-add and a compare per
byte.

## Witnessed

`TestSelfHostFeatureCensus`, `TestSelfHostFixtureSourcesCheck`,
`TestSelfHostSemanticSource*`, `TestSelfHostCheckerCodes*`, the lint
ratchet, `make check-sources`, and the emit-hash sweep. The fixpoint,
the native and wasm suites are CI's.

## Next

The elision's shape is the lever: a loop that binds the length to a
local, starts from a parameter, or steps by a variable keeps every
check. `x86_native.x86_name_bucket` and `x86_line_bucket` are the same
shape and go next (62 M). The ceiling for the whole compile is the 865 M
of checks over 10,989 sites measured on 2026-10-01; a guard-proven
unchecked read in the lowering would take the rest without rewriting
each loop.

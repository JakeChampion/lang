# A call that keeps no loan leaves the array sole

2026-11-15. All targets. The change is in the sole boxes of the unit plan
(`ssaunits.sole_boxes`, `ssaunits.loan_rows`). It fixes #16163.

## The shape

`lend_loop` in `TestSelfHostUniqueLoop` writes its own array in a loop, and
it lends the array to `peek` in the same loop:

```
xs = xs.with(i, peek(xs, i) * 2i64);
```

`semunique` tests the count once, before the loop. Each `with` in the loop
then needs no test, because its receiver is sole. On main, the call made the
array shared, so each `with` tested the count again: 9 count-test labels in
`lend_loop`, not 3.

52e35be6 deleted `keeps_no_loan`. That rule used the types of the call: a
scalar result and scalar other arguments. The rule was not safe, because a
callee that gives the array to `set_args` keeps a reference.

## The change

The loan rows say which array parameters each function can keep after it
returns. They are a greatest fixpoint over the module. A function keeps a
parameter that it returns. It also keeps a parameter that it gives to an
operation other than a read. A parameter that it lends to a call that can
keep it is kept too, and a phi carries the box. A callee with no row keeps
its arguments.

`sole_boxes` and the loan rows use one rule, `only_reads`.

## Measured

Count-test labels in the x86-64 asm of `TestSelfHostUniqueLoop`:

| Function | Before | After |
|---|---:|---:|
| `lend_loop` | 9 | 3 |
| `lend_deep` (two calls, both read) | not in main | 3 |
| `stash_loop` (two calls into `set_args`) | not in main | tests after the call |

Emitted lines of the `bench` corpus, the metric of the perf lane. Both
compilers were built by the pin, from 2d8c4355 and from this change. No row
grows.

| Benchmark | x86-64 | arm64 | wasm32 |
|---|---:|---:|---:|
| deflate_decode | 15117 to 14941 (-1.16%) | 16979 to 16784 (-1.15%) | 26128 to 26053 (-0.29%) |
| deflate_encode | 7793 to 7759 (-0.44%) | 8268 to 8235 (-0.40%) | 12923 to 12908 (-0.12%) |
| http_hello | 16222 to 16125 (-0.60%) | 17077 to 16972 (-0.61%) | 19476 to 19418 (-0.30%) |
| utf8_ingest_unchecked | 8442 to 8383 (-0.70%) | 8868 to 8809 (-0.67%) | 9406 to 9376 (-0.32%) |
| utf8_ingest_validated | 9113 to 9054 (-0.65%) | 9546 to 9487 (-0.62%) | 10401 to 10371 (-0.29%) |

## Tests

`TestSelfHostUniqueLoop` gets `lend_deep`, which lends its array to `outer`,
and `outer` lends it to `inner`. It also gets `stash_loop`, which gives its
array to `set_args` through two calls in a loop. That loop tests the count
after the call, and `args()` keeps the old elements.
`TestSelfHostSetArgs/lent-through-two-calls-in-loop` runs the same shape
under the sanitizer and the leak check on each target.
`TestSelfHostSoleLoopWithX86_64/lent-to-call` expects no test again.

## Next

#16159 item 3 is the loop of `longest` in `std/deflate/zlib.fern`. Measure
it again with this change.

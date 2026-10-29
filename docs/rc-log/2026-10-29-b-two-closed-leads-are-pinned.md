# 2026-10-29 — two closed leads are pinned

Tests only. Fixes #13788.

## The leads

Two leads in the tracker and the log describe a shape that main no longer has.
This entry records the measurement on main at e24ef291 and the test that now
pins each shape.

### A `Some` box goes to an `Ok` (#13788)

#13788 reports that `Option.ok_or` releases its unique `Some` box and then
allocates a box for the `Ok`. It also reports that the error argument is built
before the call, on the success path too.

Since 7e0e8c54 the caller inlines an instance of a generic function. Thus
`ok_or` becomes a match in its caller, and the error is built only on the
`None` path. `TestSelfHostGenericInstanceInlined` pins that form.

A conversion that is not inlined also builds the `Ok` in the `Some` box.

| Shape, 1,000 calls | x86-64 | arm64 | wasm32-wasi |
|---|--:|--:|--:|
| `some(i)`: the array and the `Some` box | 2,000 | 2,000 | 2,000 |
| `to_result(some(i), e)`, a `@noinline` match | 2,000 | 2,000 | 2,000 |
| `net.ip_parse` written with `ok_or` | 1,000 | | |
| `net.ip_parse` as `std/net` has it, an explicit match | 1,000 | | |

The issue measured 4 allocations per parse for the `ok_or` form. Both forms
now allocate only the address payload.

`TestSelfHostOptionToResultReusesTheSomeBox` pins the second row against the
first on the three targets. It also checks for a balanced census under
`-sanitize`.

### A spliced swap remove with a held element

The "Next" section of `2026-10-28-b-an-element-read-is-held-across-a-grow.md`
says that this loop copies the array on each removal:

```fern
let p: P = ps[at];
ps = remove_at(ps, at);   // spliced: if (at != last) { ps = ps.with(at, ps[last]); }
sum = sum + p.n + p.name.len();
```

On main, 3,000 removals allocate 0 times on x86-64, arm64 and wasm32-wasi. The
result is the same with `at` fixed at 0 and with `at` that moves.

The `held-element-spliced-swap` row of `TestSelfHostArrayTruncate` pins the
shape with an allocation bound of 100. The rows before it did not take this
path. `held-element-join` removes the last element, so the `with` does not
run. `held-element-removal` calls a `@noinline` function.

## Traps

A test that pins a closed lead must take the path that the lead names. The
old rows did not take the swap path, so the lead looked
pinned when it was not. `TestSelfHostOptionToResultReusesTheSomeBox` fails when
its expected value is changed, so it reads the count that it pins.

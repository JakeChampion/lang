# An append result used as a receiver or an argument is released

#10560. On the AST lowering, an expression-position `.append` whose result is
pushed into again (`acc.append(a).append(b)`) or handed to a call
(`id(xs.append(1))`, `rec(n - 1, acc.append(x))`) stranded the result's count.
Scalar elements leaked too, so it was a buffer leak, not an element leak.

## Cause

`lower_call_append` gives the result its own count: the copy of a bracketed
receiver is fresh, and an in-place push on a borrowed parameter is retained on
identity. Two consumers did not take that count over.

- A second append on the result. `arr_push` grows or copies into a new buffer
  and never frees the old one, so the superseded buffer kept its count.
- A call argument. The post-call release of an append temp exists (#6501), but
  only at a borrowable, counted or consumed position. A callee that hands its
  parameter back (`return acc`) is none of those, because the array tier of
  `param_counted_of` refused every array result. A recursive accumulator is
  also refused, because `acc.append(x)` was not a credited use of `acc`.

## Change

- `lower_call_append` parks a counted receiver (`append_result_counted`) in a
  scratch and releases it with a buffer-only `__fern_rc_dec` when the push
  returns a different pointer. In place, the count passes on in the result.
- The array tier admits an array result for an "ARROWN:" member: every return
  hands back a count of its own, so a bare `return p` is credited as the
  retained handback (the same `ret_hb` rule the enum tier uses). This applies to
  scalar-element parameters only.
- `p.append(v)` is a credited use at the array tier for a scalar-element `p`.
  Its result is a retained alias or a fresh copy, so it keeps nothing of `p`
  uncounted. A `string[]` append receiver is refused, because its copy shares
  element pointers without retaining them.
- "ARROWN:" admits a free function's call to itself, by induction on its other
  returns. Without this, `rec` never became a member.
- The identity retain now covers the 8-byte pushes as well. `return
  p.append(1.5)` on a borrowed `f64[]` handed the caller's buffer back
  uncounted, and `xs = f(xs)` then freed it under `xs`. That was a
  use-after-free under FERN_SANITIZE=1 on x86-64 and an rc underflow on wasm.

## Measured (allocs / frees, AST lowering; x86-64 and wasm agree)

| shape | before | after |
|---|---|---|
| issue receiver repro (`step` + `q` chain) | 122 / 67 | 122 / 122 |
| `id(pending.append(fd))` x10 | 11 / 1 | 11 / 11 |
| `rec(10, [])` accumulator | 6 / 0 | 6 / 6 |
| `inplace_guard` (1000 grows, 1000-deep `rec`) | 512 / 11 | 512 / 512 |
| `xs = f(xs)`, `f` returns `p.append(1.5)` | UAF | 3 / 3 |

Allocation counts are unchanged, so no in-place push became a copy.
`TestSelfHostAppendResultTemp{X86_64,Wasm}` pins every row under both
lowerings. The semantic lowering balanced all of them already.

## Trade

A post-call release follows the self-tail call in `rec`, so the op-stream TCO
no longer fires there. That is the rule `TestSelfHostSemanticTailRecursion`
already records for the AST lowering: when a frame owes a release after the
call, the lowering keeps the call. The semantic lowering keeps the loop.

## Still leaking

- Elements of a `string[]` or struct array rebound from a chained append
  (`q = q.append(a).append(b)`) or from a non-producer call
  (`pending = step(i, pending)`). The buffers balance, but the local loses its
  element-walking credit. `string_elements_chain` and `struct_elements_chain`
  allow exactly those elements.
- A `string[]` append temp passed as an argument, which #6501 refuses.
- `hs = hs.append(hold(...))` for a struct whose field is an array leaks every
  element, whatever the argument is. The same happens with `hold([i])`.

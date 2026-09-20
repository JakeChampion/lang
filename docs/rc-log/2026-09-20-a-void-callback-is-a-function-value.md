# 2026-09-20 — a void callback is a function value

Two rows of the typed-lowering census were one root: `unsupported void call`
(20 sites) and what was left of `function signature slot` after the
signature tag landed (16, all of them the void result the tag kept
refusing). Every site was a callback whose call yields nothing — the `f` of
`for_each` in `std/ordmap`, `std/pmap`, `std/pvec` and `std/bench`, and the
`(K, V) => void` visitors the tests hand them — refused where the function
type was declared, so the whole `for_each` family and every caller of it
stayed on the AST lowering.

## What changed

A void result is one word like any other slot: every backend hands a word
back from a void body (x86-64 pushes `%rax`, arm64 `x0`, and wasm types a
void callee `(result i32)` with the same all-narrow funcref as an `i32`
one), and the direct call already produces a void value in statement
position and stores the dummy. So:

- `ssasem.func_shape_error` admits a void result; a function type inside
  another is still the one refusal.
- `semsource.indirect_call` takes the statement flag its direct-call
  siblings take: a void call through a value stands as a statement and is
  refused in expression position, the rule every void call has.
- `irlower.make_wrap_named_func`, the lift's trampoline for a bare function
  name used as a value, wrote `return f(p…)` whatever `f` returned. The
  checker refuses that of a void `f` (E002), and so did the typed producer;
  the trampoline for a void target now calls it as a statement, the way the
  source would have to. The AST lowering emits the same code for both
  shapes: the compiler built with the change emits `fern.fern`
  byte-identically on x86-64 and arm64.

## Measured

Corpus census, x86-64, `FERN_SEM_IR_REPORT=1`, 864 programs, with the
struct-method clone fix of the same day
(`2026-09-20-a-struct-method-clone-is-not-a-template.md`) in the "after":

| | main at 1702eb7a4 | after both |
|---|---|---|
| declarations produced whole | 69,977 of 86,879 (80%) | 71,254 of 86,879 (82%) |
| programs produced whole | 757 of 864 | 765 of 864 |
| `unsupported void call` | 20 | 0 |
| `function signature slot` | 16 | 0 |
| `uninstantiated generic` cascade sites | 117 | 0 |

No program's compile status changed. `ordmap_test`, `pmap_test`,
`pvec_test` and `bench_test` print the same TAP through both lowerings.

`TestSelfHostSemanticProduction` gains `void-callback`: one void function
value reaching its call every way a value can — a parameter, a local, a
record field, a tuple element, a lambda and a bare name through the
trampoline — produced 0 of 7 before, 7 of 7 after, compiled through both
lowerings on x86-64, x86-64 under the sanitizer, arm64 and wasm.

## What is left in the same programs

`ordmap_test`'s and `pmap_test`'s `test_for_each` write a captured local
from the visitor (`seen = seen * 3 + …`), which is `replacement of a
capture`, a limit of the closure representation and not of this slice. The
census's largest roots are now the verifier's `function value is not a
field` (72, the async and sim tests), the remaining OS-floor contracts
(72: `tcp_send`, `set_file_times`, `window_size`, `mknod` and a tail) and
the mixed-module rule `calls a function value of N arguments, a type the
AST lowering builds a value of` (36).

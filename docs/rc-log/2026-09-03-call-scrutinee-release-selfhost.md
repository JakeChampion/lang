# 2026-09-03 — self-host: an enum payload consumed straight off a producer call (#7910 (d))

`match (mk(i)) { … }` where `mk` is a registered fresh producer. 100 rounds,
allocs/frees live, self-host against the native oracle of the same tree.

| shape (x86-64) | native | self-host before | after |
| --- | --- | --- | --- |
| the issue's probe (`Result[string[], string]` + `Option[Option[string[]]]`) | 350/350 `0` | 500/0 **24000** | 500/500 `0` |
| `Result[string[], string]`, Ok / Err both taken | 200/200 `0` | 300/0 **14400** | 300/300 `0` |
| `Option[Option[string[]]]` | 400/400 `0` | 400/0 **18400** | 400/400 `0` |
| `Option[string]`, producer-built | 100/100 `0` | 200/0 **11200** | 200/200 `0` |
| `Option[string[]]` BOUND to a local first | 200/200 `0` | 300/0 **14400** | 300/300 `0` |
| `enum E { Full(string[]), Note(string), Nil }` | 168/168 `0` | 201/0 **9648** | 201/0 **9648** (refused, below) |

## The position, isolated

Measured before the fix, x86-64, one payload position per probe:

| position, consumed by `match (mk(i))` | self-host |
| --- | --- |
| `Result[string[], string]`, only `Ok([w(i)])` | 300/0 **14400** |
| `Result[string[], string]`, only `Err(w(i))` | 200/0 **11200** |
| `Result[string, string]` | 200/0 **11200** |
| `Result[i32[], string]` | 200/0 **8000** |
| `Option[string[]]` | 300/0 **14400** |
| `Option[string]` | 200/0 **11200** |
| `Option[Option[string[]]]` | 400/0 **18400** |
| `Option[Option[string]]` | 300/0 **15200** |
| `Option[Option[i32]]` | 200/0 **8000** |
| `Option[i32[]]`, bound to a local first | 200/200 `0` |
| `Option[i32[]]`, direct | 200/0 **8000** |

Every position leaks in the DIRECT position — including `Result[i32[],
string]`, whose payload the release already knew — and the one bound form is
clean. So the position is the finding, not the payload type: a scrutinee is a
value with no name, and the whole call-bound release keys on a `var`.

The bound rows for `string[]` and nested `Option` leaked too, which is the
second half: the call-bound admission (`rcpayload_option_call_ptype`) took a
leak-safe array or a string, and no string[], no nested Option.

## The two halves

**The scrutinee becomes a binding.** `hoist_call_scrutinees` runs first in
`lower_func` and rewrites `match (mk(i)) { … }` into
`var $mscrut_L_C: T = mk(i); match ($mscrut_L_C) { … }` when `mk` is in the
OPTFRESH or "RCE:" registry and its return type carries no type variable (a
`$arg0` return would have to be resolved from the call's arguments first).
Every analysis then sees a binding, so the direct form earns exactly the
credits the written binding earns — no second release path, and no admission
widened to reach it.

**The binding's release learns the rest of the payloads.** `call_ptype_admitted`
now takes a `string[]` payload and a nested `Option` — scalar-inner through
the one-level dec, rc-inner through `nested_opt_inner_freefn` — and a
call-bound nested candidate takes `emit_optopt_rc_deep_free` rather than the
tag-guarded one-level drop: that emitter guards both levels' null AND tag
itself, so an unknown runtime variant costs it nothing, which is what the old
refusal had not credited it with. The OPTFRESH freshness flags read the
fresh-string registry through `opt_ctor_payload_fresh` (a literal, a
registered producer, an array literal of those, or a nested constructor of
those), so `Some([w(i)])` is provably sole-owned where the syntactic test saw
only concat and method producers.

## The census, and what it cost

The self-host wildcard-arm ceiling has ~3 arms of headroom, and this change
plus (a)–(c) added twelve. All of them are gone rather than banked: the
statement walk is an if-let chain, the one-shape discriminators
(`map_site_owncols`, the for-in receiver, the empty-array default, the call
callee) read `ident_name_or_empty` or an if-let, and the string-array
freshness question routes through the existing `tuple_strarr_elem_fresh`
(widened by a registry parameter) instead of a second copy. The census reads
2949 wildcard arms and 96 nested named fns — both at or under their pins.

## The user rc-enum producer is NOT closed, and the hoist is not why

`enum E { Full(string[]), Note(string), Nil }` built by a producer and
consumed by a match keeps its 201/0 against a native oracle at 168/168.
Three probes say the position is not the cause:

| shape (x86-64) | native | self-host |
| --- | --- | --- |
| `match (mk(i))`, direct | 168/168 | 201/0 **9648** |
| `var e: E = mk(i); match (e)` | 168/168 | 201/0 **9648** |
| `E { Full(string[]), Nil }`, direct | 150/150 | 200/0 **8800** |
| `E { Note(string), Nil }`, direct | 100/100 | 150/0 **7200** |

The BOUND form measures identically, so what refuses this is the consuming
rc-enum release itself — `rcenum_call_init_owner` admits a producer call only
through an "RCE:" registry row, and this enum earns none — not the scrutinee
position the hoist addresses. The Option / Result rows above are what this
slice closes; the row is pinned `clean leak` in both matrices as the next
lead.

## Witnessed

Leak-matrix rows `res_strarr__callscrut__match`,
`optopt_strarr__callscrut__match`, `opt_str__callscrut__match`,
and `opt_strarr__callbound__match` on x86-64 (with the sanitize leg) and
arm64, with `rcenum_mixed__callscrut__match` pinned beside them as the
refusal above; `TestSelfHostCallScrutineeReleaseWasmIR` runs the four closed
shapes on wasm with a balanced census and the interpreter's exit code.

# 2026-09-04 — the owned-by-default param rung is blocked on an ABI half, not on analysis

`rc_fe_run`'s taint-seed comment called native's `paramVerdict` ladder "the
documented cut", and `TestSelfHostRcPlanDiff/tuple-elem-extract-bind` said the
tuple-param divergence "closes with it". Read together those invite a one-line
change: stop seeding container params tainted. That change would compile, close
the pinned line, and free boxes the caller still owns.

Nothing is ported here. What lands is the evidence, the corrected reasons, and
one new pinned case that was missing.

## What the ladder actually costs

Native's owned-by-default param is **two-sided**. The caller retains the
argument (`calleeParamOwnedByDefault`, `internal/ir/ir.go:6363`) and the
callee's exit sweep spends that count. `paramVerdict` is one ladder precisely so
the two halves cannot disagree.

This compiler has no caller-side retain — `calleeParamOwned` / `arg_retain` and
friends find nothing in `examples/self_host/` — and asserts the opposite
invariant in the emitter:

> Array params (slots < n_params) are BORROWED — the caller retains ownership,
> so the callee never decs them (the n_params borrow boundary).
> — `emit_dec_sweep_except`

So un-seeding a container param cannot credit the param itself: no
`slot_is_reclaimable_*` accepts a param slot, and the plan-routed gates look up
`name@line:col` binding-site keys, which a bare param name never has. Its only
effect is indirect — taint stops flowing to locals *derived* from the param, and
those locals get real releases on memory the caller owns. That is an
over-release across the borrow boundary, not a leak.

## Two corrections that shrink the prize

**The owned-by-default set is enum / struct / tuple only.**
`ownedByDefaultShapeIn` (`internal/ir/rc_caps.go:304`) switches on `EnumType`,
`StructType`, `TupleType` and returns false for everything else. There is no
`ArrayType`, `StringType` or Map arm. Array, Map and string params are
`paramVerdictNotOwnedType` on native and **already agree** with this compiler's
seed. The tuple pin is close to the whole visible prize.

**Owned is reached by ESCAPING.** With `BorrowInferEnabled` on, the ladder
demotes every non-escaping param to borrowed. `src` in the tuple case qualifies
because it escapes. The decisive rung therefore reads `inferParamEscapes`
(`rc_analysis.go:794`), a whole-program greatest fixpoint this compiler does not
have — `borrowable_params_of`'s own header says so, and the interproc cost note
downstream records that the existing registry already OOMs the arm64
self-compile if recomputed per function. A second whole-program pass has a hard
budget, measured.

Rungs 4 and 5 (vtable-dispatched, address-taken) are not vacuous either: `dyn
Trait` and function values both lower through `call_indirect`, and skipping them
reproduces native's #6465 / #7307 crashes.

## The false reason, and why the code it justified is right anyway

The seed exempts `string` params and said native does too, "owned-by-default".
It does not — see the shape set above; native's seed (`rc_analysis.go:2228`)
taints a string param like any other borrowed one.

The exemption is still correct, and the reason is #7553 rather than parity.
Measured with a new rc-plan case, `fe-string-param-alias`:

```
function f(s: string): i32 { var L: string = s; return L.len(); }
function main(): i32 { var k: string = "abcdefghij"; return f(k); }
```

| table | native | self-host |
|---|---|---|
| `f` aliasBindIncs | `2:2=L` | (none) |
| `f` freeEligible | (none) | `L` |
| `f` lastUses | (none) | `L=1` |
| `main` freeEligible | (none) | `k` |
| `main` lastUses | (none) | `k=1` |

Read cold that looks like the over-release above: a free with no retain. It is
not. This is the leak matrix's `str__fnscope__alias_param` shape — the callee
only aliases its param, so the param stays borrowable and the caller keeps its
own release — which is `clean clean` on x86-64 and **`leak clean` on arm64**,
one of the four cells where this compiler is AHEAD of native (native-arm64 leaks
it, #7446). Both legs of that cell pass, including the `FERN_SANITIZE` re-run
that is what reports an over-release rather than a leak.

So the extra credits are reclaims native does not make, not frees it must not.
Seeding a string param tainted for parity's sake would propagate through this
alias and take #7553's reclaim back out.

## Trap

A plan-table divergence that reads "self-host frees, native does not" is not
evidence of an over-release. The plan says free-*eligible*; whether a release is
emitted is a gate decision downstream, and whether it is *sound* is a runtime
question. Both of this one's directions were already covered by a matrix cell
whose sanitize leg passes. Check for an existing cell before reaching for the
alarming reading — the leak matrix indexes by kind × scope × consumption ×
origin, so an aliased-param shape is `<kind>__<scope>__alias_param`.

## Next lead

The prerequisite chain, in the order it has to land, is now listed at the
`freeEligible` port header in `irlower.fern`. No sub-slice of it is
independently sound: every rung is a widening, and a widening without the
caller-side retain is an over-release. The first piece that is worth anything on
its own is the whole-program param-escape fixpoint, because it is also what
`borrowable_params_of` is approximating today — and it must be built inside the
budget the interproc cost note measures, not on top of it.

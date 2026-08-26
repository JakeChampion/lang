# Declaring a getter stranded the type — the string[]-field forwarder

`function (h: Holder) get(): string[] { return h.xs; }` is a read of `h.xs`, and
the string[]-field admission walk marked it. The mark is keyed on the TYPE, so
one such declaration anywhere in the program refused `__struct_drop_Holder` for
every `Holder` in every function — and the method never had to run.

## What it was

Loop-resident probe, 100 rounds, self-host x86-64, `FERN_LEAKCHECK=1`. Native
x86-64 and `bin/fern -interp` agree on every answer; native's counts differ in
absolute terms because it allocates a different number of boxes for the same
source, so only the ANSWERS are the oracle.

| shape | before | after | native |
| --- | --- | --- | --- |
| `keep.get().len()` | `800/100` **28800** | **`800/800` 0** | `500/500` 0 |
| `get()` declared, NEVER called | `800/100` **28800** | **`800/800` 0** | `500/500` 0 |
| free `grab(keep).len()` | `800/100` **28800** | **`800/800` 0** | `500/500` 0 |
| a Holder outliving 100 rounds of churn | `806/101` **29000** | **`806/806` 0** | `504/504` 0 |
| direct `keep.xs.len()`, no forwarder | `800/800` 0 | `800/800` 0 | `500/500` 0 |
| `var g = keep.get()` | `800/100` | unchanged — refused | `500/500` |
| `keep.get()[0]` | `800/100` | unchanged — refused | `500/500` |
| `for s in keep.get()` | `800/100` | unchanged — refused | `500/500` |
| free `var g = grab(keep)` | `800/100` | unchanged — refused | `500/500` |
| element escaping through a second call | `800/100` | unchanged — refused | `500/400` |
| forwarder returning two DIFFERENT fields | `900/100` | unchanged — not registered | `600/600` |
| bare-ident element shared by two literals | `1000/600` | unchanged — store gate | `700/700` |
| tolerated borrow + refused direct read | `1400/700` | unchanged — refusal wins | `800/800` |

288 B/round, exactly linear, unbounded — on a loop whose body was `t = t + 3`.

## Row 2 is the whole finding

The call is not what does it. `main` constructs the struct and does nothing else
with it; deleting the `get()` declaration makes the same program clean. That is
what separates this from a call-position defect (#7416 fixed one of those and
left this row byte-identical) and what makes the blast radius the point: keyed on
the type, so one declaration reaches every instance in every function.

## The verdict moves to the call sites

`strarrfld_forwarders_of` registers every function whose returns ALL hand out one
borrowed `string[]` field of the receiver or a struct parameter. Inside such a
function the forwarding return is exempt from the walk; at a CALL of one, the
walk applies its existing read rules to the forwarded field. A `.len()` receiver
admits, everything else marks. So `keep.get().len()` is the borrow
`keep.xs.len()` already was, and `var g = keep.get()` is the escape
`var g = keep.xs` already was — one rule, reached through a call.

Three registry spellings, probed with `tagged_value_of`:

    "m:<Recv>.<method>|<T>.<field>"   receiver type resolved
    "n:<method>|<field>"              receiver type NOT resolved
    "f:<name>|<T>.<field>"            free function

The `n:` row is the conservative fallback, and it marks the bare field NAME,
which the admission already reads as poisoning that field in every type.

## Two things that make it a carve-out rather than a widening

**All-or-nothing and single-valued.** A body whose returns forward two different
fields is not a forwarder: no call site could choose which mark to make, so both
reads mark as they did before. Pinned as `two_field_forwarder_not_registered`.

**An `own` parameter is refused.** A consumed struct is not a borrow, so a field
read out of it is not the shape this rule reasons about.

The free-function rows are deliberately NOT filtered against the names a frame
shadows, the way `arr_producers` is. The two registries answer opposite
questions: a producer row ADMITS a store, so a shadowed name must drop out or an
unknown body gets credited; a forwarder row MARKS a read, so a shadowed name
marks a field that was never handed out. Over-marking is the direction this scan
already errs in.

## The soundness row has to be an outliving value

The admission's own contract forbids the reads that would witness a dangle: any
read of the field that is not a `.len()` borrow refuses the type, so a probe that
reads elements back is a probe of the refused path. What is left is a Holder
built OUTSIDE the loop and borrowed through the forwarder on every round, while
100 admitted Holders are constructed and deep-freed around it. Its length still
answers native's 1 after the churn, `__rc_underflow_count()` is 0, and
`FERN_SANITIZE=1` + `FERN_RC_UNDERFLOW_TRAP=1` + `FERN_RC_FREE_DEBUG=1` are
silent on all five admitted rows. The refused rows report only their leak.

## The walk was missing two node kinds

`strarrfld_scan` had no `ExprMapLit` or `ExprFString` arm, where all five sibling
walks in `irlower.fern` have both — an unwalked `f"{h.xs[0]}"` is an element
handed to a formatter with nothing marked. Added, and **contract-only**: every
shape I could build desugars before `irlower` sees it (the compile path turns a
map literal into `map_new` + `set` calls, and an f-string into `.to_string()`
calls the walk already reaches through `ExprCall`), so both arms are unwitnessed
by any probe. They can only add marks, so the direction is a leak, never a
dangle.

## Left open

- **The `n:` fallback is unwitnessed too.** Every shape that makes a receiver
  type unresolvable — `var keep = mk();`, `hs[0].get()` — strands its own struct
  for unrelated reasons, so no probe reaches a case where the fallback's mark
  changes a count. Both rows sat at `800/0` and `900/200` regardless, and were
  dropped rather than pinned as numbers that say nothing.
- **`var keep = mk()` gets no field reclaim at all** (`800/0` across 100 rounds,
  where native is `500/500`). Unannotated struct locals initialised from a call
  are a separate gap, found while looking for an unresolvable receiver.
- A forwarder that forwards ANOTHER forwarder (`return h.get();`) is not
  registered — the return is a call, not a field read — so its own call sites go
  unmarked and the field is refused at the inner call instead. Conservative.

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_strarr_field_forwarder_test.go`.

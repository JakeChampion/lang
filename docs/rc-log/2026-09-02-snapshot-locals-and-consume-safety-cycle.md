# The threaded state local reclaims its generations (#6644 distcheck, slice 6)

`2026-09-02-own-struct-update-reuse.md` left the self-built stage1 compiling
`parser.fern` with 2.1 GB live, and named the next class by count. Traced by
BYTES instead, one class is most of it: on `parser.fern`, 1.50 GB of the
2.11 GB live at exit is grow buffers out of `__fern_arr_push`, and on
`lexer.fern` two thirds of those are the `ops` buffer `LowerState.emit`
clones for its functional update — every one of the 8,752 it grew survived.

## Where the generations went

Every lowering function threads its state the same way:

```
function lower_call_named_generic(e, c, cid, s: LowerState): LowerState {
    var sc: LowerState = s;
    …
    sc = lower_expr(c.args[ai], sc);
    sc = sc.emit(op);
    …
    return scr;
}
```

`sc` starts as an alias of the caller's box, and every rebind hands it a fresh
box — `emit` builds a new `LowerState` with a cloned `ops` buffer per op. The
self-host had no credit for such a local: its first value is the caller's,
which no sweep may touch, so `reclaimable_names_of` refused it and every
generation between the first and the last leaked — one box and one copied
`ops` buffer per emitted op, at every frame of the lowering.

The compiler already reclaimed two neighbouring shapes. A snapshot PARAM
(`s = s.write(x); return s`, #3456) copies its entry box into a hidden local
and releases each superseded generation under `__field_reclaim_<T>(new, old,
snap)`: a field pointer-equal in `new` or in `snap` is left alone, the rest go
with the box. A snapshot LOCAL (#3457) bound from a fresh-ret producer (`var st
= se.emit(op)`) did the same with no snapshot at all.

## The snapshot every derived local needs

The second shape was unsound. `var st: Buf = se.add_local(1)` copies the
borrowed parameter's fields into `st`'s box uncounted — the functional update
retains its string and nested-struct fields but not its arrays — and the first
rebind `st = st.emit(2)` released `se.ops`, the CALLER's buffer, because the
release compared the old box against the new one only. The sanitizer's
quarantine confirmed it (`snapshot_local_shared_field`, exit 124 before).

So a struct local now records the variable its binding DERIVES from
(`snapshot_source_of_init`): the alias `var q: T = p` names `p`; a call names
its receiver, or the one bare-ident argument declared at the local's type.
`seed_snapshot_local` copies that box into `$snap$<slot>` right after the
binding, and the rebind release takes it as the snapshot a snapshot param gets
at entry. That guard is what admits the two shapes the compiler is made of:

- the alias `var sc: LowerState = s`, and
- the call-derived local `var sl0: LowerState = lower_expr(b.left, s)` from a
  producer that is NOT fresh-ret and may hand `s`'s own box back — the
  snapshot covers exactly that case, and a derived box is the local's alone.

Neither earns a struct credit of its own (their final box may still be the
source's): their rebinds alone reclaim, and `tail_release_snapshot_locals`
releases the last generation at a `return sc.emit(x)` tail, with the result
standing in for the new value. A local whose derived value is bound elsewhere
and may outlive a later rebind keeps its release box-only ("NOFLD:",
`derived_outlives_rebind` — type- and order-aware, so a scalar binding or a
binding after the last rebind does not cost the field release).

A derivation is any mention of the local in the bound value, a bare ARGUMENT
included: the callee may spread-copy it into its result, and a spread retains
no array field. The exit sweep's box-only mark had only ever counted a method
receiver, which the first version of this slice found out the hard way — with
the rebinds admitted, `var apf = s.emit(op); apf = lower_expr(x, apf); return
emit_arr_store(apf, …)` credited `apf`, the sweep deep-dropped its `locals`
under the box the return had just copied them into, and the self-built
compiler crashed compiling `examples/tests` (the arm64 stage-2 fixpoint saw it
first, as a gen2 segfault). `derived_anywhere` closes that for the sweep too.

## The cycle the consume-safe registry could not admit

Admission needs every rebind `sc = g(.., sc, ..)` to name a callee that is
consume-safe at that position — the callee keeps no second reference to the
box. The registry was a least fixpoint from below, and the lowering is a
mutually recursive call graph: `lower_expr` dispatches to functions that call
`lower_expr` on their own `sc`. From below no member is ever safe, because each
is safe only once the others already are. Measured, it left every one of the
71 `var sc: LowerState = s` in the compiler with no reclaim, and no `irlower`
parameter consume-safe at all.

`consume_safe_params_interproc` is a greatest fixpoint now: every parameter
starts safe and each pass strikes out the ones whose body escapes them under
the previous pass's registry. Sound because an escape is a concrete statement
— the first pass strikes the parameter it escapes, each following pass the
forwarders one call further out, and a parameter still marked at convergence
has no chain to any escape.

Three shapes had to stop counting as escapes for the cycle to hold, each with
its argument:

- `var q: T = name` and `var q: R = g(.., name, ..)` (name at a consume-safe
  position), when `q`'s own uses all pass the same scan — asked of the
  caller's box (`own_rebinds` false), whose only releaser is the caller. Asked
  of a snapshot param or local, whose own rebinds release, the alias stays an
  escape except as the handback pair `var st: R = g(.., name, ..); name =
  st.f;` (the `ArgStash` pattern at every call argument).
- `return g(.., name, ..)` and `return T { f: name, … }`: the box goes back to
  the caller inside the result, the move-out a bare `return name` already is.
- `x.m(.., name, ..)`: a method argument, judged over every method of that
  name in the registry since the walk cannot type the receiver. This lives in
  the consume scan only. A first cut put it in the shared escape walk that
  also feeds the BORROWABLE registry, and `"application/json"` was stashed
  and freed after `__http_response_typed` had stored it — `http_content_type`
  and `http_cookies` failed the fixture sweep, while passing under the
  sanitizer, whose quarantine keeps a freed string readable.

## Measured

Reduced, `struct_alias_thread_reclaim` (aliased state, method and callee
rebinds, `return sb.emit(v)` tails), 8 rounds:

| | leaked blocks |
|---|---|
| before | 960 |
| after | 16 |

The 16 are the `names: string[]` buffers each update replaces, which
`__field_reclaim` frees only under the string[]-field admission.

`struct_state_handback` (the `ArgStash` shape): 784 to 160 — the box handed
back at rc 2 (the result struct's alias-inc) is skipped by the uniqueness gate
once per iteration; its own class.

The sanitized self-built stage1 assembling natively:

| module | before | after |
|---|---|---|
| lexer.fern | 95 MB live | 81 MB |
| parser.fern | 2.11 GB | 1.05 GB |
| checker.fern | 3.57 GB | 2.82 GB |

401 snapshot rows in the compiler, 480 `__field_reclaim_LowerState` sites
against 57. No sanitizer finding on the three modules, on the six fixtures,
or on the `examples/tests` inputs that crashed the arm64 stage 2.

## Found on the way: a variant over an array field took no count

CI's `std/pvec` suite, self-built for arm64, read a freed leaf after 3,000
appends. The append's level-grow builds `Leaf(v.tail)` over the receiver's
tail buffer, and `lower_variant_ctor_args` retained an array payload only
when it was a bare local (#3720): a FIELD READ went in uncounted. Nothing
had released that field before — the test's `var v = pvec_new(); v =
v.append(i)` took the box-only rebind — and this slice's admission routed the
rebind through `__field_reclaim_PVec`, whose array arm freed the replaced
`tail` under the leaf. Main's compiler already faults on the reduced shape;
the field read now takes the same count the local does, and the holder's
release leaves the leaf its buffer.

Pinned by `conformance/cases/variant_payload_from_array_field`, whose churn
allocates leaf-sized buffers so a freed leaf is handed out again and read
back wrong (24 against 56) rather than quietly intact.

## What is left, by bytes

On `parser.fern` the grow buffers are still 493 MB of the 1.05 GB, and the
survivors are `emit` copies whose local is consumed into ANOTHER local at its
last use — `var sr: LowerState = lower_expr(b.right, sl0)` with `sl0` never
mentioned again — so nothing releases the consumed generation. That is the
last-use release, the next slice. Then `__fern_str_concat` results (117 MB,
mostly the x86 text assembler's `x86_gas_trim` / `strip_suffix` / `reg`
temps), and the checker's `t_unknown` boxes.

## Pinned

`conformance/cases/struct_alias_thread_reclaim`,
`conformance/cases/snapshot_local_shared_field` (the caller's buffer read back
after the derived local's rebinds; `TestSelfHostSnapshotLocalKeepsSourceFieldsX86_64`
runs the same source under the sanitizer, where the stale free was a finding),
`conformance/cases/struct_state_handback`.

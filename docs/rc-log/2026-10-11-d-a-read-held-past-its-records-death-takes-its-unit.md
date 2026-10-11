# 2026-10-11 — a read held past its record's death takes its unit

`ssaunits.held_elements`, every target. Refs #11913, the copies `define`
still made under `semsource.method_call`. The append cliff compiling
`checker.fern` (`scripts/cliff-bench`, x86-64, stage 2 on the dev container):
30,032,608 bytes over 198,978 crossings to 24,148,960 over 193,521.

## The shape

```fern
let produced: Val = produce(k);
let s: St = produced.s;
if (s.n < 0) { return hold(produced); }
let recv: i32 = produced.id;
let contract: C = s.list[0];
let lent: Val = lend(Val { s: s, id: recv });
```

`produced` is used whole on the failure path, so `s` cannot be taken out of
it (`needed_past`). `s` is a field read anchored to `produced`, and
`contract`, read through `s`, is anchored to it too and lives past the
construction. In the unit model `produced` therefore lived to `contract`'s
last read: it was never consumed, only dead, and nothing released it before
the `Val` built from `s` retained the state instead of moving it. `lend`
and every define after it found a state with a count of 2, and the first
append into each of its arrays copied the array.

`method_call` is this shape: `expr(fa.obj, s, want)` returns the `Value`,
`return produced` on the failure path keeps it whole, and the `Type` read at
`s.values[recv]`, the `StructSig` and the `Contract` read out of `s` live to
the call's define. The forward watch in the previous entry showed the state's
last count released by that `Value` at the function's returns.

## The rule

A hold (#9849, #11834) gave a read a unit of its own when it was still in
hand where its source was *consumed*. It now does the same where the source
*dies unconsumed*, at the source's last direct read, for a read that is
consumed later itself or has a read anchored to it that is: the read takes a
unit at its own read, the chain through it is cut, and the source dies where
its own reads end. A read only borrowed to its death still leaves its source
to die with it, since nothing past it would have moved either.

A held read is a unit of its own, so what it is consumed beside is asked
again: `s` held at `produced`'s death is then consumed by the construction
while `contract` outlives, and `contract` takes its unit there under the
existing rule. The pass repeats until no read is added.

A read that is a tuple take already has its unit (`tuple_takes`), as a
taken payload does; a hold on it retained a second time, which the certifier
refused in `TestSelfHostSSAPhysicalRC`'s `shared-parent`. The hold now yields
to either take.

`TestSelfHostCrossBlockReuse`'s `read-outliving-the-attempt-takes-its-unit`
pins the shape above: `grow`'s append copied once and copies nothing.

`seminline.part_of` copied the round's value table once per round: the
edits seeded it with the function's own `values`, which the function still
held, so the first part phi's append copied every type in it. The round now
keeps the types of the values it makes apart (`Edits.added`, from `base`
up) and the function's table is extended once, when the round is applied.

## Measured

Stage-2 compilers on the dev container, compiling `checker.fern`:

| | bytes | crossings |
|---|--:|--:|
| before (4c038d0) | 30,032,608 | 198,978 |
| this change | 24,148,960 | 193,521 |

`FERN_RC_CERTIFY_DUMP` now prints the lifted function on a refusal as well
as under the report mode, which is how the double unit above was read.

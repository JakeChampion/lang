# 2026-09-10 — a field-place append's root must name a box this frame may grow (#8768)

## Shape

```fern
var o: Outer = mk();          // mk builds the box; its xs has spare capacity
var t: Inner = o.inner;       // t names the box o still holds
var ys: i32[] = t.xs.append(9);
return ys.len() * 10 + o.inner.xs.len();
```

Interpreter 43, x86-64 and arm64 44: the field-place mutation analysis
(`fieldPlaceMutationCopies`) proved no later read through the ROOT `t`
observes the grow, and the append ran in place at rc 1 — lengthening the
buffer `o.inner.xs` still reads. The buffer must have spare capacity for it to
show, so a literal (`cap == len`) never did; the shape needs a buffer built by
appends.

## Rule

The append decision (`appendDecision`, `fieldAppendRootOK`) now also requires
the root to be:

- the receiver or a parameter — the caller's box, under the #4873 grow bracket;
- a local the frame BUILT (`freshLocalRoots`): bound only from struct literals
  and from calls whose every value return is a box the callee constructed.

The call half is a new identity analysis, `findReturnsConstructedBox`. Neither
ownership fact serves: `returnsFreshBox` credits a returned OWNED parameter
(the caller retained it on the way in, so the count is the caller's — but its
other bindings still name that box), and `returnsNoParamEscape` excuses a bare
returned parameter as "not a flow-out". `var u = same(o)` with
`same(o: Outer): Outer { return o; }` was in place under both and answered 44.

Everything else copies, with the rule named in `fern -append-report`: a local
bound from a field read, an index, another name, a match arm, a `for` element
or a pass-through call.

## Measured

`fern -append-report examples/self_host/fern.fern`, x86-64:

| | sites | copying |
| --- | --- | --- |
| before | 5012 | 12 |
| after | 5012 | 142 |

130 sites move to the copy path: 42 in `arm64_gas_program` (root `p`, a local
bound from a call that hands a parameter back), the rest spread over the
irlower rc walks (`st.*`, `out.*` roots of the same kind). The issue predicted
104 under the self-host's stricter port rule; the fresh-call admission keeps
the difference in place.

Cost, x86-64 on the 4-core container, wall clock:

| | before | after |
| --- | --- | --- |
| native compiles the driver | 14.2 s | 14.9 s |
| the driver compiles fern.fern (stage 2) | 36.6 s | 35.4 s |

Both within run-to-run noise; the stage-2 output is byte-identical
(9,225,056 bytes) since the self-host compiler's own decisions are untouched.

## Gates

- `TestFieldAppendRootRule` (`internal/ir`): seven roots — param, literal,
  fresh call, field read, alias, pass-through call, match binding — read off
  the `AppendSite` the lowering records.
- rc corpus `field_append_root_bound_from_field_read_copies`: 43 on x86-64,
  arm64 and wasm, beside a fresh-call control that stays in place.

## Not taken

Widening the `.with` arm's root rule (`fieldSetInPlaceOK`, struct-literal
locals only) to fresh-call locals is the same admission and would be sound on
the same argument; it is left as it is because the move-out it guards has its
own measurement to do.

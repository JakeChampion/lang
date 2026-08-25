# A struct local reassigned from an alias reclaimed nothing — killer-drops slice 15

```
var p: P = P { xs: [7, 8] };
var keep: P = P { xs: [0] };
keep = p;
```

Four blocks a round, **80 allocs / 0 frees** over 20 rounds against native's 80/80.
Not one of them freed. The BIND form (`var keep: P = p;`) has been at parity all
along, so the split is REASSIGN vs BIND.

## Two refusals, both deliberate, both with their reason on record

- `reassigned_from_alias`: after `keep = p` the slot's rc fields are BORROWS of
  memory `p` still owns, so reclaiming would free the source's live fields
  (#3425 stage-2). Left to leak on purpose.
- `struct_bare_assigned_src`: `p` cannot be released either, because "a plain
  assignment retains nothing on the self-host, so the assignee holds the box
  uncounted". Its header states the precondition for lifting it outright:
  *"family knowledge UNTIL ASSIGNMENTS CARRY THE CO-EXTENSIVE RETAIN."*

That is the whole slice: carry the retain, and both premises dissolve. It is the
same move #7282 made for the alias BIND, one statement kind later.

## Keeping the pair co-extensive, given they are mutually dependent

The retain should fire only where the source is credited; the source can only be
credited if the retain fires. The cycle is broken by letting the STATIC pass decide
both: a forgiven source gets an `"ALIASSRC:"` row keyed on its binding site, and
`lower_stmt_assign` emits the retain iff that row is present. A source the escape
gate rejects earns no row, so no uncounted box is ever released. The target's
forgiveness then runs in a SECOND pass, asking whether the source ended up
CREDITED rather than merely a candidate.

## Two things this got wrong first

**The release must be the rc-gated walk, not the box-only one.** A reassigned slot
has two ownership regimes on different paths — its own fresh init OWNS its field
buffers, the aliased value SHARES the source's — and `"NODEEP:"` is per-slot, so it
can describe only one. Marking it leaks the initial buffers; omitting it walks the
source's fields twice. `"SINKSHARE:"` decides per VALUE at runtime: whichever owner
finds rc 1 does the deep work, the other takes the box dec.

**The retain has to be emitted where every return path passes.** Routing it through
`emit_arr_store`'s `alias_inc` looks natural and is wrong: the struct classes return
earlier — `emit_field_reclaim_store` and the snapshot paths take no `alias_inc`
parameter at all — so the retain was silently dropped while the credit was still
granted. That produced **exact native count parity, 80/80, with exit 99**. Only
`__rc_underflow_count()` dissented, which is the signature #7282's own note predicts
for this class of mistake: *"allocs == frees at live_bytes 0, so only the underflow
counter dissented."*

The retain is emitted immediately after the RHS is lowered, on the operand stack
(`__fern_rc_inc` returns its argument). The ARRAY path is untouched and still takes
its retain inside `emit_arr_store`.

## Results

| shape | before | after | native |
|---|---|---|---|
| struct alias reassign | 80/0 | **80/80** | 80/80 |
| same, read back after fresh arrays | value 9 | value **9** | 9 |
| struct alias BIND (control) | 40/40 | 40/40 | 40/40 |
| array reassign (control) | 40/40 | 40/40 | 40/40 |
| fresh-RHS reassign (control) | 80/80 | 80/80 | 60/60 |
| string accumulator (control) | 160/160 | 160/160 | 20/20 |

The fresh-RHS control is what established the diagnosis: the same slot reclaims
fully when the RHS is a literal, so the reclaim machinery was never the problem —
only the alias was refused.

All 95 rc probes in the scratchpad set were run through the before and after
compilers: two rows moved, both above, every exit code unchanged. Sanitizer clean
on all six shapes. The self-host still compiles itself under `FERN_STRICT_IR=1`.

## The string limb is NOT closed

`keep = s` over a string local is the exact analogue and stays at 40/0, a sound
leak, pinned as a row. The string classes reach their own reclaim predicates
(`slot_is_reclaimable_str` / `emit_str_reclaim_store`) rather than the struct credit
this slice forgives, so admitting them is its own slice — and the string
consume-rebind (`s = s + part`) that shares those predicates must not move, which is
why it is pinned here as a control.

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_struct_alias_reassign_test.go`, with 99 reserved for
the credit-without-retain failure above.

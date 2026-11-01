# 2026-11-01 — a `with` on a twin read counts for the first read

`semsink.updated_by`. Refs #13990.

## The shape

```fern
function (r: Recovery) __detect_lost(space: i32, now: i64): Recovery {
  let delay: i64 = r.__loss_delay();
  let lost: Sent[] = __lost_of(r.spaces[space], now - delay);
  return Recovery { ...r, spaces: r.spaces.with(space, r.spaces[space].__past_loss(now - delay, delay)), lost: lost };
}
```

`__past_loss` loops over the element's `sent` list and returns an update of
the element after the loop. Spliced, the element read sits before the loop,
and the `with` sits in the join block after it. The caller reads `r.spaces`
twice in its first block: once for the element `__lost_of` borrows and once
for the `with`.

## The rule

The kept-read rule of 2026-11-01-c asks whether a `with` on the first read
of the slot sits in the re-read's block. The index of `with` destinations
listed each `with` under the value it names. The second read of `r.spaces`
is a twin of the first, and the rule asks about the first read of the slot.
So the rule found no `with` for it. The sink pass read `r.spaces` again in
the join block, and the update after the loop copied the `Space`.

The index now lists a `with` under the first read of its slot. The rule then
keeps the first read. The plan takes the element out of its slot before the
loop, and the update after the loop writes in place.

## Measured

The rows below are from the same host.

- `TestSelfHostElementTakeThroughSplicedLoop`: 1,000 rounds allocate 2
  boxes against 1,000 before. The sanitized run is balanced on all three
  targets.

- With every method spliced, the serial probe of #13990 reads 48.7 a
  request, unchanged. The rule of 2026-11-01-e already covers the server's
  `__detect_lost`, whose `__lost_of` is spliced as well.
- In the default configuration the serial probe reads 46.8 and the mux
  probe 28.8, both unchanged. The five coreutils rows of 2026-11-01-a are
  flat.
- The instrumented compiler allocates 36,179,496 boxes on `checker.fern`
  against 36,179,501 before.

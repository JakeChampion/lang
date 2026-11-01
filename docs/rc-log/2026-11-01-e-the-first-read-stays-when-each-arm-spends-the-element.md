# 2026-11-01 — the first read stays when each arm spends the element

`semsink.read_above`, `child_spent_between`. Refs #13990.

## The shape

```fern
function (s: Session) __section(id: i64, fields: qpack.Field[]): Session {
  let i: i32 = s.__find(id);
  ...
  return Session { ...s, events: s.events.append(HeadersReceived(id, fields)), reqs: s.reqs.with(i, s.reqs[i].__headed(s.server, fields, length)) };
}
```

`Req.__headed` has four arms, and each returns an update of `r`. Spliced,
the element read of `s.reqs[i]` sits before the branch, and the `with` sits
in the join block. The sink pass read `s.reqs` again in the join block. The element read then
had no take, and the update in each arm copied the `Req`.

## Two conditions of the rule

The rule of 2026-11-01-c kept the first read under three conditions. Two of
them changed.

The parent condition read the dominance test the other way round from the
text of that entry. The code refused the rule when the parent was spent in a
block that dominates the first read. In the first pass, every argument of a
call counts as spent. The call `s.__find(id)` in the entry block therefore
refused the rule in `__section`.

The text said a spend in a block the first read dominates. With every method
spliced into its caller, the text's version costs 9.75 allocations a request
on the serial HTTP/3 probe. The condition is gone. Without it the probe reads
1 a request less than with the code's version, and the default configuration
moves nowhere.

The child condition asked for a descendant of the first read spent in a
dominator of the join block. The four arms of `__headed` are merged into
one update in `__section`, so that held there. In a body whose arms update
different fields, each arm spends the element, and none of the arms
dominates the join. The condition now asks for a descendant spent in a
block the first read dominates and not live into the join block.

A first form of this condition, measured together with the text's parent
condition, cost 8.75 a request. The two conditions were measured apart afterwards.

## Measured

- `TestSelfHostElementTakeThroughSplicedArms`: 1,000 rounds allocate 2,002
  boxes against 2,999 before. The sanitized run is balanced on all three
  targets.
- With every method spliced, the serial probe of #13990 reads 48.7 a request
  against 50.7, and the mux probe 30.7 against 32.8.
- In the default configuration, the serial probe reads 46.8 and the mux
  probe 28.8, both unchanged. The five coreutils rows of 2026-11-01-a are
  flat. The instrumented compiler allocates 36,179,501 boxes on
  `checker.fern`, against 36,176,921 on main on this host, +0.007%.

## Next lead

With every method spliced, the serial probe still reads 2 a request above
the one-block rule. The bisect of #13990 names `recovery.Space.__past_loss`,
`streams.Stream.__acked_send`, `streams.Stream.__sent` and
`streams.Streams.__initial_streams` at one each. `__past_loss` reads the
element before a loop and stores it back after the loop.

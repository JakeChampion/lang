# A taken array element is not also held

Follow-up to `2026-10-06-g-an-array-element-is-taken-at-its-read.md` (#11723),
which left main red on `TestSelfHostSpreadCarryElems*/with_through_copy`.

## The leak

Since #11723, an array element read can be a slot take (`payload_root`), and
the take is the read's unit. `held_elements` gives a unit to every element read
still in hand when its array is consumed. It asked only `retainable_element`,
and `array_get` is the one kind that answers yes there. A take whose element
outlived its array was therefore held as well:

- the hold retained the element at the read and released it at its death;
- the take's own unit, the emptied slot (or the shared arm's retain), was
  never released.

`outliving` now skips a read the frame already owns.

## Measured, x86-64, 100 rounds

| shape | before | after |
|---|--:|--:|
| `xs.with(0, …)` then `xs[0].v` (`TestSelfHostArrayElementTakeIsOneUnit`) | allocs 500, frees 400 | 500 / 500 |
| `with_through_copy` (spread copy, then `q.xs[0].v`) | 700 / 600 | 700 / 700 |

The new test fails on x86-64 and arm64 without the change. The record field,
tuple element and payload takes never met this: `retainable_element` admits
`array_get` alone.

# A taken array element is not held

2026-10-06: `ssaunits.units_of`. The `with_through_copy` probe of
`TestSelfHostSpreadCarryElems`, x86-64 and wasm.

## The shape

```fern
let q: H = H { xs: [In { v: i }, In { v: i + 1 }], n: 0 };
let z: H = H { ...q, n: 5 };
z = z.set0(i + 7);
return (q.xs[0].v + z.xs[0].v + z.xs[1].v + z.n + q.n) % 101;
```

Since #11723 the read `q.xs[0]` takes its slot: the array is the frame's
own and nothing reads it again, so the lowering tests the box for
uniqueness, nulls the slot when it passes and retains the element when it
fails (`ssarc.steal`). The element hold (#11680) read the same element as
still anchored to its array and in hand when the array was consumed, and
the lowering retained it once more, unconditionally: one `In` per round
never freed, 100 of 700 allocations on the probe, 40 bytes each on
x86-64 and 24 on wasm. The Test e2e self-host lane was red on main with
it from b6795704.

## The rule

A taken payload is a unit of its own at the read already, so it is not
held: `units_of` clears the hold on every read the payload takes admitted.
The hold runs after the takes are folded into ownership so that an element
read of a taken payload takes its own unit rather than pinning its array
(#9891), which is why the two can name one read; the take is the one that
moves the unit, and the hold's retain is the one that is not owed.

## Measured

`TestSelfHostSpreadCarryElemsX86_64` and `...Wasm`, `with_through_copy`:
700 allocations, 700 frees, 0 live bytes (from 600 frees). The unit-plan
driver (`TestSelfHostSSAUnits`) now refuses any plan that holds a read it
also takes.

# 2026-09-20 — an element read that outlives its array takes a unit

`examples/bench/sort_strings` ran 214x the native register backend on the
self-host, and only through the typed lowering: the AST lowering and native
are level. The shape is `__cmp_insertion`'s in `core/cmp`:

```fern
var v: T = a[i];
while (moving) {
    var o: i32 = a[j].cmp(v);
    if (o > 0) { a = a.with(j + 1, a[j]); ... }
}
a = a.with(j + 1, v);
```

In the unit model an element read is a projection, and a projection borrows
its array: `v` was anchored to `a` for as long as `v` lived. At the inner
loop's `.with`, and at the edge that carries `a` into the inner loop's phi,
`a` was therefore not dead, `ssaunits.choose` retained it instead of moving
it, `ssarc.sole_owned_base` found the count at 2, and the write copied the
array, retained every element and released the receiver's unit. The copy is
sole-held, so the rest of the inner loop wrote in place; the next outer
iteration read `v = a[i]` again and copied again. Thirty-one whole-array
copies of 16,000 strings per 32-element insertion run (#9849).

## The rule

An element read that outlives a consuming use of its array takes a unit of
its own at the read: `ssaunits.held_elements` marks it, `units_of` makes it
owned and anchored to nothing, and the physical lowering retains what it read
right after the read (`ssarc.held`), as `cell_read` already does for a cell.
The frame releases it when it dies like any unit of its own, and the array is
dead at the write, so it moves. A consuming use is an operation supply
(`with`, `append`, a counted call slot, a construction, a map mutation) or an
edge that supplies the array to an owned phi. A read that dies before any
such use stays a borrow, so `a[j].cmp(v)` costs nothing, and a view is never
held, since a view is lent.

A hold shortens a chain that is still live, so the plan's flow is derived
again over the new chains (`units_analysis`); a take's source dies at the
read, so a take alone leaves the flow as it was. The independent replay
derives the holds the same way and refuses a plan that disagrees (`element
hold disagrees with the plan`).

## Measured

| program | typed before | typed after | AST | native ssa |
|---|---|---|---|---|
| `__cmp_insertion`'s body, 4,000 strings | 1,174,291,618 | 5,794,659 | 4,264,356 | |
| `cmp.sort`, 2,000 strings | 297,510,956 | 11,658,223 | 8,771,984 | 9,018,051 |
| `sort_strings` (16,000 strings, twice) | 36,926,188,529 | 227,718,415 | | 172,792,688 |

Instructions retired under callgrind on x86-64. The production row's program
(eight strings sorted by length) allocates 17 times through the typed
lowering where it allocated 24, which is the AST lowering's count;
`TestSelfHostSemanticAllocationParity` pins that the produced bodies allocate
no more than the AST lowering on it, since neither the answer nor the leak
pins can see a copy.

## Census

The compiler's own sources produce whole (8678 of 8678).

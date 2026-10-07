# 2026-10-07 — a field read that outlives its record takes a unit

`ssaunits.held_elements`, extended from array elements to record fields and
tuple elements. Fixes #11834. Refs #8171.

## The shape

```fern
@noinline
function block(s: St, k: i32): St {
    let scope: Scope = s.scope;
    let i: i32 = 0;
    while (i < k) {
        s = step(s, i);
        i = i + 1;
    }
    return St { ...s, scope: scope };
}
```

`let scope = s.scope` cannot take its slot, because `s` is read again. In the
unit model it was a projection that borrows its record, so `s` stayed alive
for as long as `scope` did. At `step(s, i)` the record was therefore not dead.
The call retained it instead of moving it, `step` found a record with a count
of 2, and its first append to `values` copied the array.

## The rule

The element rule from #9849 now covers any read the frame can hold a unit of:
an array element, a record field or a tuple element, of a counted type that is
not a view (`retainable_read`). Such a read is still in hand when its root is
consumed, either past the consuming use or handed to it beside the root. It
then takes a unit of its own at the read: retained there, released when it
dies, anchored to nothing. The root moves.

## Measured

`TestSelfHostSemanticAllocationCounts`'s new
`field-read-outlives-the-record-move` makes 13 allocations where it made 51.

On a `checker.fern` compile (at 2e084b79) by production compilers, against
main at 4ae53835f:

| | before | this change |
|---|--:|--:|
| total Ir | 14.477 G | 14.473 G (−0.03%) |
| appends that copied a shared array | 232,786 | 208,879 |
| bytes those appends copied | 56.3 M | 46.7 M |
| stage 3 size | 10,780,784 | 10,812,760 (+0.3%) |

The compiler's own copies are mostly of short arrays, so the retain and
release each hold adds costs about as much as the copies it saves. The shape
pays in proportion to the array the callee would have copied.

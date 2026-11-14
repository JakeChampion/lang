# 2026-11-14 — a view out of a container keeps its array

All targets. `ssaunits.units_of`. Fixes #15942.

## The shape

```fern
struct Window { events: [i32] }

function field(k: i32): i32 {
    let events: i32[] = make(k);
    let w: Window = Window { events: events[:] };
    return w.events[0] + w.events[1];
}
```

The lowered function released `events` before the two element reads. The
reads took their values from a released block. Under `-harden` a released
block is zero, so the function returned 0. Without `-harden` the block kept
its old bytes until the allocator gave it out again, so no test saw the
defect.

## The cause

The analysis anchors the record to the array, because the record gathers a
view (`ssasem.view_roots`). The field read is anchored to the record. The
unit plan makes the field read an owned unit when it takes the field out of
the record. An owned unit has no parents, with one exception: a view keeps
the parents of its container, because a view is lent and holds no count of
its storage.

The exception tested `ssasem.is_view`, which is true only for the string
view type. An array view (`[i32]`, `[u8]`) failed the test. A record, a tuple or a
variant that holds a view failed it also. Each one lost its link to the
array at the take.

## What changed

The exception now tests `ssasem.holds_view`. Some units have a type that can hold a view. When the plan takes
such a unit out of a container, the unit keeps the parents and the
dependencies of that container.

## The gate

`TestSelfHostViewOutOfContainerKeepsItsArray` builds seven shapes with
`-harden` for x86-64-linux and arm64-linux: a record field, a record in a
record, a tuple, an `Option`, two reads of one field, an array element and
a byte view. Each shape returned 0 before the change.

## The trap

No lane ran the conformance corpus under `-harden`. The leak check does not
see a read of a released block. The corpus case
`array_views_see_builtin_writes` printed a wrong line under `-harden`. That
was the only sign.

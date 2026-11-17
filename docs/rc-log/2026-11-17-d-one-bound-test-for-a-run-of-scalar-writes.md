# One bound test for a run of scalar writes

2026-11-17. All targets. The change is in `ssabounds.grouped_accesses`, which was `grouped_reads`. It is
the last open item of #15747.

## The shape

```fern
@noinline
function put3(xs: i32[], at: i32): i32[] {
  return xs.with(at, 1).with(at + 1, 2).with(at + 2, 3);
}
```

Each `.with` tested its index against the length. The pass gave
one test to a run of reads of one array at constant offsets from one value.
It did not look at writes, and a `.with` ended the run.

## The rule

A `.with` of a scalar element is now a member of a run:

- It is silent. It changes no length. If a later test stops the program,
  nothing can see the write.
- The array of a member is the array that its chain of scalar writes
  started from (`written_root`). Each array of the chain has the same
  length.

The group test goes before the first member, on the receiver of that
member. Each member then has no test of its own. A run can mix reads and
writes.

A read of an index that an earlier access leaves in range is not a member.
The earlier access is in the same block, or in a block that dominates it.
The read merges with that access, so its test goes without a group. In the
inner loop of an insertion sort, a group of that read and the write after it
added three instructions to each turn (`sort_ints`, +16% on ARM64).

A write of a counted element is not a member. Such a write releases the
element that it replaces, and a release can run a finalizer.

## Measurement

`put3` on x86-64:

| | Before | After |
| --- | --- | --- |
| Bound tests (`cmpq` and `jae`) | 3 | 1 |

The one test is `at + 2` as an unsigned 64-bit value against the length. A
negative `at` is above each length as an unsigned value.

Witnessed: `TestSelfHostGuardBoundsX86_64`, `Arm64` and `Wasm`. Two new
twins count the traps: a run of four writes, and one write with three reads
of its result. Seven new rows must stop with the bound error:

- the far end
- a negative base
- a base at the top of `i32`
- a first member that is not the lowest
- writes to two arrays
- a read after a write
- a shared owner

## The trap

The group test has a line label of its own (`__fern_oob_abort.lN`). A count
of the text `__fern_oob_abort` in the assembly includes the label and its
jump. A twin with three writes had the same count as its unproven twin, so
the twin has four writes.

# An array element is taken at its read

2026-10-06: `ssaunits.payload_root`, `ssarc.steal`. The adjacency list
every graph pass builds.

## The shape

```fern
callers = callers.with(c, callers[c].append(i));
```

The element read borrowed the bucket, so the bucket stood at two counts
(the outer array's and the read's) and the append copied it whole, then
the with stored the copy and released the original: one copy of the
bucket per append, quadratic over a bucket's life. The static report
called the receiver a temporary nothing else holds, which is true of the
value and false of the buffer: the outer array holds it. 120 of 400
appends copied on the probe (capped), 26,616 in `suspend.classify` and
94,945 in `ssaunits.step_index` on a `checker.fern` compile.

## The rule

An array element read takes its slot the way a record field, a tuple
element or a variant payload does (`payload_root`, #11203): the array is
a unit of the frame's own, no later block names it, and after the read it
is reached again at most through one `with` on the same index value, the
store-back. An array's elements have no other slot a read can be proven to
name, so nothing else may read the array again; a second with from the
same box would carry the hole. The lowering is `steal`'s: a runtime
uniqueness test on the box, the slot nulled when it passes and the value
retained when it fails, so a shared array keeps every element and its
with copies as before. The with's release of the replaced element walks
past the null.

A read whose array is read no further takes with no store-back at all,
as the fixture `nested-projections` now shows: the array dies at the
read and its element goes on in the frame's own unit.

## Measured

`checker.fern` built for x86-64 by a stage-3 compiler of each tree,
callgrind `Ir`; the copies are `__arr_push_shared_count` and
`__arr_push_shared_bytes` over the same compile.

| | main | this rule |
|---|--:|--:|
| total Ir | 17.276 G | 17.267 G (−0.05%) |
| appends that copied a buffer with room | 470,377 | 299,449 |
| bytes those copies moved | 88.2 MB | 71.4 MB |
| `__fern_arr_push`, self | 304.8 M | 292.1 M |

A third of the compile's copies, and little of its time: the buckets it
copied were small (472 bytes in `classify`, 34 in `step_index`), and the
take pays a uniqueness test and a null store at every read it admits. The
rule is for the programs whose buckets are not small; the compiler is a
fixpoint under it. `TestSelfHostAllocDifferentialX86_64` holds the shape
at zero copies (`append-into-element`), and the ownership gate holds that
a shared array keeps its elements (`append-into-element-takes-only-a-
sole-owned-slot`).

## What is left

- 300 K copies remain, 71 MB. `semsource.define` is 21 MB of them: an
  attempt that may refuse and the retry from the same state
  (`method_call`), and the loop builders `match_chain`, `payloads`,
  `merge_env` and `header_phis`, each entered with its state shared.
- `checker.Scope.bind`, 133 K copies of 48 bytes: a flat scope copied
  per nested block, by design.

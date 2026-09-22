# 2026-09-21 — a slice retains the elements it copied

`conformance/cases/slice_views` produced **0 of its 111 declarations**:

```
FERN_SEM_IR: main: unsupported slice source: string[]
```

`semsource.array_slice` refused outright when the element type was counted, and
`ssasem.arr_slice_error` refused the same shape again in the graph verifier —
so a slice of anything but scalars took the whole module to the AST lowering.

## The copy aliases, so the copy retains

The self-host lowers `a[i:j]` on an array to `op_arr_slice`, a window COPY that
duplicates every element pointer into a fresh box. With counted elements that
box aliases boxes it holds no unit on, so releasing it decrements them — a free
of storage the source still holds, not a leak.

`__fern_arr_inc_elems` is the retain, and `sole_owned_base` already makes it
over the same `op_arr_slice` copy when it un-shares a receiver. The slice was
the one caller that did not, and the refusal was standing in for it.

## What the language actually says, and what the self-host does

Worth writing down, because the first version of this change's test was wrong
about it.

`a[i:j]` on an array is a **slice view**, type `[T]` — a borrowed window, not
an owned `T[]`. Native holds both halves of that: `var mid: string[] =
all[1:3]` is E003, and returning a view out of the frame that owns its backing
storage is E063, by name.

The self-host holds neither. `slice_expr` defines the result at the SOURCE's
type, so nothing in the typed path ever carries a `[T]`, and the escape rule
has nothing to fire on. The copy is what hides it: a "view" returned from the
self-host is a fresh array whose elements survive, where native says the same
program leaves a dangling window. That is **#9944**, filed rather than worked
around — this change is correct for the copy the self-host actually emits and
does not depend on the divergence, but its test cannot be written on a program
native rejects. The row is in the `[T]` spelling, inside one frame, and answers
what native answers.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`.

| program | before | after |
|---|---|---|
| `conformance/cases/slice_views` | 0 of 111 | **111 of 111** |

Its output matches `expected.stdout`, and the typed leg reclaims it whole —
`allocs=22 frees=22 live_bytes=0` — where the AST leg strands **312 bytes**.

Corpus census, 864 seeds, both columns against frozen binaries:

| | before | after |
|---|---|---|
| programs produced whole | 844 | **845** |
| declarations produced | 77,932 of 78,504 | **78,043 of 78,504** |

**Exactly one file moves and +111 is exactly its gap.** Nothing regresses. The
remaining corpus gap is 461 declarations over 19 files.

## Tests

`a-slice-retains-the-elements-it-copied` — 3 of 3, `noLeak`, all four targets,
differential against the AST leg, answering 19 as native does.

A missing retain is not a leak, so the usual `noLeak` pin is not what catches
it. Dropping the retain alone — leaving both refusals gone — makes the
sanitizer leg abort with `use-after-free (touched a quarantined block)`, which
is what the `x86-64-sanitize` target is in the row for. `[i32[]]` is in the
program too, because an element that is itself a counted array takes the same
retain at a different width.

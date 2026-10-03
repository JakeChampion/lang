# 2026-10-03 — the grow table re-evaluates a plan only once a callee gains a row

`ssaunits.grow_table`, `ssaunits.plan_callers`, and the runtime's
`__fern_write_file`. Refs #8171. The grow-table change emits no different
byte: the stage0-built compiler before and after emits the fixed older tree
(`examples/self_host/fern.fern` at 1ae9cad, its bindings spelled `let`)
and that tree's `checker.fern` byte for byte. The runtime change alters the
text of `__fern_write_file` and its mode variants in every program that
writes a file.

## What the profile named

`grow_table` settles every produced body's grow rows to a fixed point, and
ran `grow_rows` for every plan on every round: 181 k evaluations over the
compiler's 28 modules, 2.80 G inclusive, of which `grow_rows` was 2.69 G
and `dying_lent_rows`, called once per step, 0.91 G. A plan reads the
table only through rows filed under the callees its steps name, and every
row is filed under the key of the plan that produced it.

`__fern_write_file` copied the string into a fresh allocation a byte at a
time before `write(2)`, and released the copy after: 1.10 G of the
whole-compiler emit, writing 131 MB of asm text.

## What changed

`plan_callers` lists, once per module, the plans whose steps name each
plan's key. Appending a row marks the plans that name its key, and a pass
evaluates only marked plans; every plan starts marked. An unmarked plan's
inputs have not changed since it last ran, so it would hand back rows the
table already holds, and the table and the order of its rows are the
same. A first cut checked each plan's callees against the rows added since
it last ran; that check alone cost 0.88 G and is gone.

`__fern_write_file` writes from the string's own bytes through
`__str_bytes`, which borrows the string for the call.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver
the pinned stage0 builds from each tree, emitting the fixed older tree to
x86-64 asm text, against main at be489e35.

| | main | this change |
|---|--:|--:|
| total Ir | 145.50 G | 143.46 G (−1.4%) |
| `ssaunits.grow_table`, inclusive | 2.80 G | 0.76 G |
| `ssaunits.grow_rows`, inclusive | 2.69 G | 0.42 G |
| `ssaunits.plan_callers`, inclusive | — | 0.24 G |

The driver above carries stage0's runtime, so the write change shows only
in programs the new compiler builds: writing a 16 MB string to a file is
167.8 M instructions from main's compiler and 33.6 M from this one, the
rest being the string's construction.

## Witnessed

Both emit identities; the 709 targeted `internal/e2eselfhost` tests of the
previous entry; every `internal/e2eselfhost` test whose program calls
`write_file` or whose name names a writer (117); a file written and read
back byte for byte on x86-64 and on arm64 under qemu, with a write into a
missing directory still answering `Err`; `make check-sources` and the lint
ratchet.

## The trap

`scripts/selfhost-alloc-bench` builds the compiler with the native
backend, and read the grow-table change as 1.05 M fewer frees with the
same allocations. The self-host-built compiler does not leak: the native
backend never releases a local bound to an array field of an indexed
element, which `plan_callers` iterates (`for s in plans[i].steps`), #11177.
The same loop under the self-host compiler balances.

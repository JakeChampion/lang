# x86_64ssa froze the same freelist, from the opposite side (#9568)

`arm64ssa` allocated a string at `len + 9` and freed it at `len + 8`; where the
two rounded to different 16-byte classes the block went onto a class nothing
ever asked for, the freelist stopped recycling, and the heap grew without
bound. That was #9558, fixed by giving both ends one number, `strBlockBytes`.

`x86_64ssa` had the same disagreement with the operands swapped, and this is
what makes it worth writing down: the shapes are mirror images, so reasoning
from the twin gets the direction wrong.

## What the two backends actually disagreed about

On arm64 the bug was in the free: most producers were at `len + 9` and four
were at `len + 8` alongside a `len + 9` free.

On x86-64 the *free* is the majority-correct end. Its string layout carries no
trailing NUL in the documented size — `rc@base`, `len@base+4`, `data@base+8` —
and three producers match it exactly:

| producer | requested |
|---|---|
| `string_from_bytes_unchecked` | `len + 8` |
| `__str_slice` | `new_len + 8` |
| `__str_concat` | `total + 8` |

while six others reserve the NUL they write and request `len + 9`:
`Reader.read_line`, `Reader.read_chunk`, `read_dir`, `read_file`, `hostname`,
and the `io_error` message. `__fern_str_dec` freed at `len + 8`.

So on x86-64 it is the *producers* that were split, and the fix moves the
`+ 8` end up rather than the `+ 9` end down — the opposite of arm64. Sizing the
six down to `len + 8` would have been a one-byte heap overflow: each of them
stores a NUL at `data[len]`, which is the byte past a `len + 8` block.

## The boundary is one residue wide, and it is not the one arm64 has

Class rounding below 2048 bytes is `roundup16`. `roundup16(len+9)` and
`roundup16(len+8)` differ only when `len + 8` is exactly a multiple of 16, so
the bug bites at `len ≡ 8 (mod 16)` and nowhere else. `read_line` keeps the
newline, so a line of L characters stores `L + 1` and the drifting widths are
`L = 7, 23, 39, 55`.

Those are the same L values arm64 drifted at, by coincidence of a different
arithmetic — which is exactly the trap. The widths were taken from measurement,
not carried over.

## Two instruments, one of which lies

The first attempt measured RSS over a `read_line` loop at 200k and then 2M
lines, and it came back flat at every width, ssa and flat alike. That reading
was worthless twice over:

- `scripts`-side, the harness hardcoded `qemu-aarch64`. Every x86-64 binary it
  "ran" died instantly, so the numbers described a failed process.
- Even with the runner fixed, RSS could not have isolated this. The loop
  allocates an Option box per iteration beside the string; the box churns
  through its own class and moves the total around, masking which class is
  stranded.

`__heap_bump_bytes()` — the arena cursor, already exposed as a measurement
probe — answers the question directly: *bytes of fresh arena per line*. A
recycling freelist gives exactly `0`, because each line reuses the block the
previous line freed. A stranded class gives the whole class size, every line:

| L | before | after |
|---|---|---|
| 6, 8, 22, 24, 38, 40 | 0 | 0 |
| 7 | 32 | 0 |
| 23 | 48 | 0 |
| 39 | 64 | 0 |
| 55 | 80 | 0 |

A bound is the wrong assertion here. The correct value is `0`, so
`internal/e2e/x86_64ssa_string_block_recycle_test.go` asserts `0`.

## The builder was already right, and is now pinned

On arm64 the first version of the fix moved `__fern_str_dec` without moving the
`buf_*` builder, which hands its buffer out as a string through `buf_take`. The
block was then pushed onto the class *above* the one it came from and the next
request of that class wrote past its end — a corrupted live block, which is
worse than the leak being fixed.

x86-64's builder does not have that bug: `buf_new` asks for `cap + 1` payload
bytes and `emitBufStrBlock` adds the 8-byte header, so the buffer is
`cap + strBlockBytes` and a take freed at `len + strBlockBytes` with
`len <= cap` always classes at or below it. `buf_take_class_test.go` pins that
agreement anyway, with the canary construction from the arm64 twin: under-size
the builder by one byte and the test drops from 7 to 3.

## What the fix changed

`strBlockBytes = 9` in `internal/codegen/x86_64ssa`, used by every string
producer, by `__fern_str_dec`, and by both halves of `__fern_str_append`'s
in-place growth check. The bare `9`s already spelled out at the other nine
producer sites moved onto it too, so a future edit has one number to change.

Two `str_append` fixtures were tuned to the old capacity and moved by a byte:
reserving the NUL leaves a 4-byte string with three bytes of slack in the
16-byte class rather than a 5-byte string with three. They now match the arm64
twin's fixtures exactly.

## What this does not fix

The arm64 read-buffer retention (#9542) is untouched and is still why `ssa` is
not the default: `coreutils/uniq.fern` holds 369 KB at exit against the stack
machine's 368 bytes, with its freelist recycling normally and only 122
allocations — one large read buffer that is never freed. That is a plain
missing free, not a class-boundary problem.

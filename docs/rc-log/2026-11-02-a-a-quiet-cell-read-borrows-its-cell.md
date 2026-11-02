# 2026-11-02 — a quiet cell read borrows its cell

`ssasem.cell_borrows`, `ssasem.borrow_parents`, `ssaunits.owned_values`,
`ssarc.cell_read`. Follows the None-path lead of
`2026-11-01-g-a-branch-on-a-join-of-constants-goes-to-its-arm.md`.

## The shape

```fern
function (w: BufWriter) error(): Option[IoError] {
  return w.err.get();
}

function settle(w: BufWriter): Result[void, IoError] {
  ...
  if let Some(e) = w.error() {
    ...
  }
  return Ok(());
}
```

A cell read was a fresh unit, as a call result is. The read took a count on
the `Option` it handed out. The match read the tag. The None arm released
the `Option` again. `gnu.settle` paid a retain and a release for a value it
only looked at. That cost 14 instructions on every line `shuf` wrote.

## The rule

Nothing but a write to the cell or a call replaces what a cell holds. When
every use of a read comes before the first write or call on its path, the
cell keeps the content alive. Such a read is anchored to its cell
(`borrow_parents`) and owns no count (`owned_values`). `ssarc.cell_read`
emits no retain for it.

`cell_borrows` decides per read. The region a borrow can reach starts at
the read's block. It grows through each successor that has one predecessor,
a block of the region. Each block of the region ends at its first write or
call, or at its end.

Every use of the read, and of each projection off it,
must fall inside the region. An instruction use comes before its block's
limit. A phi operand or a terminator operand sits on a region block that
holds no write or call. A use outside leaves the read a unit.

A projection off a borrowed read is anchored to the read as before. A
payload that an arm keeps past the region takes its own count where it
leaves. A borrowed parameter has the same rule.

## Measured

x86-64, `-O`, instructions from callgrind.

| | before | after |
|---|--:|--:|
| `shuf -i 1-1000000` | 363,309,485 | 347,317,223 |

`gnu.settle` on the None path is now a load, a compare and a return. The
output is byte for byte the same.

The utilities that write a line at a time, each on the same input as the
previous entry:

| | before | after |
|---|--:|--:|
| `fmt -w 40` of prose | 627,875,327 | 625,175,307 |
| `fold -w 40` of prose | 51,144,277 | 51,139,559 |
| `cut -c1-3` of `seq.txt` | 187,668,114 | 187,663,702 |
| `cat -n` of `seq.txt` | 212,032,002 | 212,030,038 |

The compiler reads few cells, so the analysis costs it more than the borrow
saves. `fern.fern` built by a stage 1 with and without the borrow:

| | before | after |
|---|--:|--:|
| stage-2 compiler, bytes | 15,743,696 | 15,747,792 |
| stage 2 compiling `lexer.fern` to x86-64 asm, Ir | 1,723,013,913 | 1,723,501,086 |

Two runs of the same compiler differ by 100,000 instructions, so the cost
is 0.03%. The two stage-2 compilers emit the same assembly for
`lexer.fern`. The analysis returns early for a function that reads no
reference out of a cell, and runs its region search per read otherwise.

## Tests

`TestSelfHostCellReadBorrows` lifts four functions from the emitted ops. A
read used before any hazard carries no retain and no release. A read
followed by a write to the cell keeps a retain. A read followed by a call
that writes the cell keeps a retain. A read of a local cell, whose last use
is the read, carries no retain. The program exits 42 on every host target.

The census test runs the same program under the leak check and the
sanitizer.

## Next lead

### Implicit finalizer correction

An implicit drop can call a finalizer that replaces the cell's content.
A record construction is not an explicit call in semantic SSA.
A quiet read across that construction can therefore observe a freed snapshot.
The reproducer returns zero on main and one on the initial PR head, with balanced allocation counts in both cases.

The borrow proof now also checks whether the function's value types can hold a user finalizer.
It uses the existing recursive containment proof, including opaque values.
It checks this condition once per function with an otherwise quiet read.
Functions with possible finalizer effects retain the snapshot.
The region proof does not yet track implicit drop sites or edge drops.

The direct and array-contained finalizer regressions preserve the snapshot after the finalizer clears its cell.
The original quiet, local, written, and called controls still pass.
Both local test roots pass in 79.670 seconds without skips.
The earlier measurements retain their original source scope.

The guard is conservative in functions that contain resources.
The deterministic native ARM64 `shuf` control has identical output across three engines at 10,000 values.
The owner head records 3,266,702 instructions.
The repaired engine and the preceding main both record 3,436,719.
The repair restores correctness and the preceding main's cost in this path.
The evidence resides in `docs/benchmarks/cell-finalizer-snapshot-2026-11-02`.

A read matched in a block with two predecessors is still a unit. One
example is a value read before an `if` and matched after it. The region can
grow through a join when every predecessor of the join is in the region.

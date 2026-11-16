# A walk of a kept list borrows it

2026-11-16. All targets. The change is in the tail-call pass, the ownership
inference, the unit plan and the certifier.

## The shape

`safe` of nqueens walks a list of positions that its caller keeps. It ran
65% of the instructions of nqueens 10. Two faults were in that walk.

`safe` returns `a && b && c && safe(...)` from an arm of a value `match`.
The result of the self call goes through the phi of a join, and that join
returns it. `ssasem.tail_recursion` takes only a block that ends in the call
and returns its result. So `safe` stayed a call of itself, and the backend
made a jump to the entry: two jumps and one more move for each element.

The first fault hid the second. With `safe` written with
statements, `tail_recursion` makes a loop, and the loop is then inlined in
`append_safe`. nqueens 10 then ran 36,631,055 instructions in place of
22,218,662. The inlined loop took a count on the list, and it freed or
counted each node:

- The slot of a quiet cycle is eager, and an eager slot is consumed. A call
  with a value that lives on clears the flag (`ownership.kept_alive`). After
  the inline no call was left. The rule for a loop slot read only the values
  that the loop carries. It did not read the value that enters the loop.
- `ssaunits.phi_ownership` made a phi owned when the root of an argument was
  owned. The list of `append_safe` is owned, so the slot of the inlined loop
  was owned too.

## The change

### The tail call

`ssasem.returned_self_calls` runs before the test of `tail_recursion`. A join
that holds one phi and returns it can get the result of a self call. The
block of that call only jumps to the join. That block now returns the result
itself, and the join loses that predecessor. The block then ends in a self
tail call, and the pass makes the loop.

### The inference

`ownership.kept_alive` now reads the value that enters the loop
(`enters_alive`). It clears the flag of the eager slot when that value has a
later use, or when the frame does not own it. This is the rule of a call
argument.

### The plan

`ssaunits.borrows_entry` lets a loop slot borrow the value that enters it.
These are the conditions:

- Each other argument of the slot is a part of the slot.
- The value that enters has a use after each read of the slot, and of each
  value that borrows from the slot. The anchored table of the analysis names
  these values: a part, a copy, a view.
- No return and no other phi takes one of these values.

The slot can be that of a `tail_recursion` loop or the cursor of a `while`
loop.

### The certifier

The certifier gave a phi one unit of each input that held one. A function
that owns its list, walks it with a loop that borrows, and then reads it
again was thus refused: `spent without a unit`, at the release of the list.

`rccertify.spent_phis` finds the phis that an operation touches or a return
takes, and the phis that feed them. Nothing can spend a unit of another phi.
Such a phi is not tracked, and each of its inputs keeps its units.

A return counts also when the function has no contract. The first version of
the rule read only the contract, and `tests/stdlib/worker_test.fern` did not
compile.

For a function that the certifier passed before, each phi that took a unit
is spent, so the verdict is the same. One driver case became exact:
`reuse-of-a-loop-box-through-its-token` had one unproved value, the phi of
the token that the function only compares, and now it has none.

## Measured

Cachegrind on x86-64, against the build with the frame split:

| program | before | after | change |
|---|---:|---:|---:|
| nqueens 10 | 22,218,656 | 19,305,386 | -13.1% |
| nqueens 10, `safe` with statements | 36,631,079 | 19,305,836 | -47.3% |
| deriv 8 | 91,716,342 | 91,716,348 | 0.0% |
| rbtree 420,000 | 556,682,322 | 556,682,328 | 0.0% |
| rbtree-ck 420,000 | 830,568,594 | 830,568,600 | 0.0% |
| cfold 16 | 30,047,791 | 30,047,797 | 0.0% |
| 100 rounds of two `while` walks of 1,000 cells | 3,760,244 | 1,157,635 | -69.2% |

The loop of `safe` has 10 instructions for each element, and no frame. A
`while` walk that adds the cells has 4 instructions for each cell. The census
is balanced with the sanitizer on x86-64 and on arm64 for the five workloads.
The compiler has no such loop: its own text does not change with the rule
for `while` loops.

`TestSelfHostWalkOfAKeptListBorrows` has three walks of a list that the
caller keeps: one through `||`, one through `&&`, and one with statements.
The caller counts nothing and calls no release.

`TestSelfHostWhileWalkOfAKeptListBorrows` has two `while` walks in a function
that owns the list and reads it after the walks. That function retains
nothing. The program also has a cursor that leaves its loop and a cursor
that goes into a new cell at each step. The census is balanced on three
targets.

`TestSelfHostRcCertifyCases` has four new cases:

- The box of a phi that borrows is released.
- That box is never released.
- The phi is released with the box.
- A function with no contract returns a phi of a fresh box.

## Open

- A slot whose value another phi takes stays owned. A cursor that a branch
  in the loop can leave as it is has such a join.
- A slot that enters with a part of a value stays owned.

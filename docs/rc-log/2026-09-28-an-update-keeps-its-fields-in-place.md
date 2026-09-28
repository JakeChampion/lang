# An update keeps the fields it carries over in place

2026-09-28. `ssarc.block_keeps`, `keep_token`, `keep_release`. Refs #8171.

## Before

`s = S { ...s, f: v }` pairs the dying `s` with the construction as a reuse
donor. Every field the update carries over reaches the construction with a unit
of its own:

- a take at its read, which checks `s`'s uniqueness and nulls the slot; or
- a retain at the construction.

Then the donor gives up every child (`__sem_drop_S`), and the construction
reuses the box and stores every field back. For one changed field of 25 that is
25 uniqueness checks or retains, a walk of 25 slots and 25 stores. The two
native assemblers are update chains over a 25-field `X86Asm`, and
`x86_add_label` alone ran six of them, about 4,100 instructions per label in a
self-built compiler.

## Change

A field is kept in place when the construction stores it back into the donor's
slot of the same index, the operand is the donor's own `record_get` of that
slot, and it appears once among the operands. For a kept field:

- **When the donor is unique**, its slot's unit becomes the construction's, and
  none of the take, the retain or the release is emitted.
- **When the donor is shared**, the field is retained, and the donor is released
  whole.

The donor's uniqueness is read once, at the first kept read, into the token
slot. The reads, the retains, the donor's drop (`keep_release`) and the
construction all follow that one answer. A second read at the drop could
disagree with the first if another holder let go in between, and a kept field
would then leak a count.

The token slot is held from that first read to the construction, so a donor is
kept only when no other pairing's token is live in that window (`token_busy`).

## Measured

A stage-2 compiler (the native-built self-host compiler compiling `fern.fern`)
compiling `checker.fern` to a binary, under callgrind:

| | Ir |
|---|--:|
| before | 63.21 G |
| after | 58.72 G (−7.1%) |

The new stage 2's output for `checker.fern` is byte-identical to the new stage
1's.

`TestSelfHostSemanticReuseDifferentialX86_64` gains four cases:
- a unique update loop, where the kept path is witnessed by the
  children-drop helper's call count;
- a base shared with another binding;
- a carried-over field also copied into a second slot: the carried-over read
  is kept, the copy is not;
- one local named in two field positions, which must not be kept. With the
  single-occurrence guard loosened it over-releases and exits 99. Its fields
  are `i32[]` built from a parameter: a string built from literals folds to
  static data, which is never counted, so a case built on one cannot see a
  count go wrong.

The rc verifier models keep sites too. `irverifyrc.read_keep_site` pairs
`keep_release`'s select on the token with `keep_token`'s earlier select, so a
keep is checked for its uniqueness gate and for releasing the donor on the
decline arm. That release is `__sem_release_<T>`, now in the verifier's
release set (and in `internal/ir/rcsigs.go`, which the set is pinned to).
`irverify_run` checks 218 to 220 cover the clean, ungated and leaking shapes.

## Trap

Profiling the native-built self-host compiler does not find this. Native's own
codegen for the same source has costs of its own, such as a runtime call pair
on every self-append. Those hide what the self-built compiler spends: 87.1 G
against 63.2 G on the same compile. Build a stage 2 with `-g` and map
callgrind's addresses through `nm`. Callgrind does not read that symbol table
on its own.

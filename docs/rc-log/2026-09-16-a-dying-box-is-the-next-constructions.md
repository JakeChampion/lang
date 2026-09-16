# 2026-09-16 — a dying box is the next construction's

The typed path emitted no reuse. `ssarc` lowered every `record_new` as a fresh
`__fern_alloc`, so a `with`-style update allocated a second box of the same
shape one instruction after the first became garbage. Compiling the compiler
with `FERN_SEM_IR=` fired `__fern_alloc_reuse` 449 times; compiling it on the
typed path fired it zero times. The physical RC was correct and the allocation
traffic was the AST path's from before reuse existed.

**The pairing.** Perceus places a drop at a value's last use, so the record an
update supersedes dies BEFORE the record replacing it is built. That gap is the
whole opportunity: instead of returning the box to the allocator and taking
another of the same shape back, the drop keeps it as a token and the
construction builds in it. `reuse_pairs` walks a block once and matches an
earlier drop with a later `record_new` of an equal type that does not read the
dying value — this is Koka's ParcReuse, run over the unit plan's dying set
rather than over syntax. One pairing is live at a time and one token slot
serves the block; a second candidate arriving while one is pending is simply
left to drop normally.

**The token.** In place of the drop, `reuse_token` emits the recipe of
`docs/SELFHOST-PERCEUS-REUSE.md` §6.2: the donor's box when this frame holds
its only count, and null otherwise. The runtime helper is pure — it hands a
token back when the size class matches and allocates when the token is null —
so the shared arm releases the donor's count and passes null, and the
construction below it is one code path either way. The static plan already
proved the donor dead; the count test is what makes a hole in that proof
degrade to a fresh box rather than corrupt one another holder still reads
(#4350). The donor's slot is cleared after the test so nothing can reach the
box through the name it arrived under, and the step's remaining drops are
emitted by `drops_less`, which skips the one the construction took.

**The children.** A donor owning references gives its children up at the token,
through the same `__sem_drop_` helper `drop_value` calls under the same
uniqueness gate. That helper releases the CHILDREN ONLY, which is what makes it
the right one to call here: the box is not freed but handed on. It is owed
either way — the runtime's size-class mismatch path returns a donor block to
its freelist SHALLOWLY, so a caller that had not already released the children
would leak them on exactly the path that was supposed to be only slower.

**The shape word.** A struct box is `[shape, f0, f1, …]`, and `struct_make` is
what writes the first word. A construction building through a token never runs
that op, and the box it receives carries the right word only when the count
test passed — the fresh arm's block comes from `__fern_arr_box` with nothing in
it. So `reuse_token` reads the word off the donor, where it is live on both
arms, and `reuse_construct` writes it to whichever box arrives. The AST path
solves the same problem by copying the word from the donor in the fresh arm
only; it can, because it defers the donor's release until after the copy.

**Where the token lives.** A value's SSA id is its local slot, so the slots
above `nvals` are the frame's scratch, and every taker of scratch used to start
at `nvals` and grow. A token cannot: it is taken where a value dies and spent
at a construction further down the block, so a nested drop's scratch in between
would overwrite it. `token_slot` and `shape_slot` are now the two lowest slots
above `nvals` and `scratch_base` starts above them, which also replaces the
bare `f.graph.nvals` that ten scratch sites each spelled out.

## Measured

Firings of `__fern_alloc_reuse` compiling the compiler on the typed path: 0 →
**156**, against 449 for the AST lowering of the same sources.
`FERN_SELFHOST_NO_REUSE=1` takes it back to 0, which is what says the
differential switch reaches this layer — the first version of it did not, and
the on/off comparison was passing because both sides were the same build.

## Pinned

`TestSelfHostSemanticSourceRC` is the gate that carries the signal here:
allocations equal frees on all four targets, so a token handed to a
construction that was not entitled to it shows as a double free rather than as
a wrong answer. It gains `reuse_loop`, a record owning a string and an array
rebuilt from its own fields each iteration, and `reuse_shared`, where an array
holds a second count on the donor so the pairing is static but the count test
fails and the fresh arm runs — the path where a missing shape word corrupts the
box. Both were confirmed to fire before being pinned; a fixture that pins a
construction the pairing declines proves nothing.

`TestSelfHostReuseDifferentialX86_64` holds the other half — the program's
observable behaviour is identical with `FERN_SELFHOST_NO_REUSE=1` and without —
which is what says a reuse that fires changed only where the storage came from.

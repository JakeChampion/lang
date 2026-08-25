# An appended struct element whose source outlives the push — killer-drops slice 8

`ps = ps.append(p)` reclaimed nothing but its buffer whenever `p` was still
live afterwards. Measured over 100 rounds, x86, against native: a source read
after the push 400/200 (native 400/400), the same box pushed four times
400/200, a source rebound after the push 600/200 (native 600/600). The
container's element walk — the deep `__struct_drop_<P>` per element — was
simply not granted.

## The refuser was a duplicated predicate

The append-built ARRSTRUCT credit (#6535) admitted a bare-ident element only at
a site the construction-move analysis had taken over. Rebinding, re-reading or
re-pushing the source makes the push not its last use, so no move is recorded
and the element is neither a move nor a fresh literal.

Widening `arrstruct_self_append_elem` alone changed nothing, because the gate
that actually refuses is `arrstruct_elem_payload_escapes`, and its self-append
whitelist carried its OWN inline copy of the element test
(`is_fresh_struct_init(..) || arrstruct_moved_elem(..)`). The two had already
drifted. It now calls `arrstruct_self_append_elem`, so they agree by
construction.

## What replaces the move requirement

The pairing slices 5 and 7 established, applied to the container sink:

- **The append RETAINS** at every site the credit stamps. The stamp
  (`"APRETAIN:<line>:<col>"`) is issued by the same pass that grants the walk,
  so the inc and the dec are the same set of sites. Deriving the inc from the
  ELEMENT's own credit instead was a real bug on the way: a source with no
  credit got no inc while the container still dec'd once per push, so a box
  appended four times was released four times (exit 99).
- **Both walks are `__fern_rc_is_unique`-gated.**
  `emit_arrstruct_deep_free`'s per-element field drop now routes
  `emit_struct_field_drops_gated`, and the source's sweep already did via the
  `"SINKSHARE:"` marker, which this slice widens to the append position.
  Whichever owner reaches rc 1 does the deep work; the other takes the box dec.
  Sound in either order.
- **A MOVED element is excluded from the stamp.** It hands its single reference
  to the buffer and its own release is elided with the retain (#6726), so an inc
  there would strand every element at rc 1 after the walk — measured as the
  whole `esc_cont` structure leaking (10/2 against 10/10).

## The source keeps its credit, which is the half that pays

With only the container admitted the numbers do not move: the source is still
refused a reclaim credit, because `struct_box_sink_stored` reads an append as an
uncounted CONTAINER sink. That refusal was written for a retain that consulted
`slot_is_reclaimable_struct` and therefore skipped retired slots; a site-keyed
stamp has no such hole, so a STAMPED self-append is no longer a sink. Every
unstamped one — an append into an uncredited container, `with`, an array or
tuple element, a variant payload — still refuses, and must: an uncredited
container holds element pointers nothing retained, and it can outlive the
source through a return.

Deciding that needs the container's verdict before the struct family's gate
runs, so the ARRSTRUCT rows are computed up front (`arrstruct_credit_rows`) and
the stamps passed into the sink predicate. The two gates were never independent;
they only looked it.

## Results

| shape | before | after | native |
|---|---|---|---|
| source read after the push | 400/200 | **400/400** | 400/400 |
| struct PARAM element | 400/400 | 400/400 | 400/400 |
| same box pushed 4× | 400/200 | **400/400** | 400/400 |
| moved element (loop-scoped) | 1000/1000 | 1000/1000 | 1000/1000 |
| source rebound after the push | 600/200 | **600/500** | 600/600 |

Pinned by `internal/e2eselfhost/self_host_arrstruct_live_elem_test.go` across
x86 / arm64 / wasm, with 99 reserved for an over-release.

The last row is the one gap left, and it is not this slice's: a REASSIGNED
struct local earns no reclaim credit at all, so nothing releases the value it
holds at the exit. That is the struct-local reassign reclaim — the enum family
has it (`enum_reassign_reclaim_names`), the struct family does not.

Two rows of `self_host_arrstruct_bound_elem_test.go` changed meaning with this
slice: `outside-loop-elem-refused` and `read-after-push-refused` are now
`*-shared`, admitted and counted rather than refused. Their value assertions
(90 = a live buffer was freed and reused) are unchanged and are what proves the
counted hand-off, not a leak-safe refusal.

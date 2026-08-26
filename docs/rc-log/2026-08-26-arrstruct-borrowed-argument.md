# The struct-array twin of the ELB borrow tier

*2026-08-26* — the arrstruct half of `2026-08-26-arrenum-borrowed-argument.md`
(#7573). Same shape, same cause, different escape walker.

## The leak

`Inner[]` handed to a callee that only reads `src.len()` lost its element walk:

| probe | shape | before | native |
|---|---|---|---|
| producer-bound, `keep.len()` only | control | clean 4/4 | 4/4 |
| producer-bound, **call argument** | | **4/2** | 4/4 |
| literal-bound, `keep.len()` only | control | clean 3/3 | 3/3 |
| literal-bound, **call argument** | | **3/1** | 3/3 |

The binding source is not the axis; the argument position is.

## Which gate refused it was MEASURED

The credit expression has four gates. A temporary trace printing each verdict
per candidate said, for the caller's local: `PAYESC` — the ELEMENT gate,
`arrstruct_elem_payload_escapes` — while `arrstruct_unsafe_for` admitted the very
same call.

That asymmetry is the whole point, and it is the same one the `TUPB:` tier
records for rc-tuples. The box flag `arrstruct_unsafe_for` consults says the
callee never keeps the ARRAY, which licenses a box-only release. This class's
release WALKS the element boxes. So the element gate has to ask the stronger
question, and now consults the `"ELB:"` tier exactly as `arrenum_esc_expr` does.

Worth recording that the reading predicted this correctly and was still checked:
the trace took one run, and this area has punished readings repeatedly.

## The tier needed no work

`arrenum_elem_borrow_flags` gates on the param type ending in `[]` and on the
escape rule under an EMPTY registry. Neither is enum-specific, so the flag was
already `1` for `rd(src: Inner[], i)`. Only the consumer was missing.

The arrstruct credit gate also already threaded `borrowable`, so this slice was
smaller than #7573, which had to thread it through the whole arrenum walker.

## On the load-bearing check — stated precisely, not copied

Disabling the tier's element check puts the ARRENUM probe at exit 99 (#7573).
It does **not** break the struct-array `element_handed_out` case: that still
exits 25 at a clean 1400/1400.

The two releases differ. `emit_enum_variant_drops` FREES an element box and
zeroes the slot; this class's `__struct_drop_<T>` DECS it. A handed-out element
survives a dec, so it survives the struct-array walk where it would not survive
the enum one.

The check is kept anyway, and the refusals are pinned, for two reasons:

- Both consumers ask ONE shared tier. Having them ask different questions of it
  is a divergence hazard, not a saving.
- Refusing is the leak-safe direction, the established floor for this class.

If a later slice wants the struct-array side widened, it owes its own proof that
the dec is sufficient. This slice's silence is not that proof.

## Verification

`internal/e2eselfhost/self_host_arrstruct_borrowed_arg_test.go`, 6 cases: the two
fixed shapes and four callees that must keep refusing, including the churned
read-back of the handed-out element. Every want confirmed against BOTH oracles.

`TestSelfHostStage2FixpointArm64` green (156 s); the targeted rc set green
(359 s), including both construction matrices against their pinned files.

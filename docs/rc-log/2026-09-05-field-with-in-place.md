# 2026-09-05 — the field-receiver `.with` stores in place under the append's admission

`S { ...s, xs: s.xs.with(i, v) }` routed to `lower_arr_with_value`, which slices
the whole array and stores into the copy. The x86 assembler's label table pays
that per label, three times: `x86_add_label` threads `lab_head`, `lab_tail` and
`lab_next` through the `X86Asm` struct, and `lab_head` / `lab_tail` are 4093-slot
bucket arrays. Callgrind on a self-host-built (stage-2) x86-64 `-o` compile of
`checker.fern` put `__fn___fern_arr_slice` at **42.53%** of the run — 18.47 G of
43.43 G Ir (#8419). Native's `-append-report` already showed the four appends in
that function in place; the three `.with`s were the whole remaining clone.

## What changed

`field_append_inplace_sites_of` (the port of native's `fieldPlaceAppendCopies`,
2026-09-04) now collects a `<root>.<field>.with(i, v)` site alongside the append
sites, under the same rules — `fai_call_site_key` is the one predicate both the
candidate walk and the excused-read walk ask. The `.with` dispatch takes
`lower_field_with_inplace` for an admitted site on a SCALAR-element field
(`scalar_arr_field_type`); pointer-element fields keep the value form, since the
overwritten element would owe the container a release. (#8485 later widened that
to struct-, enum- and array-element fields, where neither form releases the
overwritten element; string elements, which do have release machinery, still
keep the value form.)

The lowering is `lower_field_append_inplace` with the store in place of the grow,
and one difference: `arr_push` carries its own rc == 1 gate and `arr_set` has
none, so the gate is spelled in the IR — `__fern_rc_is_unique` of the buffer
picks the field's own buffer or a full copy, after the #4873 share bracket has
had its say on the root box. The identity arm moves the field out of the root
exactly as the append does.

Native reaches the same store from the other end — a runtime helper rather than
a per-function admission. `__method_Array_set` emits
`__fern_arr_cow_inplace{,_ptr,_str}`, whose rc == 1 fast path returns the
receiver unchanged; `__fern_arr_cow_inplace_ptr` is why native already stores a
pointer-element field `.with` in place.

## Why the lifted form was not the fix

The arm64 assembler's twin lifted each chain array into a local before its
`.with` and stored it back (`a = T { ...a, head: head }`). Under the self-host
that store-back is a bare ident in a container literal, which
`aliased_array_names_of` credits flow-insensitively — so `head.with` and
`next.with` still cloned, only the `tail` stored in the RETURN literal landed in
place, and each label paid eight `__fern_struct_copy` calls for the extra
spreads. `arm64_asm_label` is back on the field-written form, which is the shape
this admission serves.

## Measurement

Stage-2 compilers built from the same source by the base and the new self-host
compiler, `-o checker.fern`, x86-64, this box:

| | base | new |
|---|---|---|
| total Ir (callgrind) | 43,431,439,007 | 25,746,650,710 (−40.7%) |
| `__fn___fern_arr_slice` | 18,473,454,634 (42.53%) | 2,157,109,897 (8.38%) |
| `-o` wall, 3 interleaved pairs, medians | 8.13 s | 5.72 s |
| `-emit asm` wall, medians | 3.98 s | 4.01 s |

The linked ELF and the emitted asm are byte-identical between the two.

## What the witness is

The bump high-water mark (`__heap_bump_bytes`) cannot see this clone: every
copy is the size class of the buffer it replaces, so the freelist recycles it
and both forms read flat. The shape test pins the gate instead — an admitted
`.with` body calls `__fn___fern_rc_is_unique`, the value form on a borrowed
receiver never does — and the differential cases in
`self_host_field_append_inplace_test.go` pin the answer on x86-64, arm64 and
wasm. wasmtime reports a WASI exit above 125 as its own exit 1, so every
oracle answer in that file stays below it.

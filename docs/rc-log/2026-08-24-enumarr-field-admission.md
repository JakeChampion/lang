# Enum-array fields walk their payloads — killer-drops slice 3

The construction-retain matrix's `enum_arr__fresh` cell: a struct field
`f: E[]` where a variant carries an array payload (`E.A(i32[])`) stranded
one payload buffer per construction. The struct drop's enum-array walk was
ONE level — arr_dec_ptr freed the element boxes and the buffer, but nothing
released the payload array inside each variant box. Not a killer drop
withheld: a drop that never existed at any depth.

The fix is a per-element-type walk helper, `__enum_arr_elems_drop_<E>`,
emitted on demand from the struct-drop arm in all three backends (x86
`emit_ir_enum_arr_elems_drop_one`, arm64 twin, wasm
`emit_wasm_enum_arr_elems_drop_body`): for a uniquely-owned buffer, per
uniquely-owned element, dispatch on the variant tag at box@0 (shape pointer
on the register backends, `$__tid` global on wasm) and dec the matched
variant's leak-safe-array payload fields ((k+1)*8) via the rc-guarded
`__fern_arr_dec`. The element boxes and buffer still free through the
caller's existing walk — the helper only adds the payload level.

## The admission predicate is the whole soundness argument

`enum_arr_elems_walk_ok` admits an enum only when EVERY variant's payload
fields are scalars or leak-safe scalar arrays (and some variant actually
carries one). That is the one payload kind whose construction counting is
guaranteed today: a variant ctor dups a bare-ident array payload (#3720)
and owns a fresh literal one, so the walk's guarded dec is balanced either
way — free the sole claim or return the dup. A string or nested-box payload
can reach the variant box uncounted, so those enums stay outside the walk
(shallow, leaking) until their construction retains exist. Widening the
admission without widening the counting is how this walk would turn a leak
into a use-after-free; the matrix is the regression net on both sides.

Verified per backend on the probe (`P { f: E[] }`, `E.A([..])`, 100
rounds): x86 and arm64 leakcheck 400/400 live 0, wasm exit parity with no
underflow. Flips the matrix's `enum_arr` construction cells that only
needed the drop to exist; the enum-array FIELD-REBIND flavor
(`__field_reclaim`'s enum-array arm) is a separate recorded slice, as is
the single-enum kind's broader floor.

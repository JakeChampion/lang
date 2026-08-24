# Strict-fresh enum-returning calls are owned — killer-drops slice 4

The matrix's `enum__call` cell: `P { e: mk(i) }` where mk returns a fresh
variant ctor stranded the variant box AND its payload every round (probe:
self-host 300 allocs / 100 frees, native clean, exits matching). The
ExprStructLit enum arm retains every non-ctor value — sound but
conservative: the call result arrived rc=1, the retain took it to 2, the
struct drop dec'd once, and the box stranded at 1.

The fix is the enum half of the admission the struct kind has had since the
deep-drop slices (`is_fresh_ret_binding` over `return_fresh_struct_ret_fns`):
"ENUM:"-prefixed entries in the same registry, built by
`fresh_enum_fwd_fixpoint` — free functions whose EVERY return is a fresh
variant ctor (`variant_ctor_enum_owner`) or a forwarding call to an
already-admitted member, iterated so chains ground out and ungrounded
mutual recursion stays uncredited. The enum arm skips its retain for an
admitted call (`enum_fresh_ret_call`), so the box hands over sole-owned —
exactly the class a ctor written at the field position has always been.

Ctor payload counting is position-independent (a bare-ident array payload
is dup'd at the ctor, #3720; a literal is owned), so a returned ctor is
indistinguishable from an inline one by the time the field owns it — the
admission adds no new counting obligation.

Flips `enum__call` AND `enum__fieldread`: the fieldread cell's holder is
itself built with `P { f: mkv(i) }`, so its strand was this same uncounted
call handover, not the read (the read's retain and both drops balance once
the holder owns its box). Verified on the probe per backend: x86 and arm64
leakcheck 300/300 live 0, wasm exit parity, strict arm64 whole-compiler
emit clean.

Remaining enum floors: local / param (the retain fires, the SOURCE claim
strands — the exit sweep's k_enum arm is a shallow box-only dec, #4357),
and the discarded-call statement (`mk(i);`) which the "ENUM:" entries do
not yet grant a reclaim.

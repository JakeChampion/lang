# Field reads into literals are counted shares — killer-drops slice 2

The construction-retain matrix's fieldread column said every kind leaked.
The probe ledger said otherwise about the mechanism: for the
guaranteed-retain kinds the READ was already retained — the leak was the
SOURCE. Reading `q.f` into a literal marked q as field-moved
(`optstruct_body_moves_field` → NODEEP), withdrawing q's deep drop, so
the shared buffer stranded at rc 1 with the retained claim never dec'd:
one inc, one withheld dec.

The fix is the #6623 exemption generalized: `fieldmove_counted_field_alias`
(né fieldmove_selfrebind_alias — the predicate was always just the
shape-and-kind test) now exempts a `name.<f>` value of a guaranteed-retain
kind at EVERY struct-literal field position, not only the self-rebind.
The literal incs it, the source keeps its deep drop, both decs balance.
Call-arg and return positions keep marking — no retain is guaranteed
there.

Flips `arr_i32__fieldread`, `struct__fieldread`, `struct_arr__fieldread`
to clean — exactly the kinds on the guarantee list. `enum__fieldread`
stays: the enum kind leaks on local/param/call too, a broader floor with
its own slice. string / string[] / enum-array fieldread stay until their
construction retains exist.

## The loop-carried strand, recorded

A LOOP rebind re-reading the same field (`while { var p = P { f: q.f } }`)
still strands the per-iteration retains: `__field_reclaim_<T>`'s cow
guard skips a pointer-equal field, and that skip is load-bearing for
spread carries (#6653's balance is retain-suppressed + cow-skipped, and
the spread base copy emits no inc). An rc>1-gated carried dec is unsound
too — a spread carry AFTER a retained share under-counts and frees under
the live holder. Resolving it needs the rebind emit to know the store is
a carry of the slot's own previous field, which is a per-site question,
not a helper-body one. The matrix's fieldread cells bind ONCE so the
read-counting axis stays clean; the carry interaction is this recorded
floor.

# A string local reassigned from an alias reclaimed nothing — killer-drops slice 16

```
var s: string = "ab" + "cd";
var keep: string = "zz";
keep = s;
```

**40 allocs / 0 frees** over 20 rounds — neither box. The BIND form
(`var keep: string = s;`) has been at parity since #7282, so this is the same
REASSIGN-vs-BIND split the struct limb had, in the class that keeps its own
reclaim machinery.

## Two halves refused it, and neither is what the struct limb lifted

- The **target** is dropped by a BLANKET `index_of_str(reassigned, name) < 0` in
  both STR collectors — any reassigned name, alias or not.
- The **source** is refused as a bare-ident escape, because
  `stmt_unsafe_for_alias_vb` consults `alias_ok` in its `StmtVar` arm and **not**
  in its `StmtAssign` arm. That asymmetry is the whole bug: a bind alias is
  forgiven, the byte-identical reassign alias is not.

The blanket is safe to narrow because the other reassigned-string shape never
depended on it. The accumulator (`s = s + part`) has its own collector
(`collect_str_accumulator_names`) and its own admission, and is lowered by
`emit_str_reclaim_store`, which emits no inc on purpose — a consume-rebind
replaces the slot's value with a fresh box rather than sharing one.

## Simpler than the struct limb, and the code says why

`#7282`'s note: *"A string is a single box with no field or element walk, so the
alias takes the SAME 'STR:' class rather than a shallow variant: there is no deep
release here to withhold from it."* So the `NODEEP:` / `SINKSHARE:` question that
cost `2026-08-25-struct-alias-reassign-counted.md` a build-and-measure cycle does
not arise here at all.

The retain is still emitted where every return path passes, right after the RHS is
lowered. `emit_str_reclaim_store` returns before `emit_arr_store` and takes no
`alias_inc`, so routing it there would drop the retain while the credit was still
granted — the failure whose signature is exact count parity with exit 99.

## One deliberate imprecision

The reassign forgiveness matches its target by NAME, not by binding-site key,
because an assignment carries only the target's name — there is no line/col to key
on. The precision loss is bounded and lands in the safe direction: two same-named
string locals in sibling blocks share a verdict, and for the STRING class sharing a
verdict leaks rather than frees. `#7253` names the struct credit as "the one class
where letting them share a verdict frees a live box rather than merely leaking",
and this is not that class.

## Results

| shape | before | after | native |
|---|---|---|---|
| string alias reassign | 40/0 | **40/40** | leaks nothing |
| same, read back after fresh strings | 120/80 | **120/120** | — |
| string alias BIND (control) | 40/40 | 40/40 | — |
| accumulator `s = s + part` (control) | 160/160 | 160/160 | — |
| fresh-RHS reassign (control) | 80/80 | 80/80 | — |

Native const-folds `"ab" + "cd"` and allocates nothing on most of these, so its
counts are not a retain comparison — what they establish is that it leaks nothing
and returns the same answers, which it does on every row.

All 95 rc probes were run through the before and after compilers: two rows moved,
both above, every exit code unchanged. Sanitizer clean on all six shapes, the
accumulator included. The self-host still compiles itself under `FERN_STRICT_IR=1`.

## Still refused, pinned as a row

**Borrowed PARAMS** — `var q: string = p; q = o;` with `p` and `o` both params,
80/0. Whether the release may fire depends on whether the slot's CURRENT value is
owned or borrowed, and the slot is a LOCAL, so the `slot >= n_params` guard does
not cover it. Releasing a borrowed param's value is a use-after-free the caller
sees, so this stays a sound leak until the slot can carry that distinction.

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_str_alias_reassign_test.go`.

`self_host_struct_alias_reassign_test.go`'s `string_alias_reassign_still_refused`
row goes with it. That row existed to pin this gap; the gap is closed, and keeping
it re-pinned at 40/40 would only duplicate the first row here without the
accumulator and fresh-RHS controls the string class actually needs.

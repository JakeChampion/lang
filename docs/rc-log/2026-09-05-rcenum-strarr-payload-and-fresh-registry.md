# The user rc-enum producer earns its consuming release (#7910 (d))

*2026-09-05* — `examples/self_host/irlower.fern`. The last `leak` row in the
self-host leak matrix, on both ISAs.

## The shape

```fern
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-…-" + t; }
enum E { Full(string[]), Note(string), Nil }
function mk(i: i32): E {
    if (i % 3 == 0) { return E.Full([w(i)]); }
    if (i % 3 == 1) { return E.Note(w(i)); }
    return E.Nil;
}
match (mk(i)) { E.Full(xs) => …, E.Note(s) => …, E.Nil => … }
```

`mk` never earned an `"RCE:"` row, so the consuming match released neither box
nor payload. The row's note blamed the call-scrutinee hoist; it is neither that
nor one defect. Isolating the variants as separate probes — the recipe §2 of
`SELFHOST-PERCEUS-REUSE.md` already prescribes — split it in two, and each half
refuses on its own:

| probe | before | after |
|---|---|---|
| `Note(string)` payload only, from `w(i)` | **leak** | clean |
| `Full(string[])` payload only | **leak** | clean |
| both (the matrix cell) | **leak** | clean |
| both, `Note` payload a literal | **leak** | clean |

Native is `clean` on all four, and every exit code matches it.

## Half 1 — the proof read an empty registry

`body_has_nonqualifying_rcenum_return` passed `[]` where
`fresh_rcpayload_enum_init` takes `str_fresh`, with a comment saying a syntactic
proof "avoids ordering the two registries against each other". There is no
ordering to avoid: the call site is

```fern
opt_fresh_ret_fns_of(fns, s.struct_decls, irlower.str_fresh_ret_fns_of(fns))
```

so the registry is complete before the proof runs, and the Option / Result
branch of that same function already reads it. With `[]` the string payload
`w(i)` could only be a syntactic literal or concat, so a factored producer —
the reason `"RCE:"` rows exist — disqualified its own enum.

## Half 2 — `string[]` was not a droppable payload

`enum_field_rc_droppable` admitted `string`, leak-safe scalar arrays and enum
arrays, but not `string[]`. `enum_all_variants_rc_droppable` bails on the first
non-droppable variant, so `Full(string[])` took `Note` and `Nil` down with it —
the same all-or-nothing failure #6758 recorded for `Seq(N[])`.

The deep free already existed: `__fern_str_arr_free`, what the struct-field
`k_str_arr` arm uses. What was missing was the payload-drop arm and an
admission gate, since the match-site free reaches each element:
`variant_struct_payloads_fresh` now requires an array literal of fresh elements
(`strarr_lit_all_elems_fresh_reg`, the registry-aware form), the same premise
the `string` arm states.

Purely additive: an enum with a `string[]` variant was refused at
`enum_all_variants_rc_droppable` before, so no shape that previously reclaimed
reaches the new gate.

## Witnessed

- `TestSelfHostLeakMatrixX86_64` and `TestSelfHostLeakMatrixIRArm64` — 150 cells
  each, `rcenum_mixed__callscrut__match` re-pinned `clean clean`, no other row
  moved. Both matrices are now `clean clean` throughout.
- `TestSelfHostCallScrutineeReleaseWasmIR/user_enum` — the shape was excluded
  from the wasm list as "refused on every backend"; it balances, so it is a case
  now and the exclusion comment is gone.
- The sanitize leg of each matrix cell (quarantine + trap) raised no
  over-release or use-after-free finding.

## Next lead

The three backends agree, so nothing here is contract-only. What this does NOT
touch is the whole-program compile: `make distcheck` still OOMs at 13.3 GiB
peak (measured this day, unchanged), and the matrix reaching 150/150 while that
stays red is the point `NATIVE-CONVERGENCE.md` precondition 1 now makes
explicitly — the grid covers enumerated shapes, not the compiler's own code.

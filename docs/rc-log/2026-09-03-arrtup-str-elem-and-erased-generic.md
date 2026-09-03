# 2026-09-03 — `(i32, string)[]` through an erased generic (#7910 (c))

`var ps: (i32, string)[] = [(i, w(i)), (i + 1, w(i + 1))]` handed to
`count[T](xs: T[]): i32 { return xs.len(); }`, 100 rounds, allocs/frees live:

| backend | native | self-host before | self-host after |
| --- | --- | --- | --- |
| x86-64 | 500/500 `0` | 500/100 **22400** | 500/500 `0` |
| arm64 | 500/500 `0` | 500/100 **22400** | 500/500 `0` |
| wasm | — | 500/100 **16800** | 500/500 `0` |

**Base.** The before/after rows above were measured against this branch's
base, 27e0ee4a4. The isolation table below was taken during the
investigation, one base earlier — the shape's verdicts are unchanged
between the two (the four headline probes measure identically on both),
but read its exact byte counts as of that tree.

The issue named the erased generic. Isolating one axis at a time says the
call was the second of two refusals, and the smaller:

| variant (x86-64) | native | before | after |
| --- | --- | --- | --- |
| no call at all, `ps.len()` | 500/500 | 500/100 **22400** | 500/500 `0` |
| declared, never read | 500/500 | 500/100 **22400** | 500/500 `0` |
| a MONOMORPHIC `count(xs: (i32, string)[])` | 500/500 | 500/100 **22400** | 500/500 `0` |
| the erased generic `count[T]` | 500/500 | 500/100 **22400** | 500/500 `0` |
| `(i32, i32[])[]`, no call | 500/500 | 500/500 `0` | 500/500 `0` |
| `(i32, i32[])[]` through the erased generic | 500/500 | 500/100 **15200** | 500/500 `0` |
| `string[]` through the erased generic | 300/300 | 300/300 `0` | 300/300 `0` |
| `P[]` (`P { s: string, k }`) through the erased generic | 500/500 | 500/500 `0` | 500/500 `0` |
| `(i32, string, i32[])[]` through the erased generic | 700/700 | 700/100 **30400** | 700/700 `0` |

## Refusal 1 — the string element was never admitted

The array-of-tuples credit ("ARRTUP:") proves each element tuple fresh with
`arrtup_lit_is_fresh`, which asked `tuple_arg_payload_fresh` — the sole-owner
flavour that deliberately carried an EMPTY fresh-string registry (#7374 split
its scope: "widening each is its own measured increment"). A string position
therefore admitted a literal or an inline concat and nothing else, so `w(i)`
refused the whole array and it kept the shallow buffer-only dec: every tuple
box and every string stranded, with no call in sight. That is the 22400, and
it is the same figure on every row with a string element.

This is that increment. `collect_fresh_arrtup_names`, `arrtup_lit_is_fresh`
and the "ARRTUPF:" producer registry (`fn_returns_fresh_arrtup`) now take the
registry the tuple LOCAL's credit already reads, and the element goes through
`tuple_arg_payload_droppable(…, false, str_fresh)`. Sound for the same reason
the local's element is: a registered producer's result is a fresh box the
tuple sole-owns, and `emit_tuple_type_child_drops`' string arm releases it
with the rc-aware `__fern_str_free`.

## Refusal 2 — a bare-ident call argument was an escape

`arrtup_elem_esc_shape` had no arm for a direct call with a bare-ident callee,
so the fold descended into the arguments and `ps` hit the bare-ident leaf:
code 2, escape. The array-of-structs twin (`arrstruct_elem_esc_shape`) returns
code 4 there and vets each argument: a bare-ident argument at a param proven to
touch no element ("ELB:") or to keep it only counted ("CNT:") is a borrow,
anything else escapes. The erased generic reading `xs.len()` is exactly an
"ELB:" param — which is why `P[]` through the same callee was already clean
and `(i32, i32[])[]` was not.

The ARRTUP scan now has the same code-4 arm, with `borrowable` threaded
through `arrtup_elem_payload_escapes` / `arrtup_elem_esc_expr` /
`arrtup_chain_esc` / `arrtup_elem_esc_at` the way the twin threads it. The
box-only flag would not do: this release walks the elements, so the proof has
to be about the elements.

## Witnessed

Leak-matrix rows `arrtup_str__literal__len`,
`arrtup_str__callarg__erased_generic`, `arrtup_arr__callarg__erased_generic`
and `arrtup_mixed__callarg__erased_generic` on x86-64 (with the sanitize leg)
and arm64; `TestSelfHostArrTupStrElemWasmIR` runs the same four on wasm with a
balanced census. Refusal 1 is what the literal row pins and refusal 2 what the
`arr` row pins; the `str` and `mixed` call rows need both.
